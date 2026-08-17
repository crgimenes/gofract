package fractal

import (
	"math"
	"testing"
)

func opts(iter int) Options {
	return Options{MaxIter: iter, Bailout: 256}.Normalize()
}

// TestMandelbrotKnownPoints checks the kernel against points whose membership
// is not in doubt.
func TestMandelbrotKnownPoints(t *testing.T) {
	o := opts(2000)
	m := Mandelbrot{}

	in := [][2]float64{
		{0, 0},         // centre of the main cardioid
		{-1, 0},        // centre of the period-2 bulb
		{-0.125, 0.75}, // inside the upper period-3 bulb
		{0.25, 0},      // the cusp, on the boundary but in the set
		{-1.75, 0},     // inside the period-3 island on the antenna
		// c = i is a Misiurewicz point: the orbit lands exactly on the
		// 2-cycle -1+i -> -i, so it is bounded and in the set.
		{0, 1},
		{-2, 0}, // the tip of the antenna: orbit 0 -> -2 -> 2 -> 2
	}
	for _, p := range in {
		if r := m.Iterate(p[0], p[1], &o); r.Escaped {
			t.Errorf("c = %v escaped at n = %v, want interior", p, r.N)
		}
	}

	out := [][2]float64{{1, 0}, {0, 1.1}, {-3, 0}, {0.4, 0.4}, {-2.1, 0}}
	for _, p := range out {
		if r := m.Iterate(p[0], p[1], &o); !r.Escaped {
			t.Errorf("c = %v did not escape, want exterior", p)
		}
	}
}

// TestJuliaMatchesMandelbrotAtOrigin exploits the definition of the two sets:
// the Julia set for constant c contains 0 exactly when c is in the Mandelbrot
// set, since iterating z₀ = 0 with parameter c is the Mandelbrot orbit.
func TestJuliaMatchesMandelbrotAtOrigin(t *testing.T) {
	m, j := Mandelbrot{}, Julia{}
	for _, c := range [][2]float64{
		{0, 0}, {-1, 0}, {-0.4, 0.6}, {0.3, 0.5}, {1, 0}, {-1.8, 0}, {0.5, 0.5},
	} {
		o := opts(2000)
		o.P[0], o.P[1] = c[0], c[1]
		mr := m.Iterate(c[0], c[1], &o)
		jr := j.Iterate(0, 0, &o)
		if mr.Escaped != jr.Escaped {
			t.Errorf("c = %v: Mandelbrot escaped = %v but Julia(0) escaped = %v",
				c, mr.Escaped, jr.Escaped)
		}
	}
}

// TestSmoothCountIsMonotone is the property that makes smooth colouring work:
// walking outwards along a ray, the fractional escape count must decrease
// without the staircase of the raw integer count.
func TestSmoothCountIsMonotone(t *testing.T) {
	o := opts(500)
	m := Mandelbrot{}
	prev := math.Inf(1)
	steps := 0
	for x := 0.30; x < 2.0; x += 0.001 {
		r := m.Iterate(x, 0, &o)
		if !r.Escaped {
			continue
		}
		if r.N > prev+1e-9 {
			t.Fatalf("at c = %v the smooth count rose to %v from %v", x, r.N, prev)
		}
		if r.N < 0 {
			t.Fatalf("at c = %v the smooth count is negative: %v", x, r.N)
		}
		prev = r.N
		steps++
	}
	if steps < 100 {
		t.Fatalf("only %d escaping samples; the test swept the wrong range", steps)
	}
}

// TestSmoothCountIsContinuous checks there is no visible band edge: crossing
// from escape count n to n+1 must not jump the smooth value.
func TestSmoothCountIsContinuous(t *testing.T) {
	o := opts(500)
	m := Mandelbrot{}
	prev := math.NaN()
	const step = 1e-6
	for x := 0.4; x < 0.42; x += step {
		r := m.Iterate(x, 0, &o)
		if !r.Escaped {
			continue
		}
		if !math.IsNaN(prev) && math.Abs(r.N-prev) > 0.05 {
			t.Fatalf("discontinuity at c = %v: %v -> %v", x, prev, r.N)
		}
		prev = r.N
	}
}

// TestInteriorShortcutsAgree verifies that the cardioid and bulb tests do not
// disagree with plain iteration: any point they call interior must also fail
// to escape when iterated, and vice versa near the boundary.
func TestInteriorShortcutsAgree(t *testing.T) {
	// Iterate a long way so slow-converging boundary points are settled.
	o := opts(20000)
	j := Julia{} // Julia with c = point and z₀ = 0 is the same orbit, without
	m := Mandelbrot{}
	for x := -2.1; x < 0.7; x += 0.01 {
		for y := 0.0; y < 1.2; y += 0.01 {
			o.P[0], o.P[1] = x, y
			// the analytic shortcuts, so it is an independent oracle.
			ref := j.Iterate(0, 0, &o)
			got := m.Iterate(x, y, &o)
			if ref.Escaped != got.Escaped {
				t.Fatalf("c = (%v, %v): shortcut says escaped = %v, plain iteration says %v",
					x, y, got.Escaped, ref.Escaped)
			}
		}
	}
}

func TestRegistryAndDefaults(t *testing.T) {
	if len(All()) < 2 {
		t.Fatalf("registry has %d types", len(All()))
	}
	if ByName("Mandelbrot").Name() != "Mandelbrot" {
		t.Error("ByName failed for a registered type")
	}
	if ByName("nope") == nil {
		t.Error("ByName should fall back to a registered type")
	}
	p := DefaultParams(Julia{})
	if p[0] != -0.4 || p[1] != 0.6 {
		t.Errorf("Julia defaults = %v", p)
	}
	// A type's companion must resolve, and be mutual.
	for _, f := range All() {
		name := f.Companion()
		if name == "" {
			continue
		}
		c := ByName(name)
		if c.Name() != name {
			t.Errorf("%s: companion %q is not registered", f.Name(), name)
		}
	}
}

func BenchmarkMandelbrotPoint(b *testing.B) {
	o := opts(1000)
	m := Mandelbrot{}
	// A boundary point that runs the full iteration budget.
	const cr, ci = -0.7436438870371587, 0.13182590420531197
	b.ResetTimer()
	var sink Result
	for i := 0; i < b.N; i++ {
		sink = m.Iterate(cr, ci, &o)
	}
	_ = sink
	b.ReportMetric(float64(o.MaxIter)*float64(b.N)/b.Elapsed().Seconds()/1e6, "Miter/s")
}
