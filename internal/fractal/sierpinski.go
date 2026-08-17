package fractal

import "math"

func init() { Register(Sierpinski{}) }

// Sierpinski renders the Sierpinski gasket as an escape-time fractal rather
// than by the usual iterated-function-system construction.
//
// The trick is to iterate the IFS *backwards*. Each step doubles z and then
// folds it back towards the unit triangle:
//
//	z ← 2z;  if Im z > ½ then Im z -= 1;  else if Re z > ½ then Re z -= 1
//
// A point of the gasket can always be folded back and so stays bounded
// forever; anything else eventually escapes. The escape count then measures
// how far outside the gasket the point lies, which colours the surrounding
// space instead of leaving it blank the way a plain IFS plot does.
type Sierpinski struct{}

func (Sierpinski) Name() string { return "Sierpinski" }

func (Sierpinski) Defaults() Defaults {
	// The map keeps the unit square invariant, and the gasket it leaves behind
	// is inscribed in the triangle (0,0), (1,0), (0,1) — so the square is
	// exactly what to frame.
	//
	// Doubling means |z| grows by a factor of two per step rather than being
	// squared, so escape counts stay small: a handful of iterations, not
	// hundreds. Both the bailout and the colour density are scaled to suit — a
	// tight bailout keeps the counts low, and a high density spreads that
	// narrow range across the whole palette.
	return Defaults{CX: 0.5, CY: 0.5, Width: 1.15, MaxIter: 64, Bailout: 2, Density: 16}
}

func (Sierpinski) Params() []Param { return nil }

func (Sierpinski) Companion() string { return "" }

func (Sierpinski) HasDeriv() bool { return false }

func (Sierpinski) Iterate(x, y float64, o *Options) Result {
	t := track(o)
	zr, zi := x, y

	for n := 0; n < o.MaxIter; n++ {
		mod2 := zr*zr + zi*zi
		if mod2 > o.Bailout2 {
			// The modulus grows by a bounded factor of two rather than being
			// squared, so the escape count is renormalised linearly.
			return Result{
				N:       smoothLinear(n, mod2, math.Ln2, o),
				Iter:    n,
				Escaped: true,
				Mod2:    mod2,
				Trap:    t.trapMin,
			}
		}
		// The fold tests the point *before* doubling. That ordering is what
		// keeps the unit square invariant — 2y-1 lands back in (0,1) exactly
		// when y was above ½ — and so it is what makes the bounded set the
		// gasket. Testing the doubled value instead gives a different map
		// altogether, one whose bounded set is not a Sierpinski triangle.
		nzr, nzi := 2*zr, 2*zi
		if zi > 0.5 {
			nzi--
		} else if zr > 0.5 {
			nzr--
		}
		zr, zi = nzr, nzi

		if t.step(zr, zi) {
			return t.interior()
		}
	}
	return t.interior()
}
