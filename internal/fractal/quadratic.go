package fractal

import "math"

func init() {
	Register(BurningShip{})
	Register(Tricorn{})
}

// BurningShip iterates
//
//	z_{n+1} = (|Re z_n| + i·|Im z_n|)² + c
//
// The absolute values break the formula's analyticity — and with it the
// symmetry about the real axis that the Mandelbrot set has — which is what
// produces the flame-like rigging the fractal is named for.
type BurningShip struct{}

func (BurningShip) Name() string { return "Burning Ship" }

func (BurningShip) Defaults() Defaults {
	return Defaults{CX: -0.5, CY: -0.5, Width: 3.4, MaxIter: 256, Bailout: 256, Density: 8}
}

func (BurningShip) Params() []Param { return nil }

func (BurningShip) Companion() string { return "" }

// HasDeriv is false: the absolute values make the map non-analytic, so there is
// no complex derivative for distance estimation to use.
func (BurningShip) HasDeriv() bool { return false }

func (BurningShip) Iterate(cr, ci float64, o *Options) Result {
	t := track(o)
	var zr, zi, zr2, zi2 float64

	for n := 0; n < o.MaxIter; n++ {
		if zr2+zi2 > o.Bailout2 {
			return t.escaped(n, zr2+zi2, math.Ln2, 0, 0, o)
		}
		// Squaring |Re z| + i|Im z| leaves the real part unchanged — the signs
		// cancel — and makes the imaginary part 2|Re z · Im z|.
		zi = 2*math.Abs(zr*zi) + ci
		zr = zr2 - zi2 + cr
		zr2 = zr * zr
		zi2 = zi * zi

		if t.step(zr, zi) {
			return t.interior()
		}
	}
	return t.interior()
}

// Tricorn — also called the Mandelbar — iterates the *conjugate* square:
//
//	z_{n+1} = conj(z_n)² + c
//
// Conjugating flips the sign of the cross term, turning the Mandelbrot set's
// one cusp into three and giving the set its three-cornered outline.
type Tricorn struct{}

func (Tricorn) Name() string { return "Tricorn" }

func (Tricorn) Defaults() Defaults {
	return Defaults{CX: -0.25, CY: 0, Width: 3.6, MaxIter: 256, Bailout: 256, Density: 8}
}

func (Tricorn) Params() []Param { return nil }

func (Tricorn) Companion() string { return "" }

// HasDeriv is false: conjugation is not complex-differentiable.
func (Tricorn) HasDeriv() bool { return false }

func (Tricorn) Iterate(cr, ci float64, o *Options) Result {
	t := track(o)
	var zr, zi, zr2, zi2 float64

	for n := 0; n < o.MaxIter; n++ {
		if zr2+zi2 > o.Bailout2 {
			return t.escaped(n, zr2+zi2, math.Ln2, 0, 0, o)
		}
		// conj(a+bi)² = a² - b² - 2abi: the same real part as the plain
		// square, with the imaginary part negated.
		zi = -2*zr*zi + ci
		zr = zr2 - zi2 + cr
		zr2 = zr * zr
		zi2 = zi * zi

		if t.step(zr, zi) {
			return t.interior()
		}
	}
	return t.interior()
}
