package fractal

import (
	"math"
	"math/big"
	"testing"
)

func bigOf(s string, prec uint) *big.Float {
	f, _, err := big.ParseFloat(s, 10, prec, big.ToNearestEven)
	if err != nil {
		panic(err)
	}
	return f
}

// directBig iterates z² + c entirely in high precision. It is the oracle for
// perturbation: unlike a float64 render it has no rounding of its own that could
// explain a difference away.
//
// Comparing against the float64 path instead would be measuring the wrong thing.
// Rounding a 200-bit reference coordinate to float64 moves it by ~1e-17, and a
// point 1400 iterations from escaping is sensitive enough that such a move
// shifts the escape count by dozens — so the two paths would be answering
// questions about different points.
func directBig(cx, cy *big.Float, o *Options, prec uint) (iter int, escaped bool, mod2 float64) {
	nf := func() *big.Float { return new(big.Float).SetPrec(prec) }
	zr, zi := nf(), nf()
	zr2, zi2, t := nf(), nf(), nf()
	sum := nf()
	bail := new(big.Float).SetPrec(prec).SetFloat64(o.Bailout2)

	for n := range o.MaxIter {
		zr2.Mul(zr, zr)
		zi2.Mul(zi, zi)
		sum.Add(zr2, zi2)
		if sum.Cmp(bail) > 0 {
			m, _ := sum.Float64()
			return n, true, m
		}
		t.Mul(zr, zi)
		zi.Add(t, t)
		zi.Add(zi, cy)
		zr.Sub(zr2, zi2)
		zr.Add(zr, cx)
	}
	return o.MaxIter, false, 0
}

// TestPerturbationMatchesOracle is the load-bearing test for deep zoom.
//
// Perturbation is not exact arithmetic: δz is carried in float64, and near the
// boundary its error is amplified by the orbit's own sensitivity. What a
// renderer needs is not exactness but that the error stays *below the pixel
// size* — if it does, the image is right. So the escape count has to agree with
// the high-precision oracle for the overwhelming majority of points, and the
// few disagreements have to be pixels whose answer is changing anyway.
func TestPerturbationMatchesOracle(t *testing.T) {
	const prec = 240
	m := Mandelbrot{}
	o := Options{MaxIter: 2500, Bailout: 256}.Normalize()

	// Reference points chosen to exercise different regimes: near the boundary
	// where orbits are long, well outside where they are short, and inside a
	// bulb where they never escape.
	refs := []struct {
		name   string
		cx, cy string
		span   float64 // half-width of the sampled patch
	}{
		{"seahorse", "-0.7436438870371587", "0.13182590420531197", 1e-7},
		{"near cusp", "0.2501", "0", 1e-8},
		{"antenna", "-1.7548776662466927", "0", 1e-9},
		{"exterior", "0.4", "0.3", 1e-6},
		{"inside bulb", "-1.0", "0.05", 1e-7},
		{"deep spiral", "-1.2568853759026677", "0.3796264251860849", 1e-10},
	}

	for _, ref := range refs {
		t.Run(ref.name, func(t *testing.T) {
			cx := bigOf(ref.cx, prec)
			cy := bigOf(ref.cy, prec)
			orb := m.Reference(cx, cy, &o)
			if orb.Len() < 2 {
				t.Fatalf("reference orbit is only %d long", orb.Len())
			}

			const n = 15
			var checked, mismatch int
			var worstSmooth float64
			px := 2 * ref.span / (n - 1) // the sample spacing, i.e. one "pixel"

			for iy := range n {
				for ix := range n {
					dcr := ref.span * (2*float64(ix)/(n-1) - 1)
					dci := ref.span * (2*float64(iy)/(n-1) - 1)

					qx := new(big.Float).SetPrec(prec).Add(cx, new(big.Float).SetPrec(prec).SetFloat64(dcr))
					qy := new(big.Float).SetPrec(prec).Add(cy, new(big.Float).SetPrec(prec).SetFloat64(dci))
					wantIter, wantEsc, wantMod2 := directBig(qx, qy, &o, prec)

					got := m.Perturb(dcr, dci, orb, &o)
					checked++

					// Iter is only meaningful for a point that escaped; an
					// interior result leaves it at zero.
					if wantEsc != got.Escaped || (wantEsc && wantIter != got.Iter) {
						// Before calling it an error, ask whether this pixel's
						// answer is unstable anyway: if a neighbouring sample
						// escapes at a different count, a discrepancy here is
						// smaller than the resolution of the image.
						nIter, nEsc, _ := directBig(
							new(big.Float).SetPrec(prec).Add(qx, new(big.Float).SetPrec(prec).SetFloat64(px)),
							qy, &o, prec)
						if nEsc == wantEsc && nIter == wantIter {
							t.Errorf("δc = (%g, %g): oracle escaped=%v n=%d, perturbed escaped=%v n=%d, "+
								"and the neighbouring sample agrees with the oracle — so this is error, not sensitivity",
								dcr, dci, wantEsc, wantIter, got.Escaped, got.Iter)
						}
						mismatch++
						continue
					}
					if !wantEsc {
						continue
					}
					wantN := smooth(wantIter, wantMod2, math.Ln2, &o)
					if d := math.Abs(wantN - got.N); d > worstSmooth {
						worstSmooth = d
					}
				}
			}
			frac := float64(mismatch) / float64(checked)
			t.Logf("%d points, reference orbit %d long, %d unstable (%.2f%%), "+
				"worst smooth-count difference %.2g", checked, orb.Len(), mismatch, frac*100, worstSmooth)
			if frac > 0.05 {
				t.Errorf("%.1f%% of points disagreed with the oracle", frac*100)
			}
		})
	}
}

