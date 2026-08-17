// Package ui is a very small immediate-mode widget layer built directly on
// Ebitengine's drawing primitives.
//
// It exists because a fractal explorer needs about eight kinds of control and
// nothing else; pulling in a full GUI toolkit would dominate the dependency
// list. Widgets run twice per frame: once in Update to resolve interaction,
// once in Draw to paint. Splitting the passes keeps all state mutation inside
// Update, where Ebitengine guarantees it happens exactly once per tick.
package ui

import (
	"bytes"
	"image/color"
	"log"
	"math"
	"slices"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
)

// Theme is the (dark, modern) colour scheme. WinFract's spirit, not its
// 16-colour palette.
var Theme = struct {
	Panel      color.RGBA
	PanelEdge  color.RGBA
	Text       color.RGBA
	TextDim    color.RGBA
	Accent     color.RGBA
	Widget     color.RGBA
	WidgetHot  color.RGBA
	WidgetDown color.RGBA
	Track      color.RGBA
	Overlay    color.RGBA
	Marquee    color.RGBA
	Bad        color.RGBA
}{
	Panel:      color.RGBA{22, 24, 30, 236},
	PanelEdge:  color.RGBA{58, 62, 74, 255},
	Text:       color.RGBA{226, 229, 238, 255},
	TextDim:    color.RGBA{138, 145, 162, 255},
	Accent:     color.RGBA{86, 156, 255, 255},
	Widget:     color.RGBA{44, 48, 60, 255},
	WidgetHot:  color.RGBA{58, 64, 80, 255},
	WidgetDown: color.RGBA{70, 108, 168, 255},
	Track:      color.RGBA{34, 37, 46, 255},
	Overlay:    color.RGBA{12, 14, 18, 190},
	Marquee:    color.RGBA{255, 255, 255, 255},
	Bad:        color.RGBA{233, 106, 106, 255},
}

// Fonts. Sizes are in logical pixels and get scaled with the display.
var (
	FaceUI    *text.GoTextFace
	FaceBold  *text.GoTextFace
	FaceSmall *text.GoTextFace
	// FaceMono is for formula text, where character alignment carries meaning
	// and a proportional font makes a bracket mismatch harder to spot.
	FaceMono *text.GoTextFace
	LineH    float64
)

// InitFonts prepares the faces at the given scale factor (1 for a standard
// display, 2 for HiDPI). It must be called before any drawing.
func InitFonts(scale float64) {
	reg, err := text.NewGoTextFaceSource(bytes.NewReader(goregular.TTF))
	if err != nil {
		log.Fatalf("ui: loading regular font: %v", err)
	}
	bold, err := text.NewGoTextFaceSource(bytes.NewReader(gobold.TTF))
	if err != nil {
		log.Fatalf("ui: loading bold font: %v", err)
	}
	mono, err := text.NewGoTextFaceSource(bytes.NewReader(gomono.TTF))
	if err != nil {
		log.Fatalf("ui: loading mono font: %v", err)
	}
	FaceUI = &text.GoTextFace{Source: reg, Size: 13 * scale}
	FaceBold = &text.GoTextFace{Source: bold, Size: 13 * scale}
	FaceSmall = &text.GoTextFace{Source: reg, Size: 11 * scale}
	FaceMono = &text.GoTextFace{Source: mono, Size: 12 * scale}
	LineH = 16 * scale
}

// Rect is an axis-aligned rectangle in screen coordinates.
type Rect struct{ X, Y, W, H float64 }

// Contains reports whether (x, y) is inside r.
func (r Rect) Contains(x, y float64) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// Inset shrinks r on every side.
func (r Rect) Inset(d float64) Rect { return Rect{r.X + d, r.Y + d, r.W - 2*d, r.H - 2*d} }

// Pass distinguishes the interaction pass from the painting pass.
type Pass int

const (
	// PassInput resolves hit-testing and mutates the bound values.
	PassInput Pass = iota
	// PassDraw paints, and mutates nothing.
	PassDraw
)

// Context carries input state, widget focus and the current pass.
type Context struct {
	Pass  Pass
	Scale float64

	dst *ebiten.Image

	MX, MY   float64
	down     bool
	pressed  bool
	released bool

	// active is the widget holding the pointer; it persists across frames so
	// a slider keeps tracking after the cursor leaves its track.
	active string

	// focus is the text field receiving keystrokes, and caret the insertion
	// point within it, counted in runes.
	focus string
	caret int
	// chars is scratch for the frame's typed characters, reused to keep the
	// input path allocation-free.
	chars []rune

	// Captured is true when the pointer is over UI chrome, so the caller
	// knows not to treat the click as a gesture on the fractal.
	Captured bool
}

