package fractal

import (
	"math"
	"sync"

	"github.com/vmaciel/gofract/internal/formula"
)

// CustomName is the registry name of the user-defined type.
const CustomName = "Custom"

// Default formula sources, which reproduce the Mandelbrot set.
const (
	DefaultInit = "0"
	DefaultIter = "z*z + c"
)

func init() {
	f, err := NewCustom(DefaultInit, DefaultIter)
	if err != nil {
		panic("fractal: the default custom formula does not compile: " + err.Error())
	}
	Register(f)
}

// Custom is a fractal defined by expressions entered at run time rather than
// compiled in. Two expressions describe it: one for the starting value and one
// for the step.
//
// Everything else — periodicity detection, orbit traps, the smooth escape
// count — comes from the same machinery the built-in formulas use, so a typed
// formula is a first-class fractal rather than a limited preview of one.
type Custom struct {
	init, iter *formula.Program
}

// customKey identifies a compiled pair of expressions.
type customKey struct{ init, iter string }

var customCache sync.Map // customKey -> *Custom

// NewCustom compiles a formula, reusing an earlier compilation of the same
// source. The cache matters because State.Fractal is called on the drawing
// path — several times a frame — and re-parsing there would be wasteful.
// Empty sources fall back to the defaults.
func NewCustom(init, iter string) (*Custom, error) {
	if init == "" {
		init = DefaultInit
	}
	if iter == "" {
		iter = DefaultIter
	}
	key := customKey{init, iter}
	if v, ok := customCache.Load(key); ok {
		return v.(*Custom), nil
	}
	ip, err := formula.Compile(init)
	if err != nil {
		return nil, err
	}
	tp, err := formula.Compile(iter)
	if err != nil {
		return nil, err
	}
	c := &Custom{init: ip, iter: tp}
	customCache.Store(key, c)
	return c, nil
}

// Source returns the two expressions this formula was built from.
func (c *Custom) Source() (init, iter string) { return c.init.String(), c.iter.String() }

func (c *Custom) Name() string { return CustomName }

func (c *Custom) Defaults() Defaults {
	return Defaults{CX: 0, CY: 0, Width: 3.2, MaxIter: 256, Bailout: 256, Density: 8}
}

func (c *Custom) Params() []Param {
	return []Param{
		{Name: "p1 (real)", Index: 0, Min: -2, Max: 2, Default: 0},
		{Name: "p1 (imag)", Index: 1, Min: -2, Max: 2, Default: 0},
		{Name: "p2 (real)", Index: 2, Min: -2, Max: 2, Default: 0},
		{Name: "p2 (imag)", Index: 3, Min: -2, Max: 2, Default: 0},
	}
}

func (c *Custom) Companion() string { return "" }

// HasDeriv is false: the derivative of an arbitrary typed expression would have
// to be differentiated symbolically, which the language does not do.
func (c *Custom) HasDeriv() bool { return false }

func (c *Custom) Iterate(x, y float64, o *Options) Result {
	// One machine per pixel. Its scratch stack is zeroed on entry, which costs
	// a few nanoseconds against the microseconds the iteration loop will spend.
	var m formula.Machine
	v := formula.Vars{
		Pixel: complex(x, y),
		P1:    complex(o.P[0], o.P[1]),
		P2:    complex(o.P[2], o.P[3]),
	}
	v.Z = m.Eval(c.init, &v)

	t := track(o)
	var prevLog float64 // ln|z| of the previous iterate, for the power estimate
	for n := 0; n < o.MaxIter; n++ {
		zr, zi := real(v.Z), imag(v.Z)
		mod2 := zr*zr + zi*zi
		if math.IsNaN(mod2) || math.IsInf(mod2, 0) {
			// A formula can be written that overflows or divides itself into a
			// NaN. Treating that as an escape keeps the render finite and shows
			// the shape of the problem, rather than silently painting the whole
			// region as interior.
			return t.escaped(n, o.Bailout2, math.Ln2, 0, 0, o)
		}
		if mod2 > o.Bailout2 {
			return t.escaped(n, mod2, estimatePower(prevLog, 0.5*math.Log(mod2)), 0, 0, o)
		}
		if mod2 > 1 {
			prevLog = 0.5 * math.Log(mod2)
		}
		v.N = float64(n)
		v.Z = m.Eval(c.iter, &v)
		if t.step(real(v.Z), imag(v.Z)) {
			return t.interior()
		}
	}
	return t.interior()
}

// estimatePower recovers the exponent of the formula from the orbit itself,
// returning ln(p) for the smooth escape count.
//
// The built-in formulas know their own power, but a typed expression does not
// announce one: z*z+c doubles the logarithm of the modulus each step, z^3+c
// triples it, and sin(z)*c does something else entirely. Since an escaping
// orbit satisfies |z_n| ≈ |z_{n-1}|^p, the ratio of consecutive log-moduli is
// the exponent — measured rather than assumed, so smooth colouring stays
// band-free whatever was typed.
func estimatePower(prevLog, curLog float64) float64 {
	if prevLog <= 0 || curLog <= 0 {
		return math.Ln2
	}
	p := curLog / prevLog
	// Clamp to a sane range: a formula that grows slower than linearly, or
	// faster than any polynomial anyone writes, would otherwise produce a
	// nonsense normalisation.
	if p < 1.05 || p > 16 || math.IsNaN(p) {
		return math.Ln2
	}
	return math.Log(p)
}
