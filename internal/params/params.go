// Package params holds the complete, serialisable description of what is on
// screen: where we are looking, which formula is being iterated, and how the
// result is coloured.
//
// Everything the renderer needs is reachable from a State, and a State
// round-trips through JSON. That is what makes zoom history, save/load and
// off-screen export all fall out of the same type.
package params

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"

	"github.com/vmaciel/gofract/internal/fractal"
	"github.com/vmaciel/gofract/internal/palette"
)

// View is the window onto the complex plane.
//
// Scale is world units per screen pixel, which keeps zooming independent of
// the window size: resizing the window reveals more of the plane instead of
// magnifying what is already there. It stays float64 because it is only ever a
// magnitude — a float64 reaches down to about 10⁻³⁰⁸, so the zoom ceiling from
// this field alone is around 10³⁰⁰.
//
// The centre is arbitrary precision, because it is a *position*: telling two
// neighbouring pixels apart needs roughly log₂(1/Scale) mantissa bits, and
// float64's 53 run out near 10¹⁵ magnification.
type View struct {
	CX    Real    `json:"cx"`
	CY    Real    `json:"cy"`
	Scale float64 `json:"scale"`
}

// NewView builds a view from float64 coordinates.
func NewView(cx, cy, scale float64) View {
	return View{CX: RealFrom(cx), CY: RealFrom(cy), Scale: scale}
}

// float64Limit is the pixel size at which plain float64 sampling starts to
// pixelate. The mantissa has 53 bits; coordinates near the Mandelbrot set are
// order 1; a few bits have to be left over to place a sample *within* a pixel
// for smooth colouring. That puts the practical floor around 10⁻¹³.
const float64Limit = 1e-13

// viewPrec is the mantissa precision a centre needs to resolve one pixel at
// this scale, plus a guard for the arithmetic. At shallow zoom it lands on 64
// bits, so the arbitrary-precision path costs nothing until it is needed.
func viewPrec(scale float64) uint {
	if !(scale > 0) {
		return 64
	}
	bits := -math.Log2(scale) + 48
	return uint(math.Min(math.Max(bits, 64), maxPrec))
}

// Prec is the precision this view's centre should be carried at.
func (v View) Prec() uint { return viewPrec(v.Scale) }

// Deep reports whether the zoom has outrun float64, so that sampling has to go
// through perturbation instead of iterating absolute coordinates.
func (v View) Deep() bool { return v.Scale < float64Limit }

// Offset is the world-space offset of a pixel from the view centre.
//
// It is float64 at every zoom level, and that is the observation the whole
// deep-zoom path rests on: the offset spans at most the width of the image, so
// however small the pixels get, their *distance from the centre* is only ever a
// few thousand pixels' worth. Precision is needed for where the image is, never
// for how big it is.
//
// The imaginary axis points up, as in every printed picture of the set, so the
// y term is negated.
func (v View) Offset(px, py float64, w, h int) (dx, dy float64) {
	return (px - float64(w)/2) * v.Scale, -(py - float64(h)/2) * v.Scale
}

// At is the full-precision coordinate of a pixel.
func (v View) At(px, py float64, w, h int) (Real, Real) {
	dx, dy := v.Offset(px, py, w, h)
	p := v.Prec()
	return v.CX.Add(dx, p), v.CY.Add(dy, p)
}

// AtFloat is the coordinate of a pixel as float64, for display and for the
// float64-valued formula parameters. At deep zoom it is necessarily lossy.
func (v View) AtFloat(px, py float64, w, h int) (x, y float64) {
	dx, dy := v.Offset(px, py, w, h)
	return v.CX.Float() + dx, v.CY.Float() + dy
}

// WorldToScreen is the inverse of AtFloat.
func (v View) WorldToScreen(x, y float64, w, h int) (px, py float64) {
	return float64(w)/2 + (x-v.CX.Float())/v.Scale,
		float64(h)/2 - (y-v.CY.Float())/v.Scale
}

// Pan moves the centre by a pixel delta, as a drag does.
func (v View) Pan(dpx, dpy float64) View {
	p := v.Prec()
	return View{
		CX:    v.CX.Add(-dpx*v.Scale, p),
		CY:    v.CY.Add(dpy*v.Scale, p),
		Scale: v.Scale,
	}
}

