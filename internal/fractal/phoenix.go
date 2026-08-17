package fractal

import "math"

func init() { Register(Phoenix{}) }

// Phoenix iterates a *second-order* recurrence — the only formula here that
// remembers where it came from:
//
//	z_{n+1} = z_n² + p + q·z_{n-1}
//
// with real p and q. The feedback term drags the orbit sideways, which stretches
// the usual Julia filaments into the long swept wings the fractal is named for.
//
// The plane is scanned over z₀, and — as Fractint specifies — the remembered
// value starts at the pixel too, z₋₁ = z₀. That detail matters: starting it at
// zero instead breaks the figure into two disconnected clusters rather than the
// single winged form.
type Phoenix struct{}

func (Phoenix) Name() string { return "Phoenix" }

func (Phoenix) Defaults() Defaults {
	return Defaults{CX: 0, CY: 0, Width: 2.2, MaxIter: 256, Bailout: 256, Density: 8}
}

func (Phoenix) Params() []Param {
	return []Param{
		// Fractint's classic phoenix parameters.
		{Name: "p (real)", Index: 0, Min: -2, Max: 2, Default: 0.56667},
		{Name: "q (feedback)", Index: 1, Min: -2, Max: 2, Default: -0.5},
	}
}

func (Phoenix) Companion() string { return "" }

func (Phoenix) HasDeriv() bool { return false }

func (Phoenix) Iterate(x, y float64, o *Options) Result {
	p, q := o.P[0], o.P[1]
	t := track(o)

	zr, zi := x, y
	// The previous orbit point, which the feedback term reads. It starts at the
	// pixel, not at zero.
	wr, wi := x, y

	for n := 0; n < o.MaxIter; n++ {
		zr2, zi2 := zr*zr, zi*zi
		if zr2+zi2 > o.Bailout2 {
			return t.escaped(n, zr2+zi2, math.Ln2, 0, 0, o)
		}
		nr := zr2 - zi2 + p + q*wr
		ni := 2*zr*zi + q*wi
		wr, wi = zr, zi
		zr, zi = nr, ni

		if t.step(zr, zi) {
			return t.interior()
		}
	}
	return t.interior()
}
