package app

import (
	"math"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/vmaciel/gofract/internal/fractal"
	"github.com/vmaciel/gofract/internal/palette"
	"github.com/vmaciel/gofract/internal/params"
)

const (
	// A drag shorter than this is a click, not a marquee.
	dragThreshold = 6.0
	// One wheel notch. 1.15 is small enough to feel continuous and large
	// enough that a flick covers real distance.
	wheelZoom = 1.15
	// A gap this long ends a wheel burst, so a burst is one history entry.
	wheelBurstGap = 400 * time.Millisecond
)

func justPressed(b ebiten.MouseButton) bool  { return inpututil.IsMouseButtonJustPressed(b) }
func justReleased(b ebiten.MouseButton) bool { return inpututil.IsMouseButtonJustReleased(b) }

// handleMouse implements the gestures: marquee zoom, click zoom, pan, the
// Mandelbrot/Julia switch and wheel zoom.
func (a *App) handleMouse(mx, my float64) {
	overUI := a.showPanel && (a.ui.Captured || a.ui.Active())

	// --- marquee / click zoom (left button) ---
	if justPressed(ebiten.MouseButtonLeft) && !overUI && !a.panning {
		a.boxing = true
		a.boxX0, a.boxY0 = mx, my
		a.boxX1, a.boxY1 = mx, my
	}
	if a.boxing {
		a.boxX1, a.boxY1 = mx, my
		if justReleased(ebiten.MouseButtonLeft) {
			a.boxing = false
			a.commitBox()
		}
	}

	// --- pan (middle button) ---
	if justPressed(ebiten.MouseButtonMiddle) && !overUI {
		a.panning = true
		a.panX0, a.panY0 = mx, my
		a.panStart = a.st.View
		a.hist.BeginGesture(a.st)
	}
	if a.panning {
		if ebiten.IsMouseButtonPressed(ebiten.MouseButtonMiddle) {
			// Dragging right moves the image right, i.e. the centre left.
			v := a.panStart.Pan(mx-a.panX0, my-a.panY0)
			if !v.Equal(a.st.View) {
				a.st.View = v
				a.hist.Replace(a.st)
				a.needRender = true
			}
		} else {
			a.panning = false
		}
	}

	// --- Mandelbrot <-> Julia switch (right button) ---
	if justPressed(ebiten.MouseButtonRight) && !overUI {
		a.switchCompanion(mx, my)
	}

	// --- wheel ---
	_, wheel := ebiten.Wheel()
	if wheel != 0 && overUI {
		// Over the panel the wheel scrolls the panel instead of zooming.
		a.scrollPanel(wheel)
		return
	}
	if dy := wheel; dy != 0 {
		// A whole burst of notches collapses into one history entry, so
		// stepping back returns to where the gesture started rather than
		// unwinding it one notch at a time.
		if time.Since(a.lastWheel) > wheelBurstGap {
			a.hist.BeginGesture(a.st)
		}
		a.lastWheel = time.Now()
		a.st.View = a.st.View.ZoomAt(mx, my, a.w, a.h, math.Pow(wheelZoom, dy))
		a.hist.Replace(a.st)
		a.needRender = true
	}
}

// commitBox turns a finished marquee (or a bare click) into a new view.
// scrollPanel moves the options panel, clamped to its content.
func (a *App) scrollPanel(notches float64) {
	const lines = 48 // pixels per wheel notch
	a.panelScroll -= notches * lines * a.scale
	limit := math.Max(0, a.panelContent-float64(a.h))
	a.panelScroll = math.Max(0, math.Min(a.panelScroll, limit))
}

func (a *App) commitBox() {
	r := a.boxRect()
	if r.W < dragThreshold || r.H < dragThreshold {
		// A click zooms by 2 about the clicked point.
		a.hist.Push(a.st)
		a.st.View = a.st.View.ZoomAt(a.boxX0, a.boxY0, a.w, a.h, 2)
		a.hist.Push(a.st)
		a.needRender = true
		return
	}
	a.hist.Push(a.st)
	// The marquee's width becomes the window's width.
	a.st.View = a.st.View.Recentre(r.X+r.W/2, r.Y+r.H/2, a.w, a.h,
		a.st.View.Scale*r.W/float64(a.w))
	a.hist.Push(a.st)
	a.needRender = true
}