// boundaryPoint walks a segment from a point inside the set to one outside,
// halving it the given number of times, and returns the inside endpoint together
// with the surviving interval width.
//
// This is how the deep-zoom tests get their coordinates. Hard-coding forty
// digits copied from a screenshot would be a coordinate nobody could check;
// bisecting in high precision derives a genuine deep location from two obvious
// endpoints, and the width comes out known exactly.
func boundaryPoint(t *testing.T, prec uint, steps int, o *Options) (cx, cy *big.Float, width float64) {
	t.Helper()
	nf := func() *big.Float { return new(big.Float).SetPrec(prec) }
	m := Mandelbrot{}
	inside := func(x, y *big.Float) bool {
		_, esc, _ := directBig(x, y, o, prec)
		return !esc
	}

	// The period-2 centre is unambiguously in the set; a third of the way up is
	// unambiguously out of it.
	ax, ay := nf().SetFloat64(-1), nf().SetFloat64(0)
	bx, by := nf().SetFloat64(-1), nf().SetFloat64(0.4)
	if !inside(ax, ay) {
		t.Fatal("the segment's inner endpoint is not in the set")
	}
	if inside(bx, by) {
		t.Fatal("the segment's outer endpoint is in the set")
	}

	mx, my := nf(), nf()
	two := nf().SetFloat64(2)
	for range steps {
		mx.Add(ax, bx).Quo(mx, two)
		my.Add(ay, by).Quo(my, two)
		if inside(mx, my) {
			ax.Set(mx)
			ay.Set(my)
		} else {
			bx.Set(mx)
			by.Set(my)
		}
	}
	dy := nf().Sub(by, ay)
	w, _ := dy.Float64()
	_ = m
	return ax, ay, math.Abs(w)
}