// New returns a context for the given display scale.
func New(scale float64) *Context { return &Context{Scale: scale} }

// BeginFrame records the input state for this tick. Call once from Update,
// before the input pass.
func (c *Context) BeginFrame(mx, my float64, down, pressed, released bool) {
	c.MX, c.MY = mx, my
	c.down, c.pressed, c.released = down, pressed, released
	c.Captured = false
}

// EndFrame releases the active widget. It must run after the input pass, not
// before, or a widget would never see the release that completes its click.
func (c *Context) EndFrame() {
	if c.released {
		c.active = ""
	}
}

// Begin starts a widget pass.
func (c *Context) Begin(p Pass, dst *ebiten.Image) {
	c.Pass = p
	c.dst = dst
}

// Active reports whether any widget currently owns the pointer.
func (c *Context) Active() bool { return c.active != "" }

// Editing reports whether a text field has keyboard focus. The caller must
// suppress its own shortcuts while it does, or typing a formula would trigger
// half the application.
func (c *Context) Editing() bool { return c.focus != "" }

// Unfocus drops keyboard focus, for Escape and for hiding the panel.
func (c *Context) Unfocus() { c.focus = "" }

// Capture marks a screen region as UI chrome.
func (c *Context) Capture(r Rect) {
	if r.Contains(c.MX, c.MY) {
		c.Captured = true
	}
}

// --- primitives -------------------------------------------------------

// Fill paints a solid rectangle.
func (c *Context) Fill(r Rect, col color.Color) {
	if c.Pass != PassDraw {
		return
	}
	vector.FillRect(c.dst, float32(r.X), float32(r.Y), float32(r.W), float32(r.H), col, false)
}

// Stroke paints a rectangle outline.
func (c *Context) Stroke(r Rect, width float64, col color.Color) {
	if c.Pass != PassDraw {
		return
	}
	vector.StrokeRect(c.dst, float32(r.X), float32(r.Y), float32(r.W), float32(r.H), float32(width), col, false)
}

// Text draws a string with its top-left corner at (x, y).
func (c *Context) Text(x, y float64, s string, face *text.GoTextFace, col color.Color) {
	if c.Pass != PassDraw || face == nil {
		return
	}
	op := &text.DrawOptions{}
	op.GeoM.Translate(x, y)
	op.ColorScale.ScaleWithColor(col)
	text.Draw(c.dst, s, face, op)
}

// TextRight draws a string ending at x.
func (c *Context) TextRight(x, y float64, s string, face *text.GoTextFace, col color.Color) {
	if c.Pass != PassDraw || face == nil {
		return
	}
	w, _ := text.Measure(s, face, 0)
	c.Text(x-w, y, s, face, col)
}

// TextWidth measures a string in the given face.
func TextWidth(s string, face *text.GoTextFace) float64 {
	if face == nil {
		return 0
	}
	w, _ := text.Measure(s, face, 0)
	return w
}

// --- widgets ----------------------------------------------------------

// widgetState resolves hover/press bookkeeping shared by every widget.
// It returns whether the widget is hovered, held, and whether it was
// clicked (press and release inside).
func (c *Context) widgetState(id string, r Rect) (hover, held, clicked bool) {
	hover = r.Contains(c.MX, c.MY)
	if c.Pass == PassInput {
		if hover && c.pressed && c.active == "" {
			c.active = id
		}
		// A click needs press and release on the same widget, so a drag that
		// wanders off before releasing is correctly discarded.
		if c.active == id && c.released && hover {
			clicked = true
		}
	} else {
		hover = hover && (c.active == "" || c.active == id)
	}
	held = c.active == id && c.down
	return
}

// Button draws a labelled push button and reports a click.
func (c *Context) Button(id string, r Rect, label string) bool {
	hover, held, clicked := c.widgetState(id, r)
	if c.Pass == PassDraw {
		col := Theme.Widget
		switch {
		case held:
			col = Theme.WidgetDown
		case hover:
			col = Theme.WidgetHot
		}
		c.Fill(r, col)
		w := TextWidth(label, FaceUI)
		c.Text(r.X+(r.W-w)/2, r.Y+(r.H-LineH)/2+1, label, FaceUI, Theme.Text)
	}
	return clicked
}