// ZoomAt multiplies the magnification by factor while keeping the world point
// currently under (px, py) pinned to that same pixel.
func (v View) ZoomAt(px, py float64, w, h int, factor float64) View {
	s2 := v.Scale / factor
	// Pinning the point under the cursor means the centre moves by that
	// pixel's offset scaled by the *change* in pixel size — a float64 delta,
	// whatever the depth.
	d := v.Scale - s2
	p := viewPrec(s2)
	return View{
		CX:    v.CX.Add((px-float64(w)/2)*d, p),
		CY:    v.CY.Add(-(py-float64(h)/2)*d, p),
		Scale: s2,
	}
}

// Recentre moves the centre to a pixel and sets a new scale, as releasing the
// zoom marquee does.
func (v View) Recentre(px, py float64, w, h int, scale float64) View {
	dx, dy := v.Offset(px, py, w, h)
	p := viewPrec(scale)
	return View{CX: v.CX.Add(dx, p), CY: v.CY.Add(dy, p), Scale: scale}
}

// Equal compares by value. View holds Reals, so == will not do.
func (v View) Equal(o View) bool {
	return v.Scale == o.Scale && v.CX.Equal(o.CX) && v.CY.Equal(o.CY)
}

// Width is the horizontal extent of the view in world units.
func (v View) Width(w int) float64 { return v.Scale * float64(w) }

// Magnification is the zoom relative to the canonical 3.2-wide home view. It
// is the number users recognise from Fractint's status line.
func (v View) Magnification(w int) float64 {
	if wd := v.Width(w); wd > 0 {
		return 3.2 / wd
	}
	return 1
}

// ColorMode selects which quantity the palette is applied to. Because the
// modes need different data out of the iteration loop, changing mode forces a
// re-render rather than a mere re-colour.
type ColorMode string

const (
	// ColorSmooth uses the renormalised (fractional) escape count.
	ColorSmooth ColorMode = "smooth"
	// ColorIteration uses the raw integer escape count, giving the hard
	// concentric bands of classic Fractint output.
	ColorIteration ColorMode = "iteration"
	// ColorDistance uses an estimate of the distance to the set boundary,
	// which draws the boundary as a crisp line of zoom-independent width.
	ColorDistance ColorMode = "distance"
	// ColorTrap uses the orbit's closest approach to a trap shape.
	ColorTrap ColorMode = "trap"
)

// ColorModes are the modes in menu order, with parallel labels.
var (
	ColorModes      = []ColorMode{ColorSmooth, ColorIteration, ColorDistance, ColorTrap}
	ColorModeLabels = []string{"smooth", "iteration", "distance est.", "orbit trap"}
)

// Guess selects whether solid guessing is used: skipping a sample whose four
// enclosing neighbours already agree, on the assumption that the sample between
// them agrees too.
//
// It is a two-value setting rather than the graded off/safe/fast that Fractint
// offered, because measurement did not support a middle ground. A looser
// tolerance — agreement in rendered colour rather than bit-for-bit — was tried
// and skipped only ~2% more samples while multiplying the error by five, so
// there was nothing to trade. See BenchmarkGuessing and
// TestGuessOnAgreesWithExact.
type Guess string

const (
	// GuessOff computes every sample. Always exact.
	GuessOff Guess = "off"
	// GuessOn skips a sample when its four enclosing neighbours are
	// bit-for-bit identical. In a smooth gradient that essentially never
	// happens, so the saving comes from genuinely flat areas — which are also
	// the expensive ones, since a point that never escapes costs the full
	// iteration budget. On an interior-heavy deep zoom this is an order of
	// magnitude faster for a few tenths of a percent of pixels changed.
	GuessOn Guess = "on"
)

// Guesses are the settings in menu order.
var Guesses = []Guess{GuessOff, GuessOn}

// Coloring describes how iteration results become pixels.
type Coloring struct {
	Palette string         `json:"palette"` // preset name, or CustomPalette
	Stops   []palette.Stop `json:"stops,omitempty"`
	Density float64        `json:"density"` // palette cycles per 256 iterations
	Offset  float64        `json:"offset"`  // rotation of the ramp, 0..1
	Mode    ColorMode      `json:"mode"`
	Trap    int            `json:"trap"`    // fractal.TrapKind, for ColorTrap
	LogMap  bool           `json:"log_map"` // compress escape counts logarithmically
	Inside  RGB            `json:"inside"`  // colour of the set itself

	Cycle      bool    `json:"cycle"`       // animate the ramp rotation
	CycleSpeed float64 `json:"cycle_speed"` // rotations per second

	// Relief lighting treats the colouring value as a height field and lights
	// it from a movable source, which is what gives the classic embossed look.
	Light      bool    `json:"light"`
	LightAz    float64 `json:"light_az"`    // azimuth, degrees clockwise from east
	LightEl    float64 `json:"light_el"`    // elevation above the plane, degrees
	LightDepth float64 `json:"light_depth"` // height exaggeration
	LightAmb   float64 `json:"light_amb"`   // ambient floor, 0..1
}

