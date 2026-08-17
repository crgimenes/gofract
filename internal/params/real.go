package params

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strings"
)

// Real is an immutable arbitrary-precision real number, used for the coordinates
// of the view centre.
//
// float64 carries about 15 significant decimal digits, which runs out at roughly
// 10¹⁵ magnification: past that, neighbouring pixels round to the same
// coordinate and the image breaks into flat blocks. The centre therefore has to
// grow as the zoom deepens. Nothing else does — see View.Offset.
//
// Immutability is the point of the wrapper. Views are copied by value into the
// zoom history, and *big.Float is a pointer: one shared mutable value would let
// a later pan silently rewrite the history entries recorded before it.
type Real struct {
	// f is nil for zero, and is never mutated after construction.
	f *big.Float
}

// RealFrom builds a Real from a float64.
func RealFrom(v float64) Real {
	if v == 0 {
		return Real{}
	}
	return Real{new(big.Float).SetPrec(64).SetFloat64(v)}
}

// ParseReal reads a decimal string, giving the result enough mantissa to hold
// every digit written down.
func ParseReal(s string) (Real, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return Real{}, nil
	}
	prec := decimalPrec(len(s))
	f, _, err := big.ParseFloat(s, 10, prec, big.ToNearestEven)
	if err != nil {
		return Real{}, fmt.Errorf("params: parsing %q as a coordinate: %w", s, err)
	}
	return Real{f}, nil
}

// decimalPrec converts a count of decimal digits into mantissa bits, with room
// to spare.
func decimalPrec(digits int) uint {
	bits := float64(digits)/math.Log10(2) + 64
	return uint(math.Min(bits, maxPrec))
}

// maxPrec caps the mantissa. It is far past any practical zoom and exists only
// so that a corrupt file cannot ask for gigabytes of coordinate.
const maxPrec = 1 << 16

// Float returns the value as a float64, which for a deep-zoom coordinate is
// only good enough for display.
func (r Real) Float() float64 {
	if r.f == nil {
		return 0
	}
	v, _ := r.f.Float64()
	return v
}

// Add returns r + delta at the given precision.
//
// Every navigation gesture reduces to this. A pan or a zoom moves the centre by
// an offset spanning at most the width of the image, and that offset always fits
// in float64 however deep the zoom — which is the same observation perturbation
// rendering is built on.
func (r Real) Add(delta float64, prec uint) Real {
	if prec < 64 {
		prec = 64
	}
	out := new(big.Float).SetPrec(prec)
	if r.f != nil {
		out.Set(r.f)
	}
	if delta != 0 {
		out.Add(out, new(big.Float).SetPrec(prec).SetFloat64(delta))
	}
	return Real{out}
}

// Big returns a fresh big.Float at the requested precision. The copy protects
// the invariant that a Real's value never changes.
func (r Real) Big(prec uint) *big.Float {
	if prec < 64 {
		prec = 64
	}
	out := new(big.Float).SetPrec(prec)
	if r.f != nil {
		out.Set(r.f)
	}
	return out
}

// Round returns the value re-rounded to the given precision.
func (r Real) Round(prec uint) Real {
	if r.f == nil {
		return Real{}
	}
	return Real{r.Big(prec)}
}

// Prec reports the mantissa precision the value is carried at.
func (r Real) Prec() uint {
	if r.f == nil {
		return 0
	}
	return r.f.Prec()
}

// Text renders the value with enough digits to reconstruct it exactly.
func (r Real) Text() string {
	if r.f == nil {
		return "0"
	}
	return r.f.Text('g', int(float64(r.f.Prec())*math.Log10(2))+3)
}

// Short renders the value for display, to at most the given significant digits.
func (r Real) Short(digits int) string {
	if r.f == nil {
		return "0"
	}
	return r.f.Text('g', digits)
}

// Equal compares by value.
func (r Real) Equal(o Real) bool {
	switch {
	case r.f == nil && o.f == nil:
		return true
	case r.f == nil:
		return o.f.Sign() == 0
	case o.f == nil:
		return r.f.Sign() == 0
	}
	return r.f.Cmp(o.f) == 0
}

// Finite reports whether the value is usable as a coordinate.
func (r Real) Finite() bool { return r.f == nil || !r.f.IsInf() }

// MarshalJSON writes the coordinate as a decimal string. A JSON number would
// be read back through float64 by most tools and lose every digit past the
// fifteenth, which defeats the purpose.
func (r Real) MarshalJSON() ([]byte, error) { return json.Marshal(r.Text()) }

// UnmarshalJSON accepts either a string or a bare JSON number, so a
// hand-written parameter file can say -0.75 without quotes.
func (r *Real) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		*r = Real{}
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		v, err := ParseReal(str)
		if err != nil {
			return err
		}
		*r = v
		return nil
	}
	v, err := ParseReal(s)
	if err != nil {
		return err
	}
	*r = v
	return nil
}
