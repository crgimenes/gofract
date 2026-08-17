package params

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/vmaciel/gofract/internal/fractal"
	"github.com/vmaciel/gofract/internal/palette"
)

func TestScreenWorldRoundTrip(t *testing.T) {
	v := NewView(-0.75, 0.1, 1.0/512)
	const w, h = 800, 600
	for _, p := range [][2]float64{{0, 0}, {400, 300}, {799, 599}, {123.5, 456.25}} {
		x, y := v.AtFloat(p[0], p[1], w, h)
		px, py := v.WorldToScreen(x, y, w, h)
		if math.Abs(px-p[0]) > 1e-9 || math.Abs(py-p[1]) > 1e-9 {
			t.Errorf("round trip of (%v,%v) gave (%v,%v)", p[0], p[1], px, py)
		}
	}
}

// TestZoomAtPinsCursor is the property that makes wheel zoom feel right: the
// point under the cursor must not move.
func TestZoomAtPinsCursor(t *testing.T) {
	v := NewView(0.25, -0.5, 1.0/256)
	const w, h = 1024, 768
	px, py := 300.0, 200.0
	wx, wy := v.AtFloat(px, py, w, h)

	for _, f := range []float64{2, 0.5, 1.15, 1 / 1.15} {
		z := v.ZoomAt(px, py, w, h, f)
		if got := z.Scale; math.Abs(got-v.Scale/f) > 1e-18 {
			t.Errorf("factor %v: scale %v, want %v", f, got, v.Scale/f)
		}
		gx, gy := z.AtFloat(px, py, w, h)
		if math.Abs(gx-wx) > 1e-12 || math.Abs(gy-wy) > 1e-12 {
			t.Errorf("factor %v: cursor point moved from (%v,%v) to (%v,%v)", f, wx, wy, gx, gy)
		}
	}
}

func TestHomeViewFitsRegion(t *testing.T) {
	f := fractal.ByName("Mandelbrot")
	width := f.Defaults().Width
	// A wide window fits the region vertically; a tall one horizontally.
	for _, wh := range [][2]int{{1600, 400}, {400, 1600}, {800, 800}} {
		v := HomeView(f, wh[0], wh[1])
		if v.Width(wh[0]) < width-1e-12 {
			t.Errorf("%v: horizontal extent %v is narrower than %v", wh, v.Width(wh[0]), width)
		}
		if hExt := v.Scale * float64(wh[1]); hExt < width-1e-12 {
			t.Errorf("%v: vertical extent %v is shorter than %v", wh, hExt, width)
		}
	}
}

func TestSanitizeRepairsGarbage(t *testing.T) {
	bad := State{
		Type:    "NoSuchFractal",
		View:    View{Scale: 0},
		MaxIter: -5,
		Bailout: 0.1,
		Super:   99,
		Color:   Coloring{Density: -1, Offset: math.NaN()},
	}
	s := bad.Sanitize()
	if s.Type != "Mandelbrot" {
		t.Errorf("type = %q", s.Type)
	}
	if s.MaxIter < 16 || s.Bailout < 2 || s.Super < 1 || s.Super > 3 {
		t.Errorf("out-of-range fields survived: %+v", s)
	}
	if !(s.View.Scale > 0) || !s.View.CX.Finite() {
		t.Errorf("view not repaired: %+v", s.View)
	}
	if !(s.Color.Density > 0) || math.IsNaN(s.Color.Offset) || s.Color.Palette == "" {
		t.Errorf("coloring not repaired: %+v", s.Color)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "params.json")
	want := Default(1280, 800)
	want.Type = "Julia"
	want.P[0], want.P[1] = -0.4, 0.6
	if err := want.Save(p); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(want) {
		t.Errorf("round trip changed the state:\n got %+v\nwant %+v", got, want)
	}
}