// CustomPalette is the Palette name that means "use Stops".
const CustomPalette = "Custom"

// RGB is a JSON-friendly colour.
type RGB struct {
	R uint8 `json:"r"`
	G uint8 `json:"g"`
	B uint8 `json:"b"`
}

// Ramp resolves the colouring to a baked palette.
func (c Coloring) Ramp() *palette.Palette {
	if c.Palette == CustomPalette && len(c.Stops) > 0 {
		return palette.New(CustomPalette, palette.RGBA(c.Inside.R, c.Inside.G, c.Inside.B), c.Stops)
	}
	return palette.ByName(c.Palette)
}

// EffectiveMode is the mode actually used for a formula. Distance estimation
// needs a derivative that non-analytic formulas cannot supply, so it degrades
// to smooth colouring rather than rendering something meaningless.
func (c Coloring) EffectiveMode(f fractal.Fractal) ColorMode {
	if c.Mode == ColorDistance && (f == nil || !f.HasDeriv()) {
		return ColorSmooth
	}
	if c.Mode == "" {
		return ColorSmooth
	}
	return c.Mode
}

// Need reports the optional orbit data this colouring requires of the kernel.
func (c Coloring) Need(f fractal.Fractal) fractal.Need {
	switch c.EffectiveMode(f) {
	case ColorDistance:
		return fractal.NeedDeriv
	case ColorTrap:
		return fractal.NeedTrap
	}
	return 0
}

// Equal compares two colourings. Coloring carries a slice, so == will not do.
func (c Coloring) Equal(o Coloring) bool {
	if c.Palette != o.Palette || c.Density != o.Density || c.Offset != o.Offset ||
		c.Mode != o.Mode || c.Trap != o.Trap || c.LogMap != o.LogMap ||
		c.Inside != o.Inside || c.Cycle != o.Cycle || c.CycleSpeed != o.CycleSpeed ||
		c.Light != o.Light || c.LightAz != o.LightAz || c.LightEl != o.LightEl ||
		c.LightDepth != o.LightDepth || c.LightAmb != o.LightAmb {
		return false
	}
	return slices.Equal(c.Stops, o.Stops)
}

// Formula is the source of the Custom type. It lives in the state so that a
// saved parameter file carries the formula that produced the image, not just a
// reference to whatever happens to be typed in at the time.
type Formula struct {
	Init string `json:"init"` // expression for z₀
	Iter string `json:"iter"` // expression for z_{n+1}
}

// State is the full snapshot of the explorer.
type State struct {
	Type    string     `json:"type"`
	View    View       `json:"view"`
	MaxIter int        `json:"max_iter"`
	Bailout float64    `json:"bailout"`
	P       [4]float64 `json:"params"`
	Color   Coloring   `json:"coloring"`
	Super   int        `json:"supersample"` // samples per pixel axis, 1..3
	Guess   Guess      `json:"guess"`
	Formula Formula    `json:"formula"`
}

// Equal compares two states. State carries a slice, so == will not do.
func (s State) Equal(o State) bool {
	if s.Type != o.Type || !s.View.Equal(o.View) || s.MaxIter != o.MaxIter ||
		s.Bailout != o.Bailout || s.P != o.P || s.Super != o.Super ||
		s.Guess != o.Guess || s.Formula != o.Formula {
		return false
	}
	return s.Color.Equal(o.Color)
}

// Clone returns a deep copy, so that editing a palette cannot reach back into
// states already recorded in the history.
func (s State) Clone() State {
	s.Color.Stops = slices.Clone(s.Color.Stops)
	return s
}

// Default builds the state the explorer starts in, framed for a w×h viewport.
func Default(w, h int) State {
	st := State{
		MaxIter: 256,
		Bailout: 256, // a large radius; the smooth escape count needs room
		Super:   1,
		Guess:   GuessOn,
		Formula: Formula{Init: fractal.DefaultInit, Iter: fractal.DefaultIter},
		Color: Coloring{
			Palette: "Ultra",
			// At the home view almost everything escapes within ~40
			// iterations, so a single palette cycle per 256 iterations would
			// only ever use the first sliver of the ramp. Eight cycles spread
			// the ramp across the useful range without banding, and still
			// reads well at deep zoom.
			Density:    8,
			Mode:       ColorSmooth,
			Inside:     RGB{0, 0, 0},
			CycleSpeed: 0.15,
			LightAz:    135,
			LightEl:    45,
			LightDepth: 1,
			LightAmb:   0.35,
		},
	}
	return st.ApplyDefaults(fractal.ByName("Mandelbrot"), w, h)
}