// Toggle draws a checkbox with a trailing label and reports a change.
func (c *Context) Toggle(id string, r Rect, label string, v *bool) bool {
	hover, _, clicked := c.widgetState(id, r)
	box := Rect{r.X, r.Y + (r.H-14*c.Scale)/2, 14 * c.Scale, 14 * c.Scale}
	if c.Pass == PassDraw {
		col := Theme.Widget
		if hover {
			col = Theme.WidgetHot
		}
		c.Fill(box, col)
		if *v {
			c.Fill(box.Inset(3*c.Scale), Theme.Accent)
		}
		c.Text(box.X+box.W+8*c.Scale, r.Y+(r.H-LineH)/2+1, label, FaceUI, Theme.Text)
	}
	if clicked {
		*v = !*v
		return true
	}
	return false
}

// Slider is a horizontal track with a filled portion. When logScale is set
// the value moves geometrically, which is the only usable mapping for
// iteration counts and bailout radii that span several decades.
//
// fmtFn renders the value for the inline readout; pass nil for none.
func (c *Context) Slider(id string, r Rect, v *float64, min, max float64, logScale bool, label string, fmtFn func(float64) string) bool {
	hover, held, _ := c.widgetState(id, r)
	changed := false

	toT := func(val float64) float64 {
		if logScale {
			if val < min {
				val = min
			}
			return (math.Log(val) - math.Log(min)) / (math.Log(max) - math.Log(min))
		}
		return (val - min) / (max - min)
	}
	fromT := func(t float64) float64 {
		t = clamp01(t)
		if logScale {
			return math.Exp(math.Log(min) + t*(math.Log(max)-math.Log(min)))
		}
		return min + t*(max-min)
	}

	track := Rect{r.X, r.Y + r.H - 8*c.Scale, r.W, 6 * c.Scale}
	if c.Pass == PassInput && held {
		nv := fromT((c.MX - track.X) / track.W)
		if nv != *v {
			*v = nv
			changed = true
		}
	}
	if c.Pass == PassDraw {
		if label != "" {
			c.Text(r.X, r.Y, label, FaceSmall, Theme.TextDim)
			if fmtFn != nil {
				c.TextRight(r.X+r.W, r.Y, fmtFn(*v), FaceSmall, Theme.Text)
			}
		}
		c.Fill(track, Theme.Track)
		t := clamp01(toT(*v))
		c.Fill(Rect{track.X, track.Y, track.W * t, track.H}, Theme.Accent)
		knob := Rect{track.X + track.W*t - 5*c.Scale, track.Y - 4*c.Scale, 10 * c.Scale, 14 * c.Scale}
		col := Theme.Text
		if hover || held {
			col = Theme.Accent
		}
		c.Fill(knob, col)
	}
	return changed
}

// Choice cycles through a list of options. Clicking the body (or the arrows)
// advances or retreats the selection; it is a combo box without the popup.
//
// As with Slider, the label occupies the top of r and the control the bottom,
// so a row never draws outside the rectangle it was given.
func (c *Context) Choice(id string, r Rect, label string, idx *int, options []string) bool {
	if len(options) == 0 {
		return false
	}
	labelH := 0.0
	if label != "" {
		labelH = LineH + 2*c.Scale
	}
	box := Rect{r.X, r.Y + labelH, r.W, r.H - labelH}
	arrowW := 22 * c.Scale
	left := Rect{box.X, box.Y, arrowW, box.H}
	right := Rect{box.X + box.W - arrowW, box.Y, arrowW, box.H}
	body := Rect{box.X + arrowW, box.Y, box.W - 2*arrowW, box.H}

	changed := false
	_, _, cl := c.widgetState(id+"<", left)
	_, _, cb := c.widgetState(id+"=", body)
	_, _, cr := c.widgetState(id+">", right)
	if c.Pass == PassInput {
		switch {
		case cl:
			*idx = (*idx - 1 + len(options)) % len(options)
			changed = true
		case cb, cr:
			*idx = (*idx + 1) % len(options)
			changed = true
		}
	}
	if c.Pass == PassDraw {
		if label != "" {
			c.Text(r.X, r.Y, label, FaceSmall, Theme.TextDim)
		}
		hoverCol := func(rr Rect) color.RGBA {
			if rr.Contains(c.MX, c.MY) {
				return Theme.WidgetHot
			}
			return Theme.Widget
		}
		c.Fill(left, hoverCol(left))
		c.Fill(right, hoverCol(right))
		c.Fill(body, hoverCol(body))
		ty := box.Y + (box.H-LineH)/2 + 1
		c.Text(left.X+(left.W-TextWidth("<", FaceUI))/2, ty, "<", FaceUI, Theme.TextDim)
		c.Text(right.X+(right.W-TextWidth(">", FaceUI))/2, ty, ">", FaceUI, Theme.TextDim)
		name := options[*idx%len(options)]
		c.Text(body.X+(body.W-TextWidth(name, FaceUI))/2, ty, name, FaceUI, Theme.Text)
	}
	return changed
}