// TestPerturbationAgreesWithOracleDeep checks perturbation against exact
// arithmetic at a depth float64 cannot address at all, which is where the
// failure mode perturbation is famous for lives: when the orbit passes close to
// the origin, Z + δz cancels and the lost precision surfaces as a blob of wrong
// pixels — the "Pauldelbrot glitch". Rebasing is supposed to prevent that, and a
// glitch cannot hide from a point-by-point comparison with the oracle.
func TestPerturbationAgreesWithOracleDeep(t *testing.T) {
	const prec = 400
	const steps = 80 // ~1e-25: far past float64, cheap enough to oracle
	m := Mandelbrot{}
	o := Options{MaxIter: 6000, Bailout: 256}.Normalize()

	cx, cy, w := boundaryPoint(t, prec, steps, &o)
	orb := m.Reference(cx, cy, &o)
	t.Logf("reference orbit %d iterations, patch %.3g wide", orb.Len(), w)

	nf := func() *big.Float { return new(big.Float).SetPrec(prec) }
	const n = 13
	var checked, disagreed, escapes, interiors int
	counts := map[int]bool{}
	// Straddle the boundary generously, for the same reason as above.
	for iy := range n {
		for ix := range n {
			dcr := 2 * w * (2*float64(ix)/(n-1) - 1)
			dci := 2 * w * (2*float64(iy)/(n-1) - 1)
			qx := nf().Add(cx, nf().SetFloat64(dcr))
			qy := nf().Add(cy, nf().SetFloat64(dci))

			wantIter, wantEsc, _ := directBig(qx, qy, &o, prec)
			got := m.Perturb(dcr, dci, orb, &o)
			checked++
			if wantEsc {
				escapes++
				counts[wantIter] = true
			} else {
				interiors++
			}
			if wantEsc != got.Escaped || (wantEsc && wantIter != got.Iter) {
				disagreed++
				if disagreed <= 5 {
					t.Errorf("δc = (%.3g, %.3g): oracle escaped=%v n=%d, perturbed escaped=%v n=%d",
						dcr, dci, wantEsc, wantIter, got.Escaped, got.Iter)
				}
			}
		}
	}
	// The patch has to straddle the boundary, or the comparison is trivial.
	if escapes == 0 || interiors == 0 {
		t.Fatalf("the patch does not straddle the boundary: %d escaping, %d interior",
			escapes, interiors)
	}
	t.Logf("%d points checked against exact arithmetic at ~1e-25: %d escaping "+
		"(%d distinct counts), %d interior, %d disagreements",
		checked, escapes, len(counts), interiors, disagreed)
	if disagreed > 0 {
		t.Errorf("%d of %d points disagree with exact arithmetic", disagreed, checked)
	}

	// The same patch through float64 has to be a single flat value, or the
	// comparison above is not demonstrating anything. This is the payoff:
	// perturbation separates points that float64 cannot tell apart at all.
	fcx, _ := cx.Float64()
	fcy, _ := cy.Float64()
	flat := map[int]bool{}
	for iy := range n {
		for ix := range n {
			dcr := 2 * w * (2*float64(ix)/(n-1) - 1)
			dci := 2 * w * (2*float64(iy)/(n-1) - 1)
			r := m.Iterate(fcx+dcr, fcy+dci, &o)
			key := -1
			if r.Escaped {
				key = r.Iter
			}
			flat[key] = true
		}
	}
	if len(flat) != 1 {
		t.Errorf("float64 resolved %d outcomes over a %.3g patch; it was expected to be blind",
			len(flat), 4*w)
	}
	t.Logf("over the same patch float64 resolves %d outcome, perturbation resolves %d",
		len(flat), len(counts)+1)
}

