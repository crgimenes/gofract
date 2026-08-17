package fractal

import (
	"math"
	"testing"
)

// noBoundedRegion lists the formulas for which a grid sweep is *expected* to
// find no bounded points, so that "nothing stayed bounded" is not treated as a
// broken default view:
//
//   - Newton converges everywhere, so there is no bounded-but-not-converged
//     region to find.
//   - The Sierpinski gasket has zero area, so the chance of a grid landing on
//     it is nil. The picture comes from the escape bands closing in on it.
var noBoundedRegion = map[string]bool{"Newton": true, "Sierpinski": true}

// TestEveryTypeProducesStructure sweeps each registered formula over its own
// default view and insists the result is an actual picture: values must vary,
// and — except for the formulas above — the view must contain some of the set
// as well as some of its outside. A formula whose arithmetic is right but whose
// default framing or parameters are wrong fails here, which is exactly how the
// Lambda default was caught: Fractint's stock λ gives a set with no interior.
func TestEveryTypeProducesStructure(t *testing.T) {
	for _, f := range All() {
		t.Run(f.Name(), func(t *testing.T) {
			d := f.Defaults()
			o := Options{MaxIter: d.MaxIter, Bailout: d.Bailout, P: DefaultParams(f)}.Normalize()

			const n = 60
			var escaped, inside int
			var minN, maxN = math.Inf(1), math.Inf(-1)
			for iy := range n {
				for ix := range n {
					x := d.CX + d.Width*(float64(ix)/(n-1)-0.5)
					y := d.CY + d.Width*(float64(iy)/(n-1)-0.5)
					r := f.Iterate(x, y, &o)
					if !r.Escaped {
						inside++
						continue
					}
					escaped++
					if math.IsNaN(r.N) || math.IsInf(r.N, 0) {
						t.Fatalf("(%v, %v) produced a non-finite value %v", x, y, r.N)
					}
					if r.N < 0 {
						t.Fatalf("(%v, %v) produced a negative value %v", x, y, r.N)
					}
					minN = math.Min(minN, r.N)
					maxN = math.Max(maxN, r.N)
				}
			}
			if escaped == 0 {
				t.Errorf("nothing resolved to a value over the default view")
			}
			if inside == 0 && !noBoundedRegion[f.Name()] {
				t.Errorf("nothing stayed bounded over the default view: " +
					"the framing or the default parameters are wrong")
			}
			// A single flat value everywhere would colour to one solid block.
			if escaped > 0 && maxN-minN < 1e-9 {
				t.Errorf("all values identical (%v): the view has no gradient", minN)
			}
			t.Logf("%d escaped, %d bounded, values %.2f..%.2f", escaped, inside, minN, maxN)
		})
	}
}

// TestNewtonConvergesToRoots checks the property that defines Newton's method
// here: wherever it converges, it converges *to a root of unity*, and the
// encoded value identifies which one.
func TestNewtonConvergesToRoots(t *testing.T) {
	f := Newton{}
	for _, n := range []float64{2, 3, 5, 8} {
		o := Options{MaxIter: 200, Bailout: 256}.Normalize()
		o.P[0] = n

		converged, total := 0, 0
		seen := map[int]bool{}
		for iy := range 40 {
			for ix := range 40 {
				x := -1.5 + 3*float64(ix)/39
				y := -1.5 + 3*float64(iy)/39
				total++
				r := f.Iterate(x, y, &o)
				if !r.Escaped {
					continue
				}
				converged++
				// Undo the band encoding to recover the basin index.
				band := r.N / newtonBands * n
				k := int(math.Floor(band))
				if k < 0 || k >= int(n) {
					t.Fatalf("n=%v: value %v decodes to basin %d, out of range", n, r.N, k)
				}
				seen[k] = true
			}
		}
		// Every root should claim some territory over a window this wide.
		if len(seen) != int(n) {
			t.Errorf("n=%v: only %d of %d basins appear", n, len(seen), int(n))
		}
		if frac := float64(converged) / float64(total); frac < 0.95 {
			t.Errorf("n=%v: only %.1f%% of points converged", n, frac*100)
		}
	}
}

// TestTrapsAreRecorded checks that the trap shapes are wired into the shared
// orbit bookkeeping: asking for one must produce a finite closest approach, and
// asking for none must leave the field alone.
func TestTrapsAreRecorded(t *testing.T) {
	for kind := TrapPoint; kind <= TrapSquare; kind++ {
		o := Options{MaxIter: 400, Bailout: 256, Need: NeedTrap,
			Trap: Trap{Kind: kind}}.Normalize()
		// A point outside the set, whose orbit wanders before leaving.
		r := Mandelbrot{}.Iterate(-0.75, 0.13, &o)
		if math.IsInf(r.Trap, 1) {
			t.Errorf("%s: no orbit point was ever measured", TrapNames[kind])
		}
		if r.Trap < 0 || math.IsNaN(r.Trap) {
			t.Errorf("%s: trap distance %v", TrapNames[kind], r.Trap)
		}
	}
	// Without the flag the kernel must not pay for trap tracking, and reports
	// the "never approached" sentinel.
	o := Options{MaxIter: 400, Bailout: 256, Trap: Trap{Kind: TrapPoint}}.Normalize()
	if r := (Mandelbrot{}).Iterate(-0.75, 0.13, &o); !math.IsInf(r.Trap, 1) {
		t.Errorf("trap recorded without NeedTrap: %v", r.Trap)
	}
}