// Header is a section title that doubles as a disclosure control. It returns
// whether the section's body should be built, so callers can wrap a whole
// section in a single if.
//
// Collapsing matters here because the panel has more controls than fit a
// laptop screen at once, and which ones you care about depends on what you are
// doing.
func (c *Context) Header(id string, r Rect, label string, open *bool) bool {
	_, _, clicked := c.widgetState(id, r)
	if c.Pass == PassInput && clicked {
		*open = !*open
	}
	if c.Pass == PassDraw {
		col := Theme.Accent
		if r.Contains(c.MX, c.MY) {
			col = Theme.Text
		}
		s := c.Scale
		c.disclosure(r.X+1*s, r.Y+4*s, 7*s, *open, col)
		c.Text(r.X+14*s, r.Y, label, FaceSmall, col)
		c.Fill(Rect{r.X, r.Y + r.H - 1, r.W, 1}, Theme.PanelEdge)
	}
	return *open
}

// disclosure draws the section triangle: pointing down when open, right when
// closed. It is drawn rather than typed because the Go fonts have no
// guaranteed glyph for a solid triangle.
func (c *Context) disclosure(x, y, size float64, open bool, col color.Color) {
	if c.Pass != PassDraw {
		return
	}
	var p vector.Path
	if open {
		p.MoveTo(float32(x), float32(y))
		p.LineTo(float32(x+size), float32(y))
		p.LineTo(float32(x+size/2), float32(y+size*0.85))
	} else {
		p.MoveTo(float32(x), float32(y))
		p.LineTo(float32(x), float32(y+size))
		p.LineTo(float32(x+size*0.85), float32(y+size/2))
	}
	p.Close()
	var cs ebiten.ColorScale
	cs.ScaleWithColor(col)
	vector.FillPath(c.dst, &p, nil, &DrawPathOptions{AntiAlias: true, ColorScale: cs})
}

// DrawPathOptions is re-exported so caret can name the option struct without
// every caller importing the vector package.
type DrawPathOptions = vector.DrawPathOptions

// Swatch draws a horizontal preview strip of a colour ramp.
func (c *Context) Swatch(r Rect, at func(t float64) color.RGBA) {
	if c.Pass != PassDraw {
		return
	}
	n := int(r.W)
	if n < 1 {
		return
	}
	for i := 0; i < n; i++ {
		x := r.X + float64(i)
		c.Fill(Rect{x, r.Y, 1.5, r.H}, at(float64(i)/float64(n)))
	}
}

// repeatKey reports a key press, and keeps reporting it while the key is held
// so that holding backspace deletes more than one character.
func repeatKey(k ebiten.Key) bool {
	if inpututil.IsKeyJustPressed(k) {
		return true
	}
	d := inpututil.KeyPressDuration(k)
	return d > 30 && d%3 == 0
}

