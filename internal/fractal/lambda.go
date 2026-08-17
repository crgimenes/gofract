package fractal

import "math"

func init() { Register(Lambda{}) }

// Lambda is the Julia set of the complex logistic map
//
//	z_{n+1} = λ·z_n·(1 - z_n)
//
// the same recurrence that produces the real logistic map's period-doubling
// cascade. It is conjugate to z² + c, so the shapes are relatives of the Julia
// sets — but parameterised by λ the family reads very differently, with the
// two fixed points at 0 and 1 - 1/λ organising the picture.
type Lambda struct{}

func (Lambda) Name() string { return "Lambda" }

func (Lambda) Defaults() Defaults {
	// The interesting structure sits between the two fixed points, so the view
	// is offset to put them both on screen.
	return Defaults{CX: 0.5, CY: 0, Width: 2.6, MaxIter: 256, Bailout: 256, Density: 8}
}

func (Lambda) Params() []Param {
	return []Param{
		// The family is conjugate to z² + c via c = λ/2 - λ²/4, so the set is
		// connected — and has interior worth colouring — exactly when that c
		// lands in the Mandelbrot set. Fractint's stock λ = 0.85 + 0.6i maps to
		// c = 0.334 + 0.045i, which is *outside*: that set is a dust with no
		// interior at all, which makes for a poor first impression.
		//
		// This default is the λ that maps to c = -0.1226 + 0.7449i, the centre
		// of the period-3 bulb — so the set is the Douady rabbit, seen through
		// the logistic parameterisation.
		{Name: "lambda (real)", Index: 0, Min: -3, Max: 3, Default: -0.5525},
		{Name: "lambda (imag)", Index: 1, Min: -3, Max: 3, Default: 0.9596},
	}
}

func (Lambda) Companion() string { return "" }

func (Lambda) HasDeriv() bool { return true }

func (Lambda) Iterate(x, y float64, o *Options) Result {
	lr, li := o.P[0], o.P[1]
	t := track(o)
	deriv := o.Need&NeedDeriv != 0

	zr, zi := x, y
	// d/dz₀ of λz(1-z) is λ(1 - 2z), applied by the chain rule.
	dr, di := 1.0, 0.0

	for n := 0; n < o.MaxIter; n++ {
		mod2 := zr*zr + zi*zi
		if mod2 > o.Bailout2 {
			return t.escaped(n, mod2, math.Ln2, dr, di, o)
		}
		if deriv {
			// f'(z) = λ(1 - 2z)
			ar, ai := 1-2*zr, -2*zi
			fr := lr*ar - li*ai
			fi := lr*ai + li*ar
			dr, di = fr*dr-fi*di, fr*di+fi*dr
		}
		// w = z(1 - z) = z - z²
		wr := zr - (zr*zr - zi*zi)
		wi := zi - 2*zr*zi
		zr, zi = lr*wr-li*wi, lr*wi+li*wr

		if t.step(zr, zi) {
			return t.interior()
		}
	}
	return t.interior()
}
