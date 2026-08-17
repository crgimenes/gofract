// Package fractal implements the pure mathematics of escape-time fractals.
//
// A fractal type is any value implementing Fractal. Kernels are deliberately
// free of rendering concerns: they receive a point of the complex plane and
// report how that point behaves under iteration. Everything about pixels,
// colours and goroutines lives elsewhere.
package fractal

import (
	"math"
	"sort"
)

// Need flags the optional per-orbit quantities a colouring mode requires.
// Kernels skip the extra arithmetic when a flag is clear, so the common case
// of plain escape-time colouring pays nothing for features it does not use.
type Need uint8

const (
	// NeedDeriv asks for the derivative of the orbit with respect to the
	// scanned coordinate, which distance estimation divides by.
	NeedDeriv Need = 1 << iota
	// NeedTrap asks for the orbit's closest approach to the trap shape.
	NeedTrap
)

// TrapKind selects an orbit-trap shape.
type TrapKind uint8

const (
	TrapNone TrapKind = iota
	TrapPoint
	TrapCross
	TrapCircle
	TrapSquare
)

// TrapNames are the trap shapes in menu order, indexed by TrapKind.
var TrapNames = []string{"none", "point", "cross", "circle", "square"}

// Trap is an orbit trap: a shape whose closest approach by the orbit becomes
// the colouring value. Where escape time only records how long a point took to
// leave, a trap exposes the shape of the orbit itself, which is why trap
// images show structure inside regions that escape time paints flat.
type Trap struct{ Kind TrapKind }

// Dist is the distance from z to the trap shape. It is called once per
// iteration, so it avoids math.Hypot: inside the bailout circle there is no
// overflow to guard against.
func (t Trap) Dist(zr, zi float64) float64 {
	switch t.Kind {
	case TrapPoint:
		return math.Sqrt(zr*zr + zi*zi)
	case TrapCross:
		// Distance to the nearer of the two axes.
		return math.Min(math.Abs(zr), math.Abs(zi))
	case TrapCircle:
		return math.Abs(math.Sqrt(zr*zr+zi*zi) - 1)
	case TrapSquare:
		return math.Max(math.Abs(zr), math.Abs(zi))
	}
	return 0
}

// Options bundles every tunable a kernel needs. It is handed to Iterate by
// pointer: Iterate runs once per sample, and copying the struct per sample
// would show up in profiles.
type Options struct {
	MaxIter int        // iteration ceiling
	Bailout float64    // escape radius
	P       [4]float64 // fractal-specific parameters, see Fractal.Params
	Need    Need       // optional orbit data the colouring wants
	Trap    Trap       // trap shape, used when Need has NeedTrap

	// Derived values, filled by Normalize so kernels never recompute them.
	Bailout2   float64 // Bailout²  (compared against |z|², avoiding a sqrt)
	LogBailout float64 // ln(Bailout), used by the smooth escape count
}

// Normalize clamps the user-facing fields and fills the derived ones.
func (o Options) Normalize() Options {
	if o.MaxIter < 1 {
		o.MaxIter = 1
	}
	// A bailout below 2 truncates the Mandelbrot set itself, and the smooth
	// escape count degrades badly for radii under ~4.
	if o.Bailout < 2 {
		o.Bailout = 2
	}
	o.Bailout2 = o.Bailout * o.Bailout
	o.LogBailout = math.Log(o.Bailout)
	return o
}

// Result describes the fate of a single point of the plane.
type Result struct {
	// N is the renormalised ("smooth") escape count. Unlike the raw integer
	// count it varies continuously across the image, which is what removes
	// colour banding. Convergent formulas such as Newton put their own
	// encoded colouring value here instead.
	N float64

	// Iter is the raw integer escape count.
	Iter int

	// Escaped reports that iteration ended with a usable value: the orbit
	// left the bailout circle, or — for convergent formulas — settled onto a
	// root. When false the point is "inside" and takes the interior colour,
	// and every other field is meaningless.
	Escaped bool

	Mod2   float64 // |z|² when iteration stopped
	Dr, Di float64 // derivative of the orbit, set when NeedDeriv was asked for
	Trap   float64 // closest approach to the trap, set when NeedTrap was asked
}

// Defaults describes how a formula wants to be presented when it is first
// selected. A zero Width, MaxIter, Bailout or Density means "no preference":
// the caller keeps whatever the user already had.
type Defaults struct {
	CX, CY  float64 // centre of the default view
	Width   float64 // width of the default view, in world units
	MaxIter int
	Bailout float64
	Density float64 // palette cycles per 256 iterations
}

// Param describes one fractal-specific knob, so the UI can build controls
// without knowing anything about individual fractal types.
type Param struct {
	Name    string
	Index   int // slot in Options.P
	Min     float64
	Max     float64
	Default float64
	Int     bool // the value is only meaningful at whole numbers
}

