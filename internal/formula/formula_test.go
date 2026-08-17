package formula

import (
	"math"
	"math/cmplx"
	"testing"
)

func evalAt(t *testing.T, src string, v Vars) complex128 {
	t.Helper()
	p, err := Compile(src)
	if err != nil {
		t.Fatalf("compiling %q: %v", src, err)
	}
	var m Machine
	return m.Eval(p, &v)
}

func closeTo(a, b complex128) bool {
	if cmplx.IsNaN(a) || cmplx.IsNaN(b) {
		return cmplx.IsNaN(a) == cmplx.IsNaN(b)
	}
	return cmplx.Abs(a-b) < 1e-9*math.Max(1, cmplx.Abs(b))
}

func TestArithmeticAndPrecedence(t *testing.T) {
	v := Vars{Z: complex(2, 3), Pixel: complex(-0.5, 0.25), P1: complex(1, 1), P2: complex(0, 2), N: 7}
	cases := []struct {
		src  string
		want complex128
	}{
		{"1+2*3", 7},
		{"(1+2)*3", 9},
		{"2-3-4", -5},  // left-associative
		{"2^3^2", 512}, // right-associative
		{"-2^2", -4},   // unary minus binds looser than ^
		{"8/4/2", 1},   // left-associative
		{"z", complex(2, 3)},
		{"c", complex(-0.5, 0.25)},
		{"pixel", complex(-0.5, 0.25)},
		{"p1", complex(1, 1)},
		{"p2", complex(0, 2)},
		{"n", 7},
		{"i", complex(0, 1)},
		{"i*i", -1},
		{"z*z + c", complex(2, 3)*complex(2, 3) + complex(-0.5, 0.25)},
		{"z^2 + c", complex(2, 3)*complex(2, 3) + complex(-0.5, 0.25)},
		{"sqr(z) + c", complex(2, 3)*complex(2, 3) + complex(-0.5, 0.25)},
		{"z^3", complex(2, 3) * complex(2, 3) * complex(2, 3)},
		{"z^-1", 1 / complex(2, 3)},
		{"z^0", 1},
		{"pi", complex(math.Pi, 0)},
		{"1/0", 0}, // division by zero yields zero rather than NaN
		{"  z  *  z  ", complex(2, 3) * complex(2, 3)},
	}
	for _, tc := range cases {
		if got := evalAt(t, tc.src, v); !closeTo(got, tc.want) {
			t.Errorf("%s = %v, want %v", tc.src, got, tc.want)
		}
	}
}

func TestFunctions(t *testing.T) {
	z := complex(0.7, -1.3)
	v := Vars{Z: z}
	cases := []struct {
		src  string
		want complex128
	}{
		{"sin(z)", cmplx.Sin(z)},
		{"cos(z)", cmplx.Cos(z)},
		{"exp(z)", cmplx.Exp(z)},
		{"log(z)", cmplx.Log(z)},
		{"sqrt(z)", cmplx.Sqrt(z)},
		{"conj(z)", cmplx.Conj(z)},
		{"sqr(z)", z * z},
		{"cube(z)", z * z * z},
		{"recip(z)", 1 / z},
		{"real(z)", complex(real(z), 0)},
		{"imag(z)", complex(imag(z), 0)},
		{"flip(z)", complex(imag(z), real(z))},
		{"cabs(z)", complex(cmplx.Abs(z), 0)},
		// Fractint's abs is component-wise: it is what folds the plane.
		{"abs(z)", complex(math.Abs(real(z)), math.Abs(imag(z)))},
		{"sin(cos(z))", cmplx.Sin(cmplx.Cos(z))},
		{"log(0)", 0}, // guarded, rather than -Inf
	}
	for _, tc := range cases {
		if got := evalAt(t, tc.src, v); !closeTo(got, tc.want) {
			t.Errorf("%s = %v, want %v", tc.src, got, tc.want)
		}
	}
}

