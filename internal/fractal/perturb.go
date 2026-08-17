package fractal

import "math/big"

// Orbit is a reference orbit for perturbation rendering: the iterates of one
// high-precision point, stored at float64 precision.
//
// Only the *coordinate* of the reference point needs the extra bits. Its
// iterates are order 1, so once they have been computed float64 holds them
// perfectly well — and that asymmetry is the whole reason perturbation lifts the
// zoom limit.
type Orbit struct {
	Zr, Zi []float64
}

// Len is the number of stored iterates, including Z₀.
func (o Orbit) Len() int { return len(o.Zr) }

// Perturber is implemented by formulas that can be rendered by perturbation,
// which is what carries the zoom past float64's ceiling of about 10¹⁵.
//
// The idea is to split every point into a shared high-precision reference orbit
// plus a small per-pixel difference. Writing z = Z + δz and c = C + δc, the
// Mandelbrot recurrence becomes
//
//	δz_{n+1} = 2·Z_n·δz_n + δz_n² + δc
//
// where δz and δc are the size of the *image* rather than of the coordinates.
// They therefore stay comfortably inside float64 however deep the zoom goes,
// and the expensive high-precision work happens once per image instead of once
// per pixel.
//
// Not every formula qualifies. Reference orbits are only useful if the orbit
// can be restarted from zero when the difference outgrows it (see the rebasing
// note in Mandelbrot.Perturb), which requires Z₀ = 0.
type Perturber interface {
	Fractal

	// Reference iterates the reference point in high precision and returns the
	// orbit at float64 precision. It is called once per image.
	//
	// The orbit must run to o.MaxIter, or until it leaves o.Bailout — not some
	// tighter radius. Truncating it early forces Perturb to rebase while δz is
	// still tiny, and rebasing then *destroys* the perturbation: setting δz to
	// Z + δz rounds the small part away, and every pixel collapses onto the
	// reference.
	Reference(cx, cy *big.Float, o *Options) Orbit

	// Perturb iterates the difference from the reference for the point at
	// offset (dcr, dci) from the reference coordinate.
	Perturb(dcr, dci float64, ref Orbit, o *Options) Result
}

// AsPerturber reports whether a formula can be rendered by perturbation.
func AsPerturber(f Fractal) (Perturber, bool) {
	p, ok := f.(Perturber)
	return p, ok
}