// estimate is the exterior distance estimate, the quantity the renderer's
// ColorDistance mode is built on. It is spelled out here rather than imported
// because this test is checking that the *derivative* feeding it is right.
func estimate(r Result) float64 {
	dz := math.Hypot(r.Dr, r.Di)
	if dz == 0 || r.Mod2 <= 1 {
		return 0
	}
	mod := math.Sqrt(r.Mod2)
	return 2 * mod * math.Log(mod) / dz
}

// TestDistanceEstimateBoundsTheSet validates the derivative through the thing
// it exists for. The estimate approximates the distance from an exterior point
// to the set, and by the Koebe quarter theorem a disc of a quarter that radius
// is guaranteed to be free of the set.
//
// So: compute the estimate at a point outside the set, then check that nothing
// within DE/4 of it is inside. A wrong derivative makes the estimate wrong and
// this fails — which is a far more meaningful check than re-deriving the
// recurrence in the test and comparing it against itself.
func TestDistanceEstimateBoundsTheSet(t *testing.T) {
	const iter = 4000
	member := Options{MaxIter: iter, Bailout: 256}.Normalize()
	withDeriv := Options{MaxIter: iter, Bailout: 1e6, Need: NeedDeriv}.Normalize()
	m := Mandelbrot{}

	// Exterior points at a range of distances from the boundary, including
	// some very close to it where the estimate has to be small.
	// Exterior points only. (0.35, 0.15) looks like it should be outside but
	// sits inside the main cardioid, which is what the check below is for.
	points := [][2]float64{
		{0.4, 0.0}, {0.5, 0.5}, {-0.8, 0.4}, {-1.5, 0.2},
		{0.3, 0.02}, {2.0, 1.0}, {-1.9, 0.05}, {-0.2, 0.9},
	}
	for _, p := range points {
		r := m.Iterate(p[0], p[1], &withDeriv)
		if !r.Escaped {
			t.Fatalf("(%v, %v) is not an exterior point", p[0], p[1])
		}
		de := estimate(r)
		if de <= 0 || math.IsNaN(de) || math.IsInf(de, 0) {
			t.Errorf("(%v, %v): estimate %v is not a usable distance", p[0], p[1], de)
			continue
		}
		// Probe the guaranteed-clear disc.
		radius := de / 4
		const probes = 64
		for i := range probes {
			th := 2 * math.Pi * float64(i) / probes
			qx := p[0] + radius*math.Cos(th)
			qy := p[1] + radius*math.Sin(th)
			if !m.Iterate(qx, qy, &member).Escaped {
				t.Errorf("(%v, %v): estimate %.3g claims %.3g is clear, but (%v, %v) is in the set",
					p[0], p[1], de, radius, qx, qy)
				break
			}
		}
		t.Logf("(%6.3f, %6.3f) estimate %.4g", p[0], p[1], de)
	}
}

// TestDistanceEstimateShrinksTowardsBoundary is the other half of the estimate
// being meaningful: it has to track distance, not merely bound it.
//
// Only the ordering is asserted, not the magnitude. c = 1/4 is a parabolic
// point: the orbit lingers near the fixed point at ½ for ~π/√d iterations
// before leaving, the derivative piles up over all of them, and the estimate
// comes out around five times small a hair away from the cusp. That is a known
// limit of the estimator at parabolic points rather than a defect here — away
// from them, measured estimate-to-distance ratios sit between 0.2 and 3.
func TestDistanceEstimateShrinksTowardsBoundary(t *testing.T) {
	o := Options{MaxIter: 20000, Bailout: 1e6, Need: NeedDeriv}.Normalize()
	m := Mandelbrot{}
	// Approach the cusp at c = 1/4 along the real axis.
	prev := math.Inf(1)
	first := 0.0
	dists := []float64{0.5, 0.2, 0.1, 0.05, 0.02, 0.01, 1e-3, 1e-4}
	for _, d := range dists {
		r := m.Iterate(0.25+d, 0, &o)
		if !r.Escaped {
			t.Fatalf("0.25+%v should be outside the set", d)
		}
		de := estimate(r)
		if de >= prev {
			t.Errorf("at distance %v the estimate rose to %.4g from %.4g", d, de, prev)
		}
		if de <= 0 || math.IsNaN(de) || math.IsInf(de, 0) {
			t.Errorf("at distance %v the estimate is %.4g, not a usable distance", d, de)
		}
		if first == 0 {
			first = de
		}
		prev = de
	}
	// Across four decades of approach the estimate must genuinely collapse,
	// not merely inch downwards.
	if prev > first/100 {
		t.Errorf("estimate only fell from %.4g to %.4g across %v..%v",
			first, prev, dists[0], dists[len(dists)-1])
	}
}
