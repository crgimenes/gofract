package app

import (
	"fmt"
	"image/color"
	"math"
	"strings"

	"github.com/hajimehoshi/ebiten/v2/text/v2"

	"github.com/vmaciel/gofract/internal/fractal"
	"github.com/vmaciel/gofract/internal/palette"
	"github.com/vmaciel/gofract/internal/params"
	"github.com/vmaciel/gofract/internal/ui"
)

// Panel metrics, in unscaled logical pixels.
const (
	panelWidth = 268.0

	rowChoice = 40 // label plus the arrows-and-body control
	rowSlider = 28 // label/readout line plus the track
	rowButton = 26
	rowToggle = 20
	rowSwatch = 14
	rowHead   = 22
	rowField  = 42 // label line plus the edit box
)

// Section titles, which double as the keys of App.open.
const (
	sectFormula = "FORMULA"
	sectCustom  = "CUSTOM FORMULA"
	sectColor   = "COLOR"
	sectPalette = "PALETTE EDITOR"
	sectQuality = "QUALITY"
	sect3D      = "3D VIEW"
)

// buildPanel declares the options panel. It runs twice per frame: once to
// resolve interaction and once to paint, so it must not depend on which pass
// it is in beyond what the widgets already handle.
func (a *App) buildPanel() {
	c := a.ui
	s := a.scale
	pw := panelWidth * s
	panel := ui.Rect{X: float64(a.w) - pw, Y: 0, W: pw, H: float64(a.h)}
	c.Capture(panel)
	c.Fill(panel, ui.Theme.Panel)
	c.Fill(ui.Rect{X: panel.X, Y: 0, W: 1, H: panel.H}, ui.Theme.PanelEdge)

	pad := 14 * s
	col := &ui.Column{X: panel.X + pad, Y: pad - a.panelScroll, W: pw - 2*pad, Scale: s}
	top := col.Y

	c.Text(col.X, col.Y, "GoFract", ui.FaceBold, ui.Theme.Text)
	c.Text(col.X+ui.TextWidth("GoFract ", ui.FaceBold), col.Y+2*s,
		"fractal explorer", ui.FaceSmall, ui.Theme.TextDim)
	col.Gap(18)

	a.sectionFormula(c, col)
	a.sectionCustom(c, col)
	a.sectionColor(c, col)
	a.sectionPalette(c, col)
	a.sectionQuality(c, col)
	a.section3D(c, col)
	a.sectionActions(c, col)
	a.sectionKeys(c, col)

	// Record the laid-out height so scrolling knows where to stop, and clamp
	// straight away in case a section just collapsed underneath the scroll.
	a.panelContent = col.Y - top + pad
	if limit := math.Max(0, a.panelContent-float64(a.h)); a.panelScroll > limit {
		a.panelScroll = limit
	}
	a.drawScrollbar(c, panel)
}

// drawScrollbar hints that there is more panel than fits, and where you are.
func (a *App) drawScrollbar(c *ui.Context, panel ui.Rect) {
	over := a.panelContent - panel.H
	if over <= 0 {
		return
	}
	s := a.scale
	frac := panel.H / a.panelContent
	h := panel.H * frac
	y := (panel.H - h) * (a.panelScroll / over)
	c.Fill(ui.Rect{X: panel.X + panel.W - 3*s, Y: y, W: 3 * s, H: h}, ui.Theme.PanelEdge)
}

// head draws a collapsible section header and reports whether to build the body.
func (a *App) head(c *ui.Context, col *ui.Column, title string) bool {
	open := a.open[title]
	r := col.Row(rowHead)
	shown := c.Header(title, r, title, &open)
	a.open[title] = open
	return shown
}