// switchCompanion jumps between a Mandelbrot-like type and its Julia
// counterpart. Going "down" freezes the clicked point as the Julia constant;
// that point's orbit behaviour in the parameter plane is exactly what shapes
// the resulting set.
func (a *App) switchCompanion(mx, my float64) {
	f := a.st.Fractal()
	if f == nil {
		return
	}
	name := f.Companion()
	if name == "" {
		return
	}
	next, ok := fractal.Lookup(name)
	if !ok {
		return
	}
	a.hist.Push(a.st)

	// The companion's constant is a float64 formula parameter, so the clicked
	// point is taken at float64 precision — which is all a Julia constant is.
	wx, wy := a.st.View.AtFloat(mx, my, a.w, a.h)
	p := fractal.DefaultParams(next)
	if len(next.Params()) >= 2 {
		p[0], p[1] = wx, wy
	}
	a.st.Type = next.Name()
	a.st.P = p
	a.st.View = params.HomeView(next, a.w, a.h)

	a.hist.Push(a.st)
	a.needRender = true
	if len(next.Params()) >= 2 {
		a.setStatus("%s at c = %.6f %+.6fi", next.Name(), wx, wy)
	} else {
		a.setStatus("%s", next.Name())
	}
}

// formulaEdited recompiles after a change to the custom formula. A formula that
// does not parse simply leaves the previous image up; the panel shows why.
func (a *App) formulaEdited() {
	if err := a.st.FormulaError(); err != nil {
		return
	}
	a.needRender = true
}