// ApplyDefaults switches the state to a formula, adopting the view and any
// presentation that formula asks for. What a formula leaves at zero is a
// deliberate "no preference", and the user's current value survives.
func (s State) ApplyDefaults(f fractal.Fractal, w, h int) State {
	if f == nil {
		return s
	}
	d := f.Defaults()
	s.Type = f.Name()
	s.P = fractal.DefaultParams(f)
	s.View = HomeView(f, w, h)
	if d.MaxIter > 0 {
		s.MaxIter = d.MaxIter
	}
	if d.Bailout > 0 {
		s.Bailout = d.Bailout
	}
	if d.Density > 0 {
		s.Color.Density = d.Density
	}
	return s
}

// HomeView frames a formula's default region inside a w×h viewport, fitting
// whichever axis is tighter so nothing is cropped.
func HomeView(f fractal.Fractal, w, h int) View {
	if f == nil {
		return View{Scale: 3.2 / 800}
	}
	d := f.Defaults()
	width := d.Width
	if width <= 0 {
		width = 3.2
	}
	if w <= 0 || h <= 0 {
		return NewView(d.CX, d.CY, width/800)
	}
	// The default width describes the shorter dimension of a square-ish frame;
	// fit it so the region is fully visible in either orientation.
	scale := width / float64(w)
	if s := width / float64(h); s > scale {
		scale = s
	}
	return NewView(d.CX, d.CY, scale)
}

// Options projects the state onto the kernel-facing options struct.
func (s State) Options() fractal.Options {
	f := s.Fractal()
	return fractal.Options{
		MaxIter: s.MaxIter,
		Bailout: s.Bailout,
		P:       s.P,
		Need:    s.Color.Need(f),
		Trap:    fractal.Trap{Kind: fractal.TrapKind(s.Color.Trap)},
	}.Normalize()
}

// Fractal resolves the state's type name. For the custom type it compiles the
// stored formula, falling back to the default expressions when the source does
// not parse — FormulaError reports why, so the UI can say so without the
// drawing path having to deal with an error return.
func (s State) Fractal() fractal.Fractal {
	if s.Type == fractal.CustomName {
		if f, err := fractal.NewCustom(s.Formula.Init, s.Formula.Iter); err == nil {
			return f
		}
		f, _ := fractal.NewCustom("", "")
		return f
	}
	return fractal.ByName(s.Type)
}

// FormulaError reports why the custom formula does not compile, or nil.
func (s State) FormulaError() error {
	if s.Type != fractal.CustomName {
		return nil
	}
	_, err := fractal.NewCustom(s.Formula.Init, s.Formula.Iter)
	return err
}

