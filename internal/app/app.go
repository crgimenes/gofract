// Package app wires the renderer, the parameter state and the UI into an
// Ebitengine game.
package app

import (
	"fmt"
	"image"
	"math"
	"math/big"
	"sync"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/vmaciel/gofract/internal/params"
	"github.com/vmaciel/gofract/internal/renderer"
	"github.com/vmaciel/gofract/internal/ui"
	"github.com/vmaciel/gofract/internal/view3d"
)

// Config holds the start-up options coming from the command line.
type Config struct {
	HiDPI     bool
	LoadPath  string
	ParamPath string
}

// App is the Ebitengine game.
type App struct {
	cfg  Config
	st   params.State
	rend *renderer.Renderer
	hist params.History

	// Screen geometry, in the same pixel units the renderer works in.
	w, h      int
	scale     float64
	fontScale float64
	started   bool

	// tex mirrors the renderer's published frame on the GPU. texView is the
	// view that frame was computed for, which lets Draw reproject a stale
	// frame during a gesture instead of showing nothing.
	tex     *ebiten.Image
	texVer  uint64
	texView params.View

	ui        *ui.Context
	showPanel bool
	showHUD   bool

	needRender bool
	needColor  bool

	// Zoom-box gesture.
	boxing       bool
	boxX0, boxY0 float64
	boxX1, boxY1 float64
	// Pan gestures, by mouse and by arrow keys.
	panning      bool
	panX0, panY0 float64
	panStart     params.View
	keyPanning   bool
	// Continuous wheel zoom: a burst of wheel events is one history entry.
	lastWheel time.Time

	statusMu sync.Mutex
	status   string
	statusAt time.Time

	exporting bool // guarded by statusMu

	expMul        int // PNG export size, as a multiple of the window
	lastElapsed   time.Duration
	lastDone      bool
	lastProg      float64
	lastGuessed   float64
	lastPerturbed bool

	// cycleTick counts ticks since the last colour-cycling repaint.
	cycleTick int

	// The panel scrolls: with every section expanded its controls are taller
	// than a laptop screen.
	panelScroll  float64
	panelContent float64

	// open tracks which panel sections are expanded, keyed by section title.
	open map[string]bool
	// stopSel is the palette-editor's currently selected control point.
	stopSel int

	// 3D view: the landscape/globe projection of the flat fractal.
	mode3D             mode3D
	cam                cam3D
	drag3D             bool
	drag3DX0, drag3DY0 float64
	camAtDragStart     cam3D
	// heightGrid caches a resampled grid of colouring values, refreshed only
	// when a new 2D frame lands (see uploadFrame), and heightVer is the texVer
	// it was captured from — the cache key the 3D mesh builder checks against.
	heightGrid   []float32
	heightVer    uint64
	meshKeyCache meshKey
	meshCache    []view3d.Vertex
	vbuf3D       []ebiten.Vertex
	idx3D        []uint16
}

// New creates the game. The state is framed lazily, once the window size is
// known.
func New(cfg Config) *App {
	return &App{
		cfg:       cfg,
		rend:      renderer.New(),
		showPanel: true,
		showHUD:   true,
		scale:     1,
		expMul:    defaultExportMul,
		cam:       defaultCam3D(),
		// The palette editor stays folded away until asked for; everything
		// else is what you reach for constantly.
		open: map[string]bool{
			sectFormula: true,
			sectColor:   true,
			sectQuality: true,
			sectPalette: false,
		},
	}
}

// LayoutF maps the window onto the drawing surface. Returning the size in
// physical pixels keeps text crisp and the fractal sampled at native
// resolution on HiDPI displays.
func (a *App) LayoutF(outsideWidth, outsideHeight float64) (float64, float64) {
	s := 1.0
	if a.cfg.HiDPI {
		if m := ebiten.Monitor(); m != nil {
			s = m.DeviceScaleFactor()
		}
		if s <= 0 {
			s = 1
		}
	}
	w := math.Max(1, math.Ceil(outsideWidth*s))
	h := math.Max(1, math.Ceil(outsideHeight*s))
	a.scale = s
	if int(w) != a.w || int(h) != a.h {
		a.w, a.h = int(w), int(h)
		a.needRender = true
	}
	return w, h
}

// Layout satisfies ebiten.Game; LayoutF takes precedence at runtime.
func (a *App) Layout(outsideWidth, outsideHeight int) (int, int) {
	w, h := a.LayoutF(float64(outsideWidth), float64(outsideHeight))
	return int(w), int(h)
}

