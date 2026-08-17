package fractal

import "math"

func init() { Register(Newton{}) }

// Newton is Newton's method applied to zⁿ - 1 = 0:
//
//	z_{n+1} = z_n - (z_nⁿ - 1) / (n·z_nⁿ⁻¹)
//
// Unlike the escape-time formulas, almost every starting point *converges* —
// onto one of the n roots of unity. The fractal is the boundary between the
// basins of attraction, which is a Julia set of the Newton map, and the
// natural way to colour it is by which root won.
type Newton struct{}

func (Newton) Name() string { return "Newton" }

func (Newton) Defaults() Defaults {
	// A density of 1 makes one palette sweep cover all the basins, which is
	// the classic presentation; see the encoding in Iterate.
	return Defaults{CX: 0, CY: 0, Width: 3.0, MaxIter: 128, Bailout: 256, Density: 1}
}

func (Newton) Params() []Param {
	return []Param{
		{Name: "power (n)", Index: 0, Min: 2, Max: 8, Default: 3, Int: true},
	}
}

func (Newton) Companion() string { return "" }

// HasDeriv is false: Newton converges rather than escaping, so the exterior
// distance estimate has no meaning here.
func (Newton) HasDeriv() bool { return false }

// newtonTol2 is the squared step length below which the iteration counts as
// converged. Newton's method doubles its correct digits each step, so any
// threshold in this neighbourhood costs at most one extra iteration.
const newtonTol2 = 1e-20

// newtonBands scales the encoded colouring value so that, at a density of 1,
// one sweep of the palette spans every basin exactly once.
const newtonBands = 256.0

func (Newton) Iterate(x, y float64, o *Options) Result {
	n := int(math.Round(o.P[0]))
	if n < 2 {
		n = 2
	} else if n > 8 {
		n = 8
	}
	t := track(o)
	zr, zi := x, y

	for i := 0; i < o.MaxIter; i++ {
		// p = zⁿ⁻¹ and q = zⁿ, by repeated multiplication: n is small, so this
		// beats a pair of pow/atan2 calls.
		pr, pi := 1.0, 0.0
		for k := 0; k < n-1; k++ {
			pr, pi = pr*zr-pi*zi, pr*zi+pi*zr
		}
		qr, qi := pr*zr-pi*zi, pr*zi+pi*zr

		// den = n·zⁿ⁻¹. It vanishes only at the origin, where Newton's method
		// has nothing to head towards.
		dr, di := float64(n)*pr, float64(n)*pi
		den := dr*dr + di*di
		if den < 1e-300 {
			return t.interior()
		}
		// num/den with num = zⁿ - 1.
		nr, ni := qr-1, qi
		sr := (nr*dr + ni*di) / den
		si := (ni*dr - nr*di) / den

		zr -= sr
		zi -= si
		t.step(zr, zi) // for the orbit trap; convergence is tested separately

		if sr*sr+si*si < newtonTol2 {
			// Which root of unity did we land on? The roots sit at angles
			// 2πk/n, so the nearest one follows straight from the argument.
			k := int(math.Round(math.Atan2(zi, zr) / (2 * math.Pi) * float64(n)))
			k = ((k % n) + n) % n
			// Shade within the basin by how long convergence took, rising
			// quickly at first so the fine filigree near the boundary — where
			// convergence is slowest — gets the far end of the band.
			shade := float64(i) / (float64(i) + 8)
			return Result{
				N:       newtonBands * (float64(k) + shade) / float64(n),
				Iter:    i,
				Escaped: true,
				Mod2:    zr*zr + zi*zi,
				Trap:    t.trapMin,
			}
		}
	}
	return t.interior()
}
