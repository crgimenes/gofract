package fractal

import (
	"math"
	"math/big"
)

func init() { Register(Mandelbrot{}) }

// Mandelbrot is the set of points c for which the orbit of
//
//	z₀ = 0,  z_{n+1} = z_n² + c
//
// stays bounded.
type Mandelbrot struct{}

func (Mandelbrot) Name() string { return "Mandelbrot" }

// Defaults frame the whole set: it lives inside |c| < 2, centred near -0.6.
func (Mandelbrot) Defaults() Defaults {
	return Defaults{CX: -0.6, CY: 0, Width: 3.2, MaxIter: 256, Bailout: 256, Density: 8}
}

func (Mandelbrot) Params() []Param { return nil }

func (Mandelbrot) Companion() string { return "Julia" }

func (Mandelbrot) HasDeriv() bool { return true }

func (Mandelbrot) Iterate(cr, ci float64, o *Options) Result {
	// The main cardioid and the period-2 bulb are known in closed form, and
	// together they cover most of the black area at low zoom. Testing them
	// first avoids MaxIter wasted iterations per pixel there.
	//
	// Cardioid: c is inside when |1 - sqrt(1-4c)| < 1, which rearranges into
	// the q-form below with q = |c - 1/4|².
	q := (cr-0.25)*(cr-0.25) + ci*ci
	if q*(q+(cr-0.25)) <= 0.25*ci*ci {
		return Result{}
	}
	// Period-2 bulb: the disc of radius 1/4 centred at -1.
	if (cr+1)*(cr+1)+ci*ci <= 0.0625 {
		return Result{}
	}

	t := track(o)
	deriv := o.Need&NeedDeriv != 0
	var zr, zi, zr2, zi2 float64
	// The derivative with respect to c, for distance estimation. It starts at
	// dz₀/dc = 0 and follows d/dc(z² + c) = 2·z·dz + 1.
	var dr, di float64

	for n := 0; n < o.MaxIter; n++ {
		if zr2+zi2 > o.Bailout2 {
			return t.escaped(n, zr2+zi2, math.Ln2, dr, di, o)
		}
		if deriv {
			dr, di = 2*(zr*dr-zi*di)+1, 2*(zr*di+zi*dr)
		}
		// (a+bi)² + c, keeping the squares around for the next escape test.
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

// Reference computes the high-precision orbit of the reference point, which is
// the one place in the renderer where arbitrary-precision arithmetic runs. It is
// O(MaxIter) big.Float multiplies per *image*, against O(W·H·MaxIter) float64
// multiplies for the pixels, so it is not where the time goes.
func (Mandelbrot) Reference(cx, cy *big.Float, o *Options) Orbit {
	prec := max(cx.Prec(), cy.Prec(), 64)
	nf := func() *big.Float { return new(big.Float).SetPrec(prec) }
	zr, zi := nf(), nf()
	zr2, zi2, t := nf(), nf(), nf()

	orb := Orbit{
		Zr: make([]float64, 0, o.MaxIter+2),
		Zi: make([]float64, 0, o.MaxIter+2),
	}
	for n := 0; ; n++ {
		fr, _ := zr.Float64()
		fi, _ := zi.Float64()
		orb.Zr = append(orb.Zr, fr)
		orb.Zi = append(orb.Zi, fi)
		// The orbit has to reach as far as any pixel might: to the iteration
		// ceiling, or until it leaves the render's own bailout radius. Stopping
		// at a tighter radius — |Z| > 2, say, which is where the orbit is
		// already committed to diverging — would leave Perturb rebasing while
		// δz is still infinitesimal, and that silently erases it.
		if n >= o.MaxIter || fr*fr+fi*fi > o.Bailout2 {
			break
		}
		// z ← z² + c, with the cross term reused rather than doubled by hand.
		zr2.Mul(zr, zr)
		zi2.Mul(zi, zi)
		t.Mul(zr, zi)
		zi.Add(t, t)
		zi.Add(zi, cy)
		zr.Sub(zr2, zi2)
		zr.Add(zr, cx)
	}
	return orb
}

// Perturb iterates the difference from the reference orbit.
//
// The delicate part is not the recurrence but what happens when δz grows to the
// size of Z: the sum Z + δz then loses most of its significant digits, and the
// classic symptom is a "glitch" — a blob of visibly wrong pixels.
//
// The fix used here is rebasing. When |z| drops below |δz|, the difference has
// stopped being a small correction to anything, so the orbit is restarted: the
// reference index goes back to 0 and δz is set to the full value z. Because
// Z₀ = 0 for this family, Z₀ + z is exactly z — the substitution is not an
// approximation, and the iteration count carries straight on. One reference
// orbit therefore serves the whole image with no glitches and no second
// reference, which is why Julia sets cannot use the same trick: their Z₀ is the
// starting point, not zero.
func (Mandelbrot) Perturb(dcr, dci float64, ref Orbit, o *Options) Result {
	n := len(ref.Zr)
	if n < 2 {
		return Result{}
	}
	t := track(o)
	deriv := o.Need&NeedDeriv != 0

	var dzr, dzi float64 // δz
	var dr, di float64   // dz/dc of the full orbit, for distance estimation
	m := 0               // index into the reference orbit
	// z is the full value Z_m + δz. Both start at zero.
	zr, zi := ref.Zr[0], ref.Zi[0]
	mod2 := zr*zr + zi*zi

	// The loop tests z₀ … z_{MaxIter-1} and reports the index that escaped,
	// exactly as Iterate does. Advancing before testing instead would run one
	// iteration further than the direct path and classify points differently at
	// the iteration ceiling.
	for iter := 0; iter < o.MaxIter; iter++ {
		if mod2 > o.Bailout2 {
			return t.escaped(iter, mod2, math.Ln2, dr, di, o)
		}
		if deriv {
			// The full orbit still obeys d/dc(z² + c) = 2·z·dz + 1, and z is
			// exactly what the reference and the difference add up to.
			dr, di = 2*(zr*dr-zi*di)+1, 2*(zr*di+zi*dr)
		}
		// δz ← 2·Z_m·δz + δz² + δc, grouped as (2·Z_m + δz)·δz + δc to save a
		// complex multiply.
		ar := 2*ref.Zr[m] + dzr
		ai := 2*ref.Zi[m] + dzi
		dzr, dzi = ar*dzr-ai*dzi+dcr, ar*dzi+ai*dzr+dci
		m++

		zr = ref.Zr[m] + dzr
		zi = ref.Zi[m] + dzi
		mod2 = zr*zr + zi*zi
		if t.step(zr, zi) {
			return t.interior()
		}
		// Rebase when the difference has outgrown the value it is a difference
		// from, or when the reference orbit runs out.
		if mod2 < dzr*dzr+dzi*dzi || m == n-1 {
			dzr, dzi = zr, zi
			m = 0
		}
	}
	return t.interior()
}