// handleKeys implements the Fractint-flavoured shortcuts.
func (a *App) handleKeys() error {
	// While a formula is being typed the keyboard belongs to the text field.
	// Otherwise typing "sin" would save a PNG, cycle the palette and toggle the
	// status readout.
	if a.ui.Editing() {
		return nil
	}
	k := inpututil.IsKeyJustPressed
	shift := ebiten.IsKeyPressed(ebiten.KeyShift)
	ctrl := ebiten.IsKeyPressed(ebiten.KeyControl) || ebiten.IsKeyPressed(ebiten.KeyMeta)

	switch {
	case ctrl && k(ebiten.KeyQ):
		return ebiten.Termination
	case ctrl && k(ebiten.KeyS):
		a.saveParams()
		return nil
	case ctrl && k(ebiten.KeyO):
		a.loadParams()
		return nil
	}

	// Type selection.
	if k(ebiten.KeyT) {
		a.cycleType(step(shift))
	}
	if k(ebiten.KeyJ) {
		mx, my := ebiten.CursorPosition()
		a.switchCompanion(float64(mx), float64(my))
	}

	// Iterations: doubling is the only step size that stays useful across
	// the whole range.
	if k(ebiten.KeyEqual) || k(ebiten.KeyKPAdd) {
		a.setIter(a.st.MaxIter * 2)
	}
	if k(ebiten.KeyMinus) || k(ebiten.KeyKPSubtract) {
		a.setIter(a.st.MaxIter / 2)
	}
	if k(ebiten.KeyBracketLeft) {
		a.setBailout(a.st.Bailout / 2)
	}
	if k(ebiten.KeyBracketRight) {
		a.setBailout(a.st.Bailout * 2)
	}

	// Colour.
	if k(ebiten.KeyC) {
		a.cyclePalette(step(shift))
	}
	if k(ebiten.KeyComma) {
		if shift {
			a.setCycleSpeed(a.st.Color.CycleSpeed / 1.5)
		} else {
			a.st.Color.Offset = wrap01(a.st.Color.Offset - 0.02)
			a.needColor = true
		}
	}
	if k(ebiten.KeyPeriod) {
		if shift {
			a.setCycleSpeed(a.st.Color.CycleSpeed * 1.5)
		} else {
			a.st.Color.Offset = wrap01(a.st.Color.Offset + 0.02)
			a.needColor = true
		}
	}
	if k(ebiten.KeyA) {
		a.st.Color.Cycle = !a.st.Color.Cycle
		a.needColor = true
		a.setStatus("Color cycling %s", onOff(a.st.Color.Cycle))
	}
	if k(ebiten.KeyV) {
		a.cycleColorMode(step(shift))
	}
	if k(ebiten.KeyO) {
		a.cycleTrap(step(shift))
	}
	if k(ebiten.KeyG) {
		a.toggleGuess()
	}
	if k(ebiten.Key3) {
		a.st.Color.Light = !a.st.Color.Light
		a.needColor = true
		a.setStatus("Relief lighting %s", onOff(a.st.Color.Light))
	}
	if k(ebiten.Key4) {
		a.cycle3D(step(shift))
	}
	if k(ebiten.KeyL) && !ctrl {
		a.st.Color.LogMap = !a.st.Color.LogMap
		a.needColor = true
		a.setStatus("Logarithmic mapping %s", onOff(a.st.Color.LogMap))
	}

	// View.
	if a.mode3D == mode3DOff {
		if k(ebiten.KeyHome) || k(ebiten.KeyH) {
			a.hist.Push(a.st)
			a.st.View = params.HomeView(a.st.Fractal(), a.w, a.h)
			a.hist.Push(a.st)
			a.needRender = true
		}
	} else if k(ebiten.KeyHome) || k(ebiten.KeyH) {
		a.cam = defaultCam3D()
		a.setStatus("3D camera reset")
	}
	if k(ebiten.KeyBackspace) {
		if shift {
			a.historyGo(a.hist.Forward())
		} else {
			a.historyGo(a.hist.Back())
		}
	}

	if a.mode3D == mode3DOff {
		// Keyboard pan, held down for continuous motion. Like the wheel, the
		// whole gesture is one history entry: pushed when the first arrow goes
		// down, finalised when the last one comes up.
		dx, dy := 0.0, 0.0
		if ebiten.IsKeyPressed(ebiten.KeyArrowLeft) {
			dx -= 1
		}
		if ebiten.IsKeyPressed(ebiten.KeyArrowRight) {
			dx += 1
		}
		if ebiten.IsKeyPressed(ebiten.KeyArrowUp) {
			dy -= 1
		}
		if ebiten.IsKeyPressed(ebiten.KeyArrowDown) {
			dy += 1
		}
		if dx != 0 || dy != 0 {
			if !a.keyPanning {
				a.keyPanning = true
				a.hist.BeginGesture(a.st)
			}
			const panSpeed = 12 // pixels per tick
			step := panSpeed * a.scale
			a.st.View = a.st.View.Pan(-dx*step, -dy*step)
			a.hist.Replace(a.st)
			a.needRender = true
		} else {
			a.keyPanning = false
		}
	} else {
		// Arrow keys orbit the camera when a 3D projection is showing, rather
		// than panning a fractal view the camera is not looking at directly.
		const camStep = 1.5 // degrees per tick
		if ebiten.IsKeyPressed(ebiten.KeyArrowLeft) {
			a.cam.Yaw -= camStep
		}
		if ebiten.IsKeyPressed(ebiten.KeyArrowRight) {
			a.cam.Yaw += camStep
		}
		if ebiten.IsKeyPressed(ebiten.KeyArrowUp) {
			a.cam.Pitch = clampPitch(a.cam.Pitch+camStep, a.mode3D)
		}
		if ebiten.IsKeyPressed(ebiten.KeyArrowDown) {
			a.cam.Pitch = clampPitch(a.cam.Pitch-camStep, a.mode3D)
		}
	}

	// Quality and chrome.
	if k(ebiten.KeyR) {
		a.st.Super = a.st.Super%3 + 1
		a.needRender = true
		a.setStatus("Supersampling %dx%d", a.st.Super, a.st.Super)
	}
	if k(ebiten.KeyTab) || k(ebiten.KeyX) {
		a.showPanel = !a.showPanel
	}
	if k(ebiten.KeyI) {
		a.showHUD = !a.showHUD
	}
	if k(ebiten.KeyF) || k(ebiten.KeyF11) {
		ebiten.SetFullscreen(!ebiten.IsFullscreen())
	}
	if k(ebiten.KeyS) && !ctrl {
		a.exportPNG()
	}
	if k(ebiten.KeyEscape) {
		if a.boxing {
			a.boxing = false
		} else if ebiten.IsFullscreen() {
			ebiten.SetFullscreen(false)
		}
	}
	return nil
}