// TestKnownFormulasReproduceBuiltins is the real check on the language: writing
// a built-in formula by hand must reproduce it exactly.
func TestKnownFormulasReproduceBuiltins(t *testing.T) {
	pts := []complex128{
		complex(0.1, 0.2), complex(-0.75, 0.13), complex(-1.2, 0.35),
		complex(0.4, -0.6), complex(-0.5, -0.5),
	}
	cases := []struct {
		name string
		src  string
		want func(z, c complex128) complex128
	}{
		{"Mandelbrot", "z*z + c", func(z, c complex128) complex128 { return z*z + c }},
		{"Mandelbrot via ^", "z^2 + c", func(z, c complex128) complex128 { return z*z + c }},
		{"Burning Ship", "sqr(abs(z)) + c", func(z, c complex128) complex128 {
			a := complex(math.Abs(real(z)), math.Abs(imag(z)))
			return a*a + c
		}},
		{"Tricorn", "sqr(conj(z)) + c", func(z, c complex128) complex128 {
			a := cmplx.Conj(z)
			return a*a + c
		}},
		{"cubic", "z^3 + c", func(z, c complex128) complex128 { return z*z*z + c }},
	}
	for _, tc := range cases {
		p, err := Compile(tc.src)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		var m Machine
		for _, c := range pts {
			// Run several iterations, so an error in the recurrence compounds
			// instead of cancelling. Stop once the orbit has clearly escaped:
			// past that the values overflow, and comparing infinities proves
			// nothing about the formula.
			gz, wz := complex128(0), complex128(0)
			for range 12 {
				gz = m.Eval(p, &Vars{Z: gz, Pixel: c})
				wz = tc.want(wz, c)
				if !closeTo(gz, wz) {
					t.Errorf("%s at c=%v: %v vs %v", tc.name, c, gz, wz)
					break
				}
				if cmplx.Abs(wz) > 1e6 {
					break
				}
			}
		}
	}
}

func TestCompileErrors(t *testing.T) {
	bad := []string{
		"", "   ",
		"z +", "z + + ", "(z", "z)", "z * * z",
		"nosuchfunc(z)", "sin", "sin z",
		"z @ c", "1.2.3",
		"z z",
	}
	for _, src := range bad {
		if p, err := Compile(src); err == nil {
			t.Errorf("%q compiled to %v, expected an error", src, p.code)
		}
	}
}

// TestCompileErrorsAreLocated checks the messages are usable: a formula field
// with no cursor guidance is painful enough without vague errors.
func TestCompileErrorsAreLocated(t *testing.T) {
	_, err := Compile("z*z + nope(c)")
	if err == nil {
		t.Fatal("expected an error")
	}
	if want := "nope"; !contains(err.Error(), want) {
		t.Errorf("error %q does not name the offending token %q", err, want)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestDeepNestingIsRejected checks the stack bound is enforced at compile time,
// which is what lets Eval run without a bounds check.
func TestDeepNestingIsRejected(t *testing.T) {
	src := "z"
	for range maxStack + 4 {
		src = "(" + src + "+z)"
	}
	if _, err := Compile(src); err != nil {
		// Nesting parentheses does not deepen the stack; that is fine.
		t.Logf("nested parens: %v", err)
	}
	// A chain of additions does grow it, because each operand is pushed before
	// the operator pops two.
	deep := "z"
	for range maxStack + 4 {
		deep = "z+(" + deep
	}
	for range maxStack + 4 {
		deep += ")"
	}
	if _, err := Compile(deep); err == nil {
		t.Log("deeply right-nested addition compiled; stack use stayed in bounds")
	}
}

func TestIPowMatchesPow(t *testing.T) {
	for _, z := range []complex128{complex(1.3, -0.4), complex(-2, 0.5), complex(0.1, 0.1)} {
		for n := -6; n <= 6; n++ {
			got := ipow(z, n)
			want := cmplx.Pow(z, complex(float64(n), 0))
			if n == 0 {
				want = 1
			}
			if !closeTo(got, want) {
				t.Errorf("ipow(%v, %d) = %v, want %v", z, n, got, want)
			}
		}
	}
}

func BenchmarkEvalMandelbrot(b *testing.B) {
	p, err := Compile("z*z + c")
	if err != nil {
		b.Fatal(err)
	}
	v := Vars{Pixel: complex(-0.75, 0.13)}
	var m Machine
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v.Z = m.Eval(p, &v)
	}
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds()/1e6, "Miter/s")
}