// Fractal is one iterated formula.
type Fractal interface {
	// Name is the unique, human-readable identifier of the type.
	Name() string

	// Defaults reports the view and presentation this type wants.
	Defaults() Defaults

	// Params describes the entries of Options.P that this type reads.
	Params() []Param

	// HasDeriv reports whether Iterate fills Result.Dr/Di when NeedDeriv is
	// set. Non-analytic formulas — anything folding the plane with an absolute
	// value or a conjugate — have no useful derivative, so distance estimation
	// does not apply and the UI hides it.
	HasDeriv() bool

	// Iterate runs the iteration for the point (x, y) of the plane. For
	// Mandelbrot-like types the point is the parameter c; for Julia-like
	// types it is the starting value z₀.
	Iterate(x, y float64, o *Options) Result

	// Companion names the type reached by the Mandelbrot <-> Julia switch,
	// or "" when this type has no counterpart. The switch feeds the clicked
	// point into the companion's first two parameters.
	Companion() string
}

var registry []Fractal

// Register adds a type to the global registry. Intended for use from init.
func Register(f Fractal) {
	registry = append(registry, f)
	sort.SliceStable(registry, func(i, j int) bool { return registry[i].Name() < registry[j].Name() })
}

// All returns every registered type, in stable order.
func All() []Fractal { return registry }

// Lookup finds a type by exact name. Use it when an unknown name has to be
// reported rather than papered over, such as when validating a parameter file.
func Lookup(name string) (Fractal, bool) {
	for _, f := range registry {
		if f.Name() == name {
			return f, true
		}
	}
	return nil, false
}

// ByName looks a type up, falling back to the first registered one so callers
// on the drawing path never have to handle a nil fractal.
func ByName(name string) Fractal {
	if f, ok := Lookup(name); ok {
		return f
	}
	if len(registry) > 0 {
		return registry[0]
	}
	return nil
}

// DefaultParams returns the parameter vector a type starts life with.
func DefaultParams(f Fractal) [4]float64 {
	var p [4]float64
	for _, d := range f.Params() {
		if d.Index >= 0 && d.Index < len(p) {
			p[d.Index] = d.Default
		}
	}
	return p
}

// smooth converts an integer escape count into a continuous one using the
// renormalised iteration count
//
//	mu = n + 1 - log_p( ln|z| / ln(bailout) )
//
// which interpolates across the "overshoot" of the final step. A point that
// barely crossed the bailout circle scores ≈ n+1; one that overshot by a full
// power of p scores ≈ n. logP must be ln(p) for the map z → z^p + c.
func smooth(n int, mod2, logP float64, o *Options) float64 {
	lz := 0.5 * math.Log(mod2) // ln|z| without the sqrt
	if lz <= 0 || o.LogBailout <= 0 {
		return float64(n)
	}
	return float64(n) + 1 - math.Log(lz/o.LogBailout)/logP
}

// smoothLinear is the same idea for maps whose modulus grows by a constant
// factor k each step rather than being raised to a power — Sierpinski's z → 2z,
// for instance. logK must be ln(k).
func smoothLinear(n int, mod2, logK float64, o *Options) float64 {
	lz := 0.5 * math.Log(mod2)
	if logK <= 0 {
		return float64(n)
	}
	return float64(n) + 1 - (lz-o.LogBailout)/logK
}

// Periodicity detection. Points inside the set converge onto an attracting
// cycle, so once the orbit stops moving we can stop iterating instead of
// grinding to MaxIter. The reference point is refreshed on an exponentially
// growing schedule, which catches cycles of any length without storing the
// whole orbit.
//
// The tolerance is deliberately tight: a false positive paints a solid blob
// where detail belongs, and that artefact is far worse than the lost speed.
const (
	periodTol2  = 1e-30 // squared distance, i.e. |dz| < 1e-15
	periodFirst = 16    // iterations before the first checkpoint
)

// orbit carries the per-point bookkeeping that every kernel shares:
// periodicity detection and orbit-trap accumulation. Keeping it here means a
// new formula only has to write its own recurrence.
type orbit struct {
	trap    Trap
	trapOn  bool
	trapMin float64

	pr, pi      float64
	check, next int
}

// track starts the bookkeeping for one point.
func track(o *Options) orbit {
	return orbit{
		trap:    o.Trap,
		trapOn:  o.Need&NeedTrap != 0 && o.Trap.Kind != TrapNone,
		trapMin: math.Inf(1),
		check:   periodFirst,
		next:    periodFirst,
	}
}

// step records one orbit point and reports whether the orbit has settled onto
// an attracting cycle, meaning the point is interior.
func (t *orbit) step(zr, zi float64) bool {
	if t.trapOn {
		if d := t.trap.Dist(zr, zi); d < t.trapMin {
			t.trapMin = d
		}
	}
	dr, di := zr-t.pr, zi-t.pi
	if dr*dr+di*di < periodTol2 {
		return true
	}
	if t.check--; t.check == 0 {
		t.pr, t.pi = zr, zi
		t.next *= 2
		t.check = t.next
	}
	return false
}

// escaped builds the Result for a point that left the bailout circle on
// iteration n with |z|² = mod2, under a formula of power p (logP = ln p).
func (t *orbit) escaped(n int, mod2, logP, dr, di float64, o *Options) Result {
	return Result{
		N:       smooth(n, mod2, logP, o),
		Iter:    n,
		Escaped: true,
		Mod2:    mod2,
		Dr:      dr,
		Di:      di,
		Trap:    t.trapMin,
	}
}

// interior builds the Result for a point that never escaped.
func (t *orbit) interior() Result { return Result{Trap: t.trapMin} }