func (a *App) sectionFormula(c *ui.Context, col *ui.Column) {
	if !a.head(c, col, sectFormula) {
		return
	}
	all := fractal.All()
	names := make([]string, len(all))
	idx := 0
	for i, f := range all {
		names[i] = f.Name()
		if f.Name() == a.st.Type {
			idx = i
		}
	}
	if c.Choice("type", col.Row(rowChoice), "Type", &idx, names) {
		a.setType(all[idx])
	}

	iter := float64(a.st.MaxIter)
	if c.Slider("iter", col.Row(rowSlider), &iter, 16, 1_000_000, true, "Max iterations", fmtInt) {
		a.st.MaxIter = int(math.Round(iter))
		a.needRender = true
	}

	bail := a.st.Bailout
	if c.Slider("bail", col.Row(rowSlider), &bail, 2, 1e6, true, "Bailout radius", fmtSig) {
		a.st.Bailout = bail
		a.needRender = true
	}

	// Type-specific parameters, described by the fractal itself.
	if f := a.st.Fractal(); f != nil {
		for _, p := range f.Params() {
			if p.Index < 0 || p.Index >= len(a.st.P) {
				continue
			}
			v := a.st.P[p.Index]
			id := fmt.Sprintf("p%d", p.Index)
			format := fmtFine
			if p.Int {
				format = fmtInt
			}
			if c.Slider(id, col.Row(rowSlider), &v, p.Min, p.Max, false, p.Name, format) {
				if p.Int {
					v = math.Round(v)
				}
				if v != a.st.P[p.Index] {
					a.st.P[p.Index] = v
					a.needRender = true
				}
			}
		}
	}
	col.Gap(6)
}

// sectionCustom edits the two expressions behind the Custom type. It only
// appears when that type is selected: for everything else there is nothing to
// type.
func (a *App) sectionCustom(c *ui.Context, col *ui.Column) {
	if a.st.Type != fractal.CustomName {
		return
	}
	if !a.head(c, col, sectCustom) {
		return
	}
	err := a.st.FormulaError()
	bad := err != nil

	if c.TextField("f.init", col.Row(rowField), "z0  =", &a.st.Formula.Init, bad) {
		a.formulaEdited()
	}
	if c.TextField("f.iter", col.Row(rowField), "z  ->", &a.st.Formula.Iter, bad) {
		a.formulaEdited()
	}

	s := a.scale
	if bad {
		// Wrap the message rather than let it run off the panel.
		for _, line := range wrapText(err.Error(), col.W, ui.FaceSmall) {
			c.Text(col.X, col.Y, line, ui.FaceSmall, ui.Theme.Bad)
			col.Gap(13)
		}
		col.Gap(4)
	} else {
		c.Text(col.X, col.Y, "vars: z c pixel p1 p2 n i pi e", ui.FaceSmall, ui.Theme.TextDim)
		col.Gap(13)
		c.Text(col.X, col.Y, "abs is component-wise; cabs is modulus", ui.FaceSmall, ui.Theme.TextDim)
		col.Gap(17)
	}

	half := (col.W - 8*s) / 2
	for i := 0; i < len(formulaPresets); i += 2 {
		r := col.Row(rowButton)
		for j := range 2 {
			if i+j >= len(formulaPresets) {
				break
			}
			pr := formulaPresets[i+j]
			box := ui.Rect{X: r.X + float64(j)*(half+8*s), Y: r.Y, W: half, H: r.H}
			if c.Button("fp"+pr.name, box, pr.name) {
				a.hist.Push(a.st)
				a.st.Formula = params.Formula{Init: pr.init, Iter: pr.iter}
				a.formulaEdited()
				a.hist.Push(a.st)
			}
		}
	}
	col.Gap(6)
}

// formulaPresets seed the editor with formulas worth starting from, including
// ones that reproduce built-in types so the notation can be checked against a
// known picture.
var formulaPresets = []struct{ name, init, iter string }{
	{"Mandelbrot", "0", "z*z + c"},
	{"Julia", "c", "z*z + p1"},
	{"Burning Ship", "0", "sqr(abs(z)) + c"},
	{"Tricorn", "0", "sqr(conj(z)) + c"},
	{"Cubic", "0", "z^3 + c"},
	{"Magnet", "0", "sqr((z*z + c - 1) / (2*z + c - 2))"},
	{"Sine", "c", "sin(z) * p1"},
	{"Exp", "0", "exp(z) + c"},
}