func step(shift bool) int {
	if shift {
		return -1
	}
	return 1
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func wrap01(v float64) float64 { return v - math.Floor(v) }

func (a *App) setIter(n int) {
	if n < 16 {
		n = 16
	}
	if n > 1_000_000 {
		n = 1_000_000
	}
	if n == a.st.MaxIter {
		return
	}
	a.st.MaxIter = n
	a.needRender = true
	a.setStatus("Max iterations %d", n)
}

func (a *App) setBailout(b float64) {
	b = math.Max(2, math.Min(b, 1e6))
	if b == a.st.Bailout {
		return
	}
	a.st.Bailout = b
	a.needRender = true
	a.setStatus("Bailout %g", b)
}

func (a *App) cycleType(d int) {
	all := fractal.All()
	if len(all) == 0 {
		return
	}
	idx := 0
	for i, f := range all {
		if f.Name() == a.st.Type {
			idx = i
			break
		}
	}
	next := all[((idx+d)%len(all)+len(all))%len(all)]
	a.setType(next)
}

// setType switches formula, adopting whatever presentation that formula asks
// for: coordinates, iteration counts and colour densities that suit one
// formula rarely suit another.
func (a *App) setType(f fractal.Fractal) {
	a.hist.Push(a.st)
	a.st = a.st.ApplyDefaults(f, a.w, a.h)
	a.hist.Push(a.st)
	a.needRender = true
	a.setStatus("%s", f.Name())
}

// cycleColorMode steps through the colouring modes, skipping any the current
// formula cannot support.
func (a *App) cycleColorMode(d int) {
	f := a.st.Fractal()
	modes := make([]params.ColorMode, 0, len(params.ColorModes))
	for _, m := range params.ColorModes {
		if m == params.ColorDistance && (f == nil || !f.HasDeriv()) {
			continue
		}
		modes = append(modes, m)
	}
	idx := 0
	for i, m := range modes {
		if m == a.st.Color.Mode {
			idx = i
		}
	}
	n := len(modes)
	a.st.Color.Mode = modes[((idx+d)%n+n)%n]
	a.needRender = true
	a.setStatus("Coloring: %s", a.st.Color.Mode)
}

func (a *App) cycleTrap(d int) {
	n := len(fractal.TrapNames)
	a.st.Color.Trap = ((a.st.Color.Trap+d)%n + n) % n
	// The trap shape is evaluated inside the iteration loop.
	a.needRender = true
	if a.st.Color.Mode != params.ColorTrap {
		a.setStatus("Trap shape: %s (switch coloring to orbit trap with V)", fractal.TrapNames[a.st.Color.Trap])
		return
	}
	a.setStatus("Trap shape: %s", fractal.TrapNames[a.st.Color.Trap])
}

func (a *App) toggleGuess() {
	if a.st.Guess == params.GuessOn {
		a.st.Guess = params.GuessOff
	} else {
		a.st.Guess = params.GuessOn
	}
	a.needRender = true
	a.setStatus("Solid guessing %s", a.st.Guess)
}

func (a *App) setCycleSpeed(v float64) {
	a.st.Color.CycleSpeed = math.Max(0.01, math.Min(3, v))
	a.setStatus("Cycle speed %.2f rot/s", a.st.Color.CycleSpeed)
}

func (a *App) cyclePalette(d int) {
	idx := -1
	for i, p := range palette.Presets {
		if p.Name == a.st.Color.Palette {
			idx = i
			break
		}
	}
	// Stepping off a custom ramp lands on the first preset.
	if idx < 0 {
		idx = -d
	}
	n := len(palette.Presets)
	p := palette.Presets[((idx+d)%n+n)%n]
	a.st.Color.Palette = p.Name
	a.needColor = true
	a.setStatus("Palette: %s", p.Name)
}

func (a *App) historyGo(st params.State, ok bool) {
	if !ok {
		return
	}
	a.st = st
	a.stopSel = min(a.stopSel, max(0, len(st.Color.Stops)-1))
	a.needRender = true
	i, n := a.hist.Pos()
	a.setStatus("History %d/%d", i, n)
}