// ensureInit performs the setup that needs a known window size.
func (a *App) ensureInit() {
	if a.fontScale != a.scale {
		ui.InitFonts(a.scale)
		a.ui = ui.New(a.scale)
		a.fontScale = a.scale
	}
	if a.started {
		return
	}
	a.started = true

	if a.cfg.LoadPath != "" {
		if st, err := params.Load(a.cfg.LoadPath); err == nil {
			a.st = st
			a.setStatus("Loaded %s", a.cfg.LoadPath)
		} else {
			a.st = params.Default(a.w, a.h)
			a.setStatus("Could not load %s: %v", a.cfg.LoadPath, err)
		}
	} else {
		a.st = params.Default(a.w, a.h)
	}
	a.hist.Push(a.st)
	a.needRender = true
}

// Update runs one tick: input, then whatever work it scheduled.
func (a *App) Update() error {
	if a.w == 0 || a.h == 0 {
		return nil
	}
	a.ensureInit()

	mx, my := ebiten.CursorPosition()
	a.ui.BeginFrame(
		float64(mx), float64(my),
		ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft),
		justPressed(ebiten.MouseButtonLeft),
		justReleased(ebiten.MouseButtonLeft),
	)

	// Interaction pass over the widgets. It runs before the fractal gestures
	// so the panel gets first refusal on the pointer.
	a.ui.Begin(ui.PassInput, nil)
	if a.showPanel {
		a.buildPanel()
	}

	if err := a.handleKeys(); err != nil {
		return err
	}
	if a.mode3D != mode3DOff {
		a.handleMouse3D(float64(mx), float64(my))
	} else {
		a.handleMouse(float64(mx), float64(my))
	}
	a.handleDrop()
	a.ui.EndFrame()
	a.advanceCycle()

	switch {
	case a.needRender:
		a.startRender()
	case a.needColor:
		a.rend.Recolor(renderer.SpecFrom(a.st.Color, a.st.Fractal()))
		a.needColor = false
	}
	return nil
}

// advanceCycle animates the palette rotation. Cycling is a re-colour of cached
// values rather than a re-render, so it costs a lookup-table pass and no
// iteration at all — which is the whole reason the renderer keeps the values.
//
// That pass is still real work: about 6 ms at 1080p and twice that on a Retina
// surface, against a 16 ms frame budget. So the repaint is throttled to roughly
// a third of the budget. The rotation itself advances every tick regardless, so
// the animation runs at the speed asked for either way — only its frame rate
// drops on a large window.
func (a *App) advanceCycle() {
	if !a.st.Color.Cycle {
		a.cycleTick = 0
		return
	}
	tps := float64(ebiten.TPS())
	if tps <= 0 {
		tps = 60
	}
	a.st.Color.Offset = wrap01(a.st.Color.Offset + a.st.Color.CycleSpeed/tps)

	every := 1
	if budget := time.Duration(float64(time.Second) / tps / 3); budget > 0 {
		if cost := a.rend.RecolorCost(); cost > budget {
			every = min(int(cost/budget)+1, 8)
		}
	}
	a.cycleTick++
	if a.cycleTick >= every {
		a.cycleTick = 0
		a.needColor = true
	}
}

// startRender submits the current state to the renderer.
func (a *App) startRender() {
	a.needRender = false
	a.needColor = false
	f := a.st.Fractal()
	if f == nil {
		return
	}
	a.rend.Start(renderer.Request{
		W:     a.w,
		H:     a.h,
		SS:    a.st.Super,
		View:  a.st.View,
		Frac:  f,
		Opts:  a.st.Options(),
		Color: renderer.SpecFrom(a.st.Color, f),
		Guess: a.st.Guess,
	})
}

// Draw paints the frame: fractal, gesture overlay, HUD, panel.
func (a *App) Draw(screen *ebiten.Image) {
	if a.ui == nil {
		return
	}
	a.uploadFrame()
	if a.mode3D != mode3DOff {
		a.draw3D(screen)
	} else {
		a.drawFractal(screen)
	}

	a.ui.Begin(ui.PassDraw, screen)
	if a.mode3D == mode3DOff {
		a.drawBox()
	}
	if a.mode3D != mode3DOff {
		a.drawHUD3D()
	} else if a.showHUD {
		a.drawHUD()
	}
	if a.showPanel {
		a.buildPanel()
	}
	a.drawStatus()
}

// uploadFrame copies a newly published frame to the GPU. Ebitengine's
// WritePixels wants an exactly matching size, so the texture is recreated
// whenever the render size changes.
func (a *App) uploadFrame() {
	var newFrame bool
	a.rend.WithFrame(func(f renderer.Frame) {
		a.lastElapsed = f.Elapsed
		a.lastDone = f.Done
		a.lastProg = f.Progress
		if f.Done {
			a.lastGuessed = f.Guessed
			a.lastPerturbed = f.Perturbed
		}
		if f.Img == nil || f.Version == a.texVer {
			return
		}
		b := f.Img.Bounds()
		if a.tex == nil || a.tex.Bounds() != b {
			a.tex = ebiten.NewImage(b.Dx(), b.Dy())
		}
		a.tex.WritePixels(f.Img.Pix)
		a.texVer = f.Version
		a.texView = f.View
		newFrame = true
	})
	if !newFrame {
		return
	}
	// HeightGrid takes the renderer's own lock, so this has to run after
	// WithFrame has released it — calling it from inside that closure would
	// deadlock on a non-reentrant mutex.
	if grid, ok := a.rend.HeightGrid(heightSampleW, heightSampleH); ok {
		a.heightGrid = grid
		a.heightVer = a.texVer
	}
}