// Sanitize repairs values that a hand-edited or corrupt file could carry, so
// a bad parameter file cannot wedge the renderer.
func (s State) Sanitize() State {
	if _, ok := fractal.Lookup(s.Type); !ok {
		s.Type = "Mandelbrot"
	}
	if s.Formula.Init == "" {
		s.Formula.Init = fractal.DefaultInit
	}
	if s.Formula.Iter == "" {
		s.Formula.Iter = fractal.DefaultIter
	}
	s.MaxIter = clampInt(s.MaxIter, 16, 1_000_000)
	if !(s.Bailout >= 2) {
		s.Bailout = 256
	}
	s.Bailout = math.Min(s.Bailout, 1e12)
	s.Super = clampInt(s.Super, 1, 3)
	if !slices.Contains(Guesses, s.Guess) {
		// "safe" and "fast" are the names an earlier build wrote.
		s.Guess = GuessOn
	}
	if !(s.View.Scale > 0) || math.IsInf(s.View.Scale, 0) {
		s.View.Scale = 3.2 / 800
	}
	if !s.View.CX.Finite() {
		s.View.CX = Real{}
	}
	if !s.View.CY.Finite() {
		s.View.CY = Real{}
	}
	// Coordinates travel through decimal, and a decimal string does not carry
	// the mantissa width it came from. Re-rounding to the precision this zoom
	// level calls for is what makes save/load exact rather than merely close.
	vp := s.View.Prec()
	s.View.CX = s.View.CX.Round(vp)
	s.View.CY = s.View.CY.Round(vp)
	if !(s.Color.Density > 0) {
		s.Color.Density = 8
	}
	s.Color.Density = math.Min(s.Color.Density, 512)
	if math.IsNaN(s.Color.Offset) {
		s.Color.Offset = 0
	}
	if s.Color.Palette == "" {
		s.Color.Palette = "Ultra"
	}
	if s.Color.Palette == CustomPalette && len(s.Color.Stops) == 0 {
		s.Color.Palette = "Ultra"
	}
	if !slices.Contains(ColorModes, s.Color.Mode) {
		s.Color.Mode = ColorSmooth
	}
	s.Color.Trap = clampInt(s.Color.Trap, 0, len(fractal.TrapNames)-1)
	if !(s.Color.CycleSpeed > 0) || s.Color.CycleSpeed > 5 {
		s.Color.CycleSpeed = 0.15
	}
	if math.IsNaN(s.Color.LightAz) {
		s.Color.LightAz = 135
	}
	if math.IsNaN(s.Color.LightEl) || s.Color.LightEl <= 0 || s.Color.LightEl >= 90 {
		s.Color.LightEl = 45
	}
	if !(s.Color.LightDepth > 0) || s.Color.LightDepth > 100 {
		s.Color.LightDepth = 1
	}
	if !(s.Color.LightAmb >= 0) || s.Color.LightAmb > 1 {
		s.Color.LightAmb = 0.35
	}
	for i := range s.Color.Stops {
		st := &s.Color.Stops[i]
		if math.IsNaN(st.Pos) {
			st.Pos = 0
		}
		st.Pos = math.Max(0, math.Min(1, st.Pos))
	}
	// The gradient sampler needs its control points in order.
	slices.SortStableFunc(s.Color.Stops, func(a, b palette.Stop) int {
		switch {
		case a.Pos < b.Pos:
			return -1
		case a.Pos > b.Pos:
			return 1
		}
		return 0
	})
	for i := range s.P {
		if math.IsNaN(s.P[i]) || math.IsInf(s.P[i], 0) {
			s.P[i] = 0
		}
	}
	return s
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Save writes the state as indented JSON.
func (s State) Save(path string) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, b, 0o644)
}

// Load reads a state written by Save.
func Load(path string) (State, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	return Parse(b)
}

// Parse decodes a state from JSON and repairs anything out of range, so a
// hand-edited or truncated file cannot wedge the renderer.
func Parse(b []byte) (State, error) {
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return s, err
	}
	return s.Sanitize(), nil
}

// History is the zoom stack: a linear undo/redo list of states.
type History struct {
	items []State
	idx   int
}

// maxHistory bounds the stack; exploration sessions are long.
const maxHistory = 256

// Push records a new state, discarding anything ahead of the cursor.
// Consecutive identical states collapse, so a no-op action leaves no entry.
func (h *History) Push(s State) {
	h.truncate()
	if len(h.items) > 0 && h.items[len(h.items)-1].Equal(s) {
		return
	}
	h.append(s)
}

// BeginGesture opens a continuous gesture such as a drag or a wheel burst. It
// records the state the gesture starts from and then opens a second, working
// entry for Replace to keep updating, so the whole gesture reads as a single
// step when walking the history. The duplicate is appended directly, bypassing
// Push's collapsing.
func (h *History) BeginGesture(s State) {
	h.Push(s)
	h.truncate()
	h.append(s)
}

// Replace overwrites the state at the cursor. Used during a gesture opened by
// BeginGesture.
func (h *History) Replace(s State) {
	if len(h.items) == 0 {
		h.append(s)
		return
	}
	h.items[h.idx] = s.Clone()
}

func (h *History) truncate() {
	if len(h.items) > 0 && h.idx < len(h.items)-1 {
		h.items = h.items[:h.idx+1]
	}
}

func (h *History) append(s State) {
	h.items = append(h.items, s.Clone())
	if len(h.items) > maxHistory {
		h.items = h.items[len(h.items)-maxHistory:]
	}
	h.idx = len(h.items) - 1
}

// Back steps to the previous state.
func (h *History) Back() (State, bool) {
	if h.idx <= 0 {
		return State{}, false
	}
	h.idx--
	return h.items[h.idx].Clone(), true
}

// Forward steps to the next state.
func (h *History) Forward() (State, bool) {
	if h.idx >= len(h.items)-1 {
		return State{}, false
	}
	h.idx++
	return h.items[h.idx].Clone(), true
}

// Pos reports the cursor position and stack depth, for the status display.
func (h *History) Pos() (int, int) { return h.idx + 1, len(h.items) }