// TestPerturbationIsReferenceIndependent renders the same points twice from two
// different reference orbits.
//
// This is the sharpest check available at depths no oracle can reach cheaply: a
// glitch is by definition a place where the answer depends on which reference
// was used, so two references agreeing everywhere is exactly the property
// rebasing is supposed to provide. It also needs no high-precision iteration of
// its own, which is what makes it affordable here.
func TestPerturbationIsReferenceIndependent(t *testing.T) {
	const prec = 400
	const steps = 80
	m := Mandelbrot{}
	o := Options{MaxIter: 6000, Bailout: 256}.Normalize()

	ax, ay, w := boundaryPoint(t, prec, steps, &o)
	nf := func() *big.Float { return new(big.Float).SetPrec(prec) }

	// A second reference, offset by a third of the patch. Its orbit is a
	// completely different sequence of numbers.
	shift := 0.7 * w
	bx := nf().Add(ax, nf().SetFloat64(shift))
	by := nf().Set(ay)

	refA := m.Reference(ax, ay, &o)
	refB := m.Reference(bx, by, &o)
	t.Logf("reference orbits %d and %d iterations, patch %.3g", refA.Len(), refB.Len(), w)
	if refA.Len() == refB.Len() && refA.Zr[refA.Len()/2] == refB.Zr[refB.Len()/2] {
		t.Fatal("the two references are the same orbit; the test would prove nothing")
	}

	const n = 21
	var checked, disagreed, varied int
	seen := map[int]bool{}
	for iy := range n {
		for ix := range n {
			// The same absolute point, expressed as an offset from each
			// reference in turn.
			dx := 2 * w * (2*float64(ix)/(n-1) - 1)
			dy := 2 * w * (2*float64(iy)/(n-1) - 1)
			ra := m.Perturb(dx, dy, refA, &o)
			rb := m.Perturb(dx-shift, dy, refB, &o)
			checked++
			key := -1
			if ra.Escaped {
				key = ra.Iter
			}
			if !seen[key] {
				seen[key] = true
				varied++
			}
			if ra.Escaped != rb.Escaped || (ra.Escaped && ra.Iter != rb.Iter) {
				disagreed++
				if disagreed <= 5 {
					t.Errorf("(%.3g, %.3g): reference A says escaped=%v n=%d, reference B says escaped=%v n=%d",
						dx, dy, ra.Escaped, ra.Iter, rb.Escaped, rb.Iter)
				}
			}
		}
	}
	if varied < 2 {
		t.Fatal("the patch is uniform; the comparison proves nothing")
	}
	t.Logf("%d points, %d distinct outcomes, %d reference-dependent", checked, varied, disagreed)
	if disagreed > 0 {
		t.Errorf("%d of %d points depend on which reference was used", disagreed, checked)
	}
}

// TestReferenceOrbitStartsAtZero pins the property rebasing depends on.
func TestReferenceOrbitStartsAtZero(t *testing.T) {
	ro := Options{MaxIter: 50, Bailout: 256}.Normalize()
	orb := Mandelbrot{}.Reference(bigOf("-0.75", 100), bigOf("0.1", 100), &ro)
	if orb.Len() < 2 {
		t.Fatal("orbit too short")
	}
	if orb.Zr[0] != 0 || orb.Zi[0] != 0 {
		t.Errorf("Z0 = (%v, %v), want the origin — rebasing substitutes Z0 + z for z "+
			"and is only exact when Z0 is zero", orb.Zr[0], orb.Zi[0])
	}
}

// TestOnlyMandelbrotPerturbs documents which formulas take the deep-zoom path,
// so that adding one is a deliberate act rather than an accident.
func TestOnlyMandelbrotPerturbs(t *testing.T) {
	want := map[string]bool{"Mandelbrot": true}
	for _, f := range All() {
		_, ok := AsPerturber(f)
		if ok != want[f.Name()] {
			t.Errorf("%s: perturbation support = %v, expected %v", f.Name(), ok, want[f.Name()])
		}
	}
}

func BenchmarkPerturb(b *testing.B) {
	m := Mandelbrot{}
	o := Options{MaxIter: 5000, Bailout: 256}.Normalize()
	orb := m.Reference(bigOf("-1.7692990176102237571", 400), bigOf("0.0042368479187367906", 400), &o)
	b.ResetTimer()
	var sink Result
	for i := 0; i < b.N; i++ {
		sink = m.Perturb(1e-19*float64(i%97), 3e-19, orb, &o)
	}
	_ = sink
}

func BenchmarkReferenceOrbit(b *testing.B) {
	m := Mandelbrot{}
	cx := bigOf("-1.7692990176102237571298652846166767235527", 400)
	cy := bigOf("0.00423684791873679065711711745046141549342", 400)
	for i := 0; i < b.N; i++ {
		ro := Options{MaxIter: 10000, Bailout: 256}.Normalize()
		m.Reference(cx, cy, &ro)
	}
}
