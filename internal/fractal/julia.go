package fractal

import "math"

func init() { Register(Julia{}) }

// Julia is the filled Julia set of z → z² + c for a fixed c. Where the
// Mandelbrot set varies c and starts from z₀ = 0, here c is frozen and the
// plane is scanned over the starting value z₀.
//
// The Mandelbrot set is precisely the map of which c produce a connected
// Julia set, which is why picking c by clicking on the Mandelbrot image is
// the natural way to explore this family.
type Julia struct{}

func (Julia) Name() string { return "Julia" }

// Defaults frame the plane a little tighter than the Mandelbrot set: a filled
// Julia set is contained in the disc of radius 2, but for the c values worth
// looking at it rarely reaches much past 1.3.
func (Julia) Defaults() Defaults {
	return Defaults{CX: 0, CY: 0, Width: 2.8, MaxIter: 256, Bailout: 256, Density: 8}
}

func (Julia) Params() []Param {
	return []Param{
		// -0.4 + 0.6i sits in a period-5 bulb and gives the familiar
		// "dendrite with spirals" that most people picture as a Julia set.
		{Name: "c (real)", Index: 0, Min: -2, Max: 2, Default: -0.4},
		{Name: "c (imag)", Index: 1, Min: -2, Max: 2, Default: 0.6},
	}
}

func (Julia) Companion() string { return "Mandelbrot" }

func (Julia) HasDeriv() bool { return true }

func (Julia) Iterate(x, y float64, o *Options) Result {
	cr, ci := o.P[0], o.P[1]
	t := track(o)
	deriv := o.Need&NeedDeriv != 0

	zr, zi := x, y
	zr2, zi2 := zr*zr, zi*zi
	// The derivative with respect to z₀ starts at 1 and follows
	// d/dz₀(z² + c) = 2·z·dz — no +1, since c does not depend on z₀.
	dr, di := 1.0, 0.0

	for n := 0; n < o.MaxIter; n++ {
		if zr2+zi2 > o.Bailout2 {
			return t.escaped(n, zr2+zi2, math.Ln2, dr, di, o)
		}
		if deriv {
			dr, di = 2*(zr*dr-zi*di), 2*(zr*di+zi*dr)
		}
		zi = 2*zr*zi + ci
		zr = zr2 - zi2 + cr
		zr2 = zr * zr
		zi2 = zi * zi

		if t.step(zr, zi) {
			return t.interior()
		}
	}
	return t.interior()
}