// TextField is a single-line editor.
//
// It is the one widget here that needs keyboard focus, which is why the context
// tracks it: everything else in this package is driven entirely by the pointer.
// It returns true when the contents changed.
func (c *Context) TextField(id string, r Rect, label string, val *string, invalid bool) bool {
	labelH := 0.0
	if label != "" {
		labelH = LineH + 2*c.Scale
	}
	box := Rect{r.X, r.Y + labelH, r.W, r.H - labelH}
	hover := box.Contains(c.MX, c.MY)
	changed := false

	if c.Pass == PassInput {
		if c.pressed {
			if hover {
				c.focus = id
				c.caret = len([]rune(*val))
			} else if c.focus == id {
				c.focus = ""
			}
		}
		if c.focus == id {
			rs := []rune(*val)
			c.caret = min(max(c.caret, 0), len(rs))

			c.chars = ebiten.AppendInputChars(c.chars[:0])
			for _, ch := range c.chars {
				// Control characters would be invisible in the field and
				// meaningless to the parser.
				if ch >= 0x20 && ch != 0x7f {
					rs = slices.Insert(rs, c.caret, ch)
					c.caret++
					changed = true
				}
			}
			if repeatKey(ebiten.KeyBackspace) && c.caret > 0 {
				rs = slices.Delete(rs, c.caret-1, c.caret)
				c.caret--
				changed = true
			}
			if repeatKey(ebiten.KeyDelete) && c.caret < len(rs) {
				rs = slices.Delete(rs, c.caret, c.caret+1)
				changed = true
			}
			if repeatKey(ebiten.KeyArrowLeft) {
				c.caret = max(0, c.caret-1)
			}
			if repeatKey(ebiten.KeyArrowRight) {
				c.caret = min(len(rs), c.caret+1)
			}
			if inpututil.IsKeyJustPressed(ebiten.KeyHome) {
				c.caret = 0
			}
			if inpututil.IsKeyJustPressed(ebiten.KeyEnd) {
				c.caret = len(rs)
			}
			if inpututil.IsKeyJustPressed(ebiten.KeyEscape) ||
				inpututil.IsKeyJustPressed(ebiten.KeyEnter) ||
				inpututil.IsKeyJustPressed(ebiten.KeyKPEnter) {
				c.focus = ""
			}
			if changed {
				*val = string(rs)
			}
		}
	}

	if c.Pass == PassDraw {
		if label != "" {
			col := Theme.TextDim
			if invalid {
				col = Theme.Bad
			}
			c.Text(r.X, r.Y, label, FaceSmall, col)
		}
		bg := Theme.Track
		if c.focus == id {
			bg = Theme.Widget
		}
		c.Fill(box, bg)
		edge := Theme.PanelEdge
		switch {
		case invalid:
			edge = Theme.Bad
		case c.focus == id:
			edge = Theme.Accent
		case hover:
			edge = Theme.TextDim
		}
		c.Stroke(box, 1, edge)

		pad := 5 * c.Scale
		ty := box.Y + (box.H-LineH)/2 + 1
		rs := []rune(*val)
		caret := min(max(c.caret, 0), len(rs))
		// Scroll so the caret stays visible in a field narrower than the text.
		avail := box.W - 2*pad
		start := 0
		for TextWidth(string(rs[start:caret]), FaceMono) > avail && start < caret {
			start++
		}
		shown := string(rs[start:])
		for TextWidth(shown, FaceMono) > avail && len(shown) > 0 {
			shown = shown[:len(shown)-1]
		}
		c.Text(box.X+pad, ty, shown, FaceMono, Theme.Text)
		if c.focus == id {
			cx := box.X + pad + TextWidth(string(rs[start:caret]), FaceMono)
			c.Fill(Rect{cx, box.Y + 3*c.Scale, max(1, c.Scale), box.H - 6*c.Scale}, Theme.Accent)
		}
	}
	return changed
}

// Ticks draws a tick under a strip for each fraction in pos, highlighting the
// one at index sel. It marks where a gradient's control points sit.
func (c *Context) Ticks(r Rect, pos []float64, sel int) {
	if c.Pass != PassDraw {
		return
	}
	for i, p := range pos {
		col := Theme.TextDim
		w := 1.0 * c.Scale
		if i == sel {
			col = Theme.Text
			w = 2 * c.Scale
		}
		c.Fill(Rect{r.X + p*r.W - w/2, r.Y, w, r.H}, col)
	}
}

// --- layout -----------------------------------------------------------

// rowGap is the vertical space Row leaves between widgets, unscaled.
const rowGap = 5

// Column stacks widgets vertically inside a fixed width.
type Column struct {
	X, Y, W float64
	Scale   float64
}

// Row reserves a rectangle of the given height (in unscaled units) and
// advances the cursor past it plus a small gap.
func (col *Column) Row(h float64) Rect {
	r := Rect{col.X, col.Y, col.W, h * col.Scale}
	col.Y += r.H + rowGap*col.Scale
	return r
}

// Gap advances the cursor without reserving a widget.
func (col *Column) Gap(h float64) { col.Y += h * col.Scale }

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