// wrapText breaks a message into lines that fit the given width.
func wrapText(s string, width float64, face *text.GoTextFace) []string {
	words := strings.Fields(s)
	var lines []string
	cur := ""
	for _, w := range words {
		try := w
		if cur != "" {
			try = cur + " " + w
		}
		if ui.TextWidth(try, face) > width && cur != "" {
			lines = append(lines, cur)
			cur = w
		} else {
			cur = try
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

func (a *App) sectionColor(c *ui.Context, col *ui.Column) {
	if !a.head(c, col, sectColor) {
		return
	}
	f := a.st.Fractal()

	// Colouring mode. Distance estimation needs a derivative that non-analytic
	// formulas cannot supply, so it is left out of the menu for those rather
	// than silently doing something else.
	var modes []params.ColorMode
	var labels []string
	for i, m := range params.ColorModes {
		if m == params.ColorDistance && (f == nil || !f.HasDeriv()) {
			continue
		}
		modes = append(modes, m)
		labels = append(labels, params.ColorModeLabels[i])
	}
	mIdx := 0
	for i, m := range modes {
		if m == a.st.Color.Mode {
			mIdx = i
		}
	}
	if c.Choice("mode", col.Row(rowChoice), "Coloring", &mIdx, labels) {
		a.st.Color.Mode = modes[mIdx]
		// A different mode needs different data out of the iteration loop.
		a.needRender = true
	}

	// The trap shape only matters in orbit-trap mode.
	if a.st.Color.Mode == params.ColorTrap {
		tIdx := a.st.Color.Trap
		if c.Choice("trap", col.Row(rowChoice), "Trap shape", &tIdx, fractal.TrapNames) {
			a.st.Color.Trap = tIdx
			a.needRender = true
		}
	}

	names := make([]string, 0, len(palette.Presets)+1)
	idx := 0
	for i, p := range palette.Presets {
		names = append(names, p.Name)
		if p.Name == a.st.Color.Palette {
			idx = i
		}
	}
	if len(a.st.Color.Stops) > 0 {
		names = append(names, params.CustomPalette)
		if a.st.Color.Palette == params.CustomPalette {
			idx = len(names) - 1
		}
	}
	if c.Choice("pal", col.Row(rowChoice), "Palette", &idx, names) {
		a.st.Color.Palette = names[idx]
		a.needColor = true
	}

	// A live preview of the ramp as it is actually applied, rotation included.
	ramp := a.st.Color.Ramp()
	off := a.st.Color.Offset
	c.Swatch(col.Row(rowSwatch), func(t float64) color.RGBA { return ramp.At(t + off) })

	dens := a.st.Color.Density
	if c.Slider("dens", col.Row(rowSlider), &dens, 0.02, 64, true, "Color density", fmtSig) {
		a.st.Color.Density = dens
		a.needColor = true
	}
	if c.Slider("off", col.Row(rowSlider), &a.st.Color.Offset, 0, 1, false, "Color rotation", fmtPct) {
		a.needColor = true
	}
	if c.Toggle("cycle", col.Row(rowToggle), "Cycle colors", &a.st.Color.Cycle) {
		a.needColor = true
	}
	if a.st.Color.Cycle {
		c.Slider("cspd", col.Row(rowSlider), &a.st.Color.CycleSpeed, 0.01, 3, true, "Cycle speed", fmtSpeed)
	}
	if c.Toggle("logmap", col.Row(rowToggle), "Logarithmic mapping", &a.st.Color.LogMap) {
		a.needColor = true
	}
	if c.Toggle("light", col.Row(rowToggle), "Relief lighting", &a.st.Color.Light) {
		a.needColor = true
	}
	if a.st.Color.Light {
		if c.Slider("laz", col.Row(rowSlider), &a.st.Color.LightAz, 0, 360, false, "Light direction", fmtDeg) {
			a.needColor = true
		}
		if c.Slider("lel", col.Row(rowSlider), &a.st.Color.LightEl, 5, 89, false, "Light elevation", fmtDeg) {
			a.needColor = true
		}
		if c.Slider("ldp", col.Row(rowSlider), &a.st.Color.LightDepth, 0.05, 20, true, "Relief depth", fmtSig) {
			a.needColor = true
		}
		if c.Slider("lam", col.Row(rowSlider), &a.st.Color.LightAmb, 0, 1, false, "Ambient", fmtPct) {
			a.needColor = true
		}
	}
	col.Gap(6)
}

// sectionPalette is the stop editor for a custom ramp.
func (a *App) sectionPalette(c *ui.Context, col *ui.Column) {
	if !a.head(c, col, sectPalette) {
		return
	}
	s := a.scale
	half := (col.W - 8*s) / 2

	if len(a.st.Color.Stops) == 0 {
		c.Text(col.X, col.Y, "Copy a preset to start editing,", ui.FaceSmall, ui.Theme.TextDim)
		c.Text(col.X, col.Y+13*s, "or drop a Fractint .map file.", ui.FaceSmall, ui.Theme.TextDim)
		col.Gap(32)
		if c.Button("palcopy", col.Row(rowButton), "Copy current preset") {
			a.forkPalette()
		}
		col.Gap(6)
		return
	}

	stops := a.st.Color.Stops
	a.stopSel = min(max(a.stopSel, 0), len(stops)-1)

	// The ramp with a tick under each control point, so the selection is
	// visible against the gradient it belongs to.
	custom := palette.New("", palette.RGBA(0, 0, 0), stops)
	c.Swatch(col.Row(rowSwatch), func(t float64) color.RGBA { return custom.At(t) })
	pos := make([]float64, len(stops))
	for i, st := range stops {
		pos[i] = st.Pos
	}
	tickRow := col.Row(5)
	c.Ticks(tickRow, pos, a.stopSel)

	labels := make([]string, len(stops))
	for i := range stops {
		labels[i] = fmt.Sprintf("stop %d / %d", i+1, len(stops))
	}
	c.Choice("stopsel", col.Row(rowChoice), "", &a.stopSel, labels)

	st := &a.st.Color.Stops[a.stopSel]
	if c.Slider("stoppos", col.Row(rowSlider), &st.Pos, 0, 1, false, "Position", fmtPct) {
		a.sortStops()
		a.needColor = true
	}
	if a.colorSlider(c, col, "stopr", "Red", &st.R) ||
		a.colorSlider(c, col, "stopg", "Green", &st.G) ||
		a.colorSlider(c, col, "stopb", "Blue", &st.B) {
		a.needColor = true
	}

	r := col.Row(rowButton)
	if c.Button("stopadd", ui.Rect{X: r.X, Y: r.Y, W: half, H: r.H}, "Add stop") {
		a.addStop()
	}
	if c.Button("stopdel", ui.Rect{X: r.X + half + 8*s, Y: r.Y, W: half, H: r.H}, "Delete stop") {
		a.deleteStop()
	}
	r = col.Row(rowButton)
	if c.Button("palcopy2", ui.Rect{X: r.X, Y: r.Y, W: half, H: r.H}, "From preset") {
		a.forkPalette()
	}
	if c.Button("palrev", ui.Rect{X: r.X + half + 8*s, Y: r.Y, W: half, H: r.H}, "Reverse") {
		a.reversePalette()
	}
	col.Gap(6)
}

// colorSlider edits one 8-bit channel in place.
func (a *App) colorSlider(c *ui.Context, col *ui.Column, id, label string, v *uint8) bool {
	f := float64(*v)
	if c.Slider(id, col.Row(rowSlider), &f, 0, 255, false, label, fmtInt) {
		if n := uint8(math.Round(f)); n != *v {
			*v = n
			return true
		}
	}
	return false
}

func (a *App) sectionQuality(c *ui.Context, col *ui.Column) {
	if !a.head(c, col, sectQuality) {
		return
	}
	ssIdx := a.st.Super - 1
	if c.Choice("ss", col.Row(rowChoice), "Supersampling", &ssIdx, []string{"1x (off)", "2x2", "3x3"}) {
		a.st.Super = ssIdx + 1
		a.needRender = true
	}

	guessing := a.st.Guess == params.GuessOn
	if c.Toggle("guess", col.Row(rowToggle), "Solid guessing", &guessing) {
		a.st.Guess = params.GuessOff
		if guessing {
			a.st.Guess = params.GuessOn
		}
		a.needRender = true
	}

	mulIdx := exportIndex(a.exportMul())
	if c.Choice("exp", col.Row(rowChoice), "PNG export size", &mulIdx, exportLabels) {
		a.setExportMul(exportMuls[mulIdx])
	}
	col.Gap(6)
}

// section3D turns the flat fractal into a landscape or a globe: Fractint's own
// "3D transform" and "planet" modes, revived as a live view rather than a
// one-shot render.
func (a *App) section3D(c *ui.Context, col *ui.Column) {
	if !a.head(c, col, sect3D) {
		return
	}
	modeIdx := int(a.mode3D)
	if c.Choice("m3d", col.Row(rowChoice), "View", &modeIdx, mode3DLabels) {
		a.mode3D = mode3D(modeIdx)
		a.clamp3D()
		a.setStatus("3D view: %s", a.mode3D)
	}

	if a.mode3D == mode3DOff {
		c.Text(col.X, col.Y, "Landscape reads the picture as a", ui.FaceSmall, ui.Theme.TextDim)
		col.Gap(13)
		c.Text(col.X, col.Y, "height field; Globe wraps it onto a sphere.", ui.FaceSmall, ui.Theme.TextDim)
		col.Gap(17)
		col.Gap(6)
		return
	}

	// The sliders bind directly to a.cam's fields and mutate them in place;
	// only Pitch needs anything done after the fact (keeping the camera the
	// right side up — see clampPitch).
	c.Slider("c.yaw", col.Row(rowSlider), &a.cam.Yaw, -180, 180, false, "Yaw", fmtDeg)

	pitchMin, pitchMax := 8.0, 85.0
	if a.mode3D == mode3DGlobe {
		pitchMin, pitchMax = -85, 85
	}
	if c.Slider("c.pitch", col.Row(rowSlider), &a.cam.Pitch, pitchMin, pitchMax, false, "Pitch", fmtDeg) {
		a.cam.Pitch = clampPitch(a.cam.Pitch, a.mode3D)
	}
	c.Slider("c.dist", col.Row(rowSlider), &a.cam.Distance, 1.3, 12, true, "Distance", fmtSig)
	c.Slider("c.fov", col.Row(rowSlider), &a.cam.FOV, 15, 90, false, "Field of view", fmtDeg)

	depthMax := 3.0
	if a.mode3D == mode3DGlobe {
		depthMax = 0.6 // a globe's radius is 1; more than this and terrain pokes through its own far side
	}
	c.Slider("c.height", col.Row(rowSlider), &a.cam.HeightScale, 0.02, depthMax, true, "Height scale", fmtSig)
	if a.st.Color.Light {
		c.Text(col.X, col.Y, "Uses the Color section's light position.", ui.FaceSmall, ui.Theme.TextDim)
	} else {
		c.Text(col.X, col.Y, "Turn on Relief lighting (Color) to move the sun.", ui.FaceSmall, ui.Theme.TextDim)
	}
	col.Gap(17)

	if c.Button("c.reset", col.Row(rowButton), "Reset camera") {
		a.cam = defaultCam3D()
	}
	col.Gap(6)
}

// mode3DLabels parallel mode3D's values in order (Off, Landscape, Globe).
var mode3DLabels = []string{"off (2D)", "landscape", "globe"}

func (a *App) sectionActions(c *ui.Context, col *ui.Column) {
	s := a.scale
	half := (col.W - 8*s) / 2

	r := col.Row(rowButton)
	if c.Button("png", ui.Rect{X: r.X, Y: r.Y, W: half, H: r.H}, "Save PNG") {
		a.exportPNG()
	}
	if c.Button("home", ui.Rect{X: r.X + half + 8*s, Y: r.Y, W: half, H: r.H}, "Reset view") {
		a.hist.Push(a.st)
		a.st.View = params.HomeView(a.st.Fractal(), a.w, a.h)
		a.hist.Push(a.st)
		a.needRender = true
	}

	r = col.Row(rowButton)
	if c.Button("sp", ui.Rect{X: r.X, Y: r.Y, W: half, H: r.H}, "Save params") {
		a.saveParams()
	}
	if c.Button("lp", ui.Rect{X: r.X + half + 8*s, Y: r.Y, W: half, H: r.H}, "Load params") {
		a.loadParams()
	}

	r = col.Row(rowButton)
	if c.Button("back", ui.Rect{X: r.X, Y: r.Y, W: half, H: r.H}, "< Back") {
		a.historyGo(a.hist.Back())
	}
	if c.Button("fwd", ui.Rect{X: r.X + half + 8*s, Y: r.Y, W: half, H: r.H}, "Forward >") {
		a.historyGo(a.hist.Forward())
	}
	col.Gap(4)
}

// sectionKeys lists the shortcuts at the end of the panel's flow. It used to be
// pinned to the bottom edge, which meant dropping lines whenever the controls
// above grew; now that the panel scrolls it can simply be the last thing.
func (a *App) sectionKeys(c *ui.Context, col *ui.Column) {
	lines := []string{
		"drag zoom box    click zoom 2x",
		"wheel zoom    middle-drag pan",
		"right-click / J   Mandel<->Julia",
		"T type   +/- iters   [ ] bailout",
		"C palette   , . rotate   A cycle",
		"V coloring mode   O trap shape",
		"3 relief lighting   4 landscape/globe",
		"G guessing   R supersample",
		"H home   S png   Backspace back",
		"drop a .map or .json on the window",
	}
	col.Gap(8)
	c.Fill(ui.Rect{X: col.X, Y: col.Y, W: col.W, H: 1}, ui.Theme.PanelEdge)
	col.Gap(8)
	for _, l := range lines {
		c.Text(col.X, col.Y, l, ui.FaceSmall, ui.Theme.TextDim)
		col.Gap(14)
	}
}

// drawHUD paints the status readout: where we are, how deep, how expensive.
func (a *App) drawHUD() {
	c := a.ui
	s := a.scale
	v := a.st.View
	hi, hn := a.hist.Pos()

	status := "rendering"
	if a.lastDone {
		status = "done"
	} else if a.lastProg > 0 {
		status = fmt.Sprintf("pass %.0f%%", a.lastProg*100)
	}
	render := fmt.Sprintf("%.0f ms  (%s)", float64(a.lastElapsed.Microseconds())/1000, status)
	if a.lastDone && a.lastGuessed > 0.005 {
		render += fmt.Sprintf("  −%.0f%% guessed", a.lastGuessed*100)
	}

	mode := string(a.st.Color.EffectiveMode(a.st.Fractal()))
	if a.st.Color.Mode == params.ColorTrap {
		mode += " / " + fractal.TrapNames[a.st.Color.Trap]
	}

	// How many digits of the centre are actually meaningful at this zoom: one
	// per factor of ten of magnification, plus a couple to see them move.
	digits := max(8, int(math.Log10(1/v.Scale))+3)

	precision := "float64"
	if v.Deep() {
		precision = fmt.Sprintf("%d bits", v.Prec())
		if a.lastPerturbed {
			precision += ", perturbation"
		} else {
			precision += " — TOO DEEP FOR " + a.st.Type
		}
	}

	lines := [][2]string{
		{"re", v.CX.Short(digits)},
		{"im", v.CY.Short(digits)},
		{"width", fmt.Sprintf("%.6g", v.Width(a.w))},
		{"magnif.", fmtMag(v.Magnification(a.w))},
		{"precision", precision},
		{"iters", fmt.Sprintf("%d   bailout %g", a.st.MaxIter, a.st.Bailout)},
		{"color", mode},
		{"render", render},
		{"history", fmt.Sprintf("%d / %d", hi, hn)},
	}
	if f := a.st.Fractal(); f != nil {
		if ps := f.Params(); len(ps) >= 2 {
			lines = append(lines, [2]string{"params", fmt.Sprintf("%.10g, %.10g", a.st.P[0], a.st.P[1])})
		}
	}

	labelW := 62 * s
	pad := 10 * s
	lh := 16 * s
	// Deep-zoom coordinates run to dozens of digits, so the box is allowed to
	// grow but not to swallow the window.
	maxW := float64(a.w)*0.55 - 2*pad
	w := ui.TextWidth(a.st.Type, ui.FaceBold)
	for _, l := range lines {
		if x := labelW + ui.TextWidth(l[1], ui.FaceSmall); x > w {
			w = math.Min(x, maxW)
		}
	}
	box := ui.Rect{X: 12 * s, Y: 12 * s, W: w + 2*pad, H: lh*float64(len(lines)+1) + 2*pad}
	c.Fill(box, ui.Theme.Overlay)

	y := box.Y + pad
	c.Text(box.X+pad, y, a.st.Type, ui.FaceBold, ui.Theme.Accent)
	y += lh
	for _, l := range lines {
		c.Text(box.X+pad, y, l[0], ui.FaceSmall, ui.Theme.TextDim)
		c.Text(box.X+pad+labelW, y, l[1], ui.FaceSmall, ui.Theme.Text)
		y += lh
	}

	// Progress bar along the top edge while a render is in flight.
	if !a.lastDone {
		c.Fill(ui.Rect{X: 0, Y: 0, W: float64(a.w) * a.lastProg, H: 2 * s}, ui.Theme.Accent)
	}
}

// --- value formatting -------------------------------------------------

func fmtInt(v float64) string { return fmt.Sprintf("%d", int(math.Round(v))) }

func fmtSig(v float64) string {
	switch {
	case v >= 1000:
		return fmt.Sprintf("%.0f", v)
	case v >= 10:
		return fmt.Sprintf("%.1f", v)
	default:
		return fmt.Sprintf("%.3g", v)
	}
}

func fmtFine(v float64) string { return fmt.Sprintf("%.5f", v) }

func fmtPct(v float64) string { return fmt.Sprintf("%.0f%%", v*100) }

func fmtSpeed(v float64) string { return fmt.Sprintf("%.2f rot/s", v) }

func fmtDeg(v float64) string { return fmt.Sprintf("%.0f deg", v) }

// fmtMag prints the zoom the way Fractint does: plain up to a few thousand,
// then in powers of ten, because deep zooms run to 1e15 with float64.
func fmtMag(m float64) string {
	if m < 10000 {
		return fmt.Sprintf("%.1fx", m)
	}
	return fmt.Sprintf("%.3ex", m)
}