// TestHistoryGestureCollapses covers the rule that a continuous gesture is one
// history step: Back must return to where the gesture started, not to an
// intermediate frame of it.
func TestHistoryGestureCollapses(t *testing.T) {
	var h History
	start := Default(800, 600)
	h.Push(start)

	moved := start
	for i := 0; i < 5; i++ {
		if i == 0 {
			h.BeginGesture(moved)
		}
		moved.View.Scale /= 1.15
		h.Replace(moved)
	}
	if _, n := h.Pos(); n != 2 {
		t.Fatalf("gesture produced %d entries, want 2", n)
	}
	got, ok := h.Back()
	if !ok {
		t.Fatal("Back failed")
	}
	if !got.Equal(start) {
		t.Errorf("Back gave centre %s, want the pre-gesture state", got.View.CX.Short(6))
	}
	fwd, ok := h.Forward()
	if !ok {
		t.Fatal("Forward failed")
	}
	if !fwd.Equal(moved) {
		t.Errorf("Forward gave scale %g, want the post-gesture %g", fwd.View.Scale, moved.View.Scale)
	}
}

func TestHistoryPushCollapsesDuplicates(t *testing.T) {
	var h History
	s := Default(800, 600)
	h.Push(s)
	h.Push(s)
	h.Push(s)
	if _, n := h.Pos(); n != 1 {
		t.Errorf("identical pushes produced %d entries, want 1", n)
	}
}

// TestHistoryPushTruncatesRedo checks that exploring after stepping back
// discards the abandoned branch.
func TestHistoryPushTruncatesRedo(t *testing.T) {
	var h History
	a := Default(800, 600)
	b, c := a, a
	b.MaxIter = 512
	c.MaxIter = 1024
	h.Push(a)
	h.Push(b)
	h.Push(c)
	if _, ok := h.Back(); !ok {
		t.Fatal("Back failed")
	}
	d := a
	d.MaxIter = 2048
	h.Push(d)
	if _, ok := h.Forward(); ok {
		t.Error("redo survived a new push")
	}
	if i, n := h.Pos(); i != 3 || n != 3 {
		t.Errorf("position %d/%d, want 3/3", i, n)
	}
}

// TestCustomPaletteRoundTrip covers the newest thing on the wire: an edited
// palette has to survive save/load, and the state comparison has to notice when
// one stop changes — State carries a slice, so == would not have caught it.
func TestCustomPaletteRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "params.json")
	want := Default(1280, 800)
	want.Color.Palette = CustomPalette
	want.Color.Stops = []palette.Stop{
		{Pos: 0, R: 10, G: 20, B: 30},
		{Pos: 0.4, R: 200, G: 100, B: 0},
		{Pos: 0.75, R: 0, G: 0, B: 255},
	}
	want.Color.Cycle = true
	want.Color.Mode = ColorTrap
	want.Color.Trap = 2
	want.Guess = GuessOff
	if err := want.Save(p); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(want) {
		t.Fatalf("round trip changed the state:\n got %+v\nwant %+v", got.Color, want.Color)
	}
	// A single changed component must register as different.
	other := got.Clone()
	other.Color.Stops[1].G++
	if other.Equal(got) {
		t.Error("Equal ignored a changed palette stop")
	}
	// And Clone must not alias the original's stops.
	if got.Color.Stops[1].G == other.Color.Stops[1].G {
		t.Error("Clone shared the stop slice with the original")
	}
}

// TestSanitizeOrdersAndRepairsStops checks the invariant the gradient sampler
// depends on: control points arrive sorted and in range whatever the file said.
func TestSanitizeOrdersAndRepairsStops(t *testing.T) {
	s := Default(800, 600)
	s.Color.Palette = CustomPalette
	s.Color.Stops = []palette.Stop{
		{Pos: 0.9, R: 1}, {Pos: -3, G: 2}, {Pos: math.NaN(), B: 3}, {Pos: 0.5},
	}
	s.Guess = "safe" // a name an earlier build wrote
	s = s.Sanitize()

	if s.Guess != GuessOn {
		t.Errorf("legacy guess value became %q", s.Guess)
	}
	prev := -1.0
	for _, st := range s.Color.Stops {
		if st.Pos < 0 || st.Pos > 1 || math.IsNaN(st.Pos) {
			t.Errorf("stop position %v out of range", st.Pos)
		}
		if st.Pos < prev {
			t.Errorf("stops out of order: %v after %v", st.Pos, prev)
		}
		prev = st.Pos
	}
}