// drawFractal blits the last frame, reprojected when the view has moved on
// since it was computed. Without this a pan or a wheel zoom would show a
// blank window until the next pass lands.
func (a *App) drawFractal(screen *ebiten.Image) {
	if a.tex == nil {
		screen.Fill(image.Black.C)
		return
	}
	tb := a.tex.Bounds()
	tw, th := float64(tb.Dx()), float64(tb.Dy())
	cur := a.st.View
	k := a.texView.Scale / cur.Scale

	// How far the stale frame's centre sits from the current one, in current
	// pixels. Taking the difference at the view's precision matters at deep
	// zoom: as float64 both centres would round to the same value and the
	// preview would not track the gesture at all.
	prec := max(a.texView.Prec(), cur.Prec())
	ddx := new(big.Float).SetPrec(prec).Sub(a.texView.CX.Big(prec), cur.CX.Big(prec))
	ddy := new(big.Float).SetPrec(prec).Sub(a.texView.CY.Big(prec), cur.CY.Big(prec))
	offX, _ := ddx.Float64()
	offY, _ := ddy.Float64()

	op := &ebiten.DrawImageOptions{}
	op.Filter = ebiten.FilterLinear
	op.GeoM.Scale(k, k)
	op.GeoM.Translate(
		float64(a.w)/2-tw/2*k+offX/cur.Scale,
		float64(a.h)/2-th/2*k-offY/cur.Scale,
	)
	// Anything the stale frame does not cover shows through as black.
	screen.Fill(image.Black.C)
	screen.DrawImage(a.tex, op)
}

// drawBox paints the zoom marquee.
func (a *App) drawBox() {
	if !a.boxing {
		return
	}
	r := a.boxRect()
	if r.W < 1 || r.H < 1 {
		return
	}
	a.ui.Stroke(r, 1*a.scale, ui.Theme.Marquee)
	// Crosshair at the box centre, the point that will become the new centre.
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	d := 5 * a.scale
	a.ui.Fill(ui.Rect{X: cx - d, Y: cy - 0.5*a.scale, W: 2 * d, H: a.scale}, ui.Theme.Marquee)
	a.ui.Fill(ui.Rect{X: cx - 0.5*a.scale, Y: cy - d, W: a.scale, H: 2 * d}, ui.Theme.Marquee)
}

// boxRect returns the aspect-locked marquee in screen coordinates.
func (a *App) boxRect() ui.Rect {
	aspect := float64(a.w) / float64(a.h)
	dx := a.boxX1 - a.boxX0
	dy := a.boxY1 - a.boxY0
	// Lock the marquee to the window aspect so the zoom never distorts.
	ext := math.Max(math.Abs(dx), math.Abs(dy)*aspect)
	w := math.Copysign(ext, nonZero(dx))
	h := math.Copysign(ext/aspect, nonZero(dy))
	x, y := a.boxX0, a.boxY0
	if w < 0 {
		x, w = x+w, -w
	}
	if h < 0 {
		y, h = y+h, -h
	}
	return ui.Rect{X: x, Y: y, W: w, H: h}
}

func nonZero(v float64) float64 {
	if v == 0 {
		return 1
	}
	return v
}

// setStatus posts a transient message to the bottom of the window. It is safe
// to call from a background goroutine.
func (a *App) setStatus(format string, args ...any) {
	a.statusMu.Lock()
	a.status = fmt.Sprintf(format, args...)
	a.statusAt = time.Now()
	a.statusMu.Unlock()
}

func (a *App) drawStatus() {
	a.statusMu.Lock()
	msg, at := a.status, a.statusAt
	a.statusMu.Unlock()
	if msg == "" || time.Since(at) > 5*time.Second {
		return
	}
	pad := 10 * a.scale
	w := ui.TextWidth(msg, ui.FaceUI) + 2*pad
	r := ui.Rect{X: 12 * a.scale, Y: float64(a.h) - 40*a.scale, W: w, H: 26 * a.scale}
	a.ui.Fill(r, ui.Theme.Overlay)
	a.ui.Text(r.X+pad, r.Y+(r.H-ui.LineH)/2+1, msg, ui.FaceUI, ui.Theme.Text)
}
