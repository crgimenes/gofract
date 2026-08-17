// Package formula compiles user-entered complex expressions into a small stack
// machine, so a fractal can be described at run time instead of being compiled
// in.
//
// The language is deliberately close to Fractint's .frm dialect, because that
// is the notation decades of published formulas are written in: the same
// function names, the same component-wise abs, the same `pixel` for the point
// being scanned.
//
// Expressions compile to a flat instruction list over a fixed-size stack rather
// than to a tree of closures. The evaluator runs once per iteration per pixel —
// tens of billions of times over a deep render — so it has to be free of
// allocation and of pointer chasing.
package formula

import (
	"fmt"
	"math"
	"math/cmplx"
	"strconv"
	"strings"
	"unicode"
)

// Vars are the values an expression can read.
type Vars struct {
	Z      complex128 // the current orbit value
	Pixel  complex128 // the point of the plane being scanned
	N      float64    // the iteration number
	P1, P2 complex128 // user parameters
}

// maxStack bounds the evaluation stack. Expressions deeper than this are
// rejected at compile time, so evaluation never needs to check.
const maxStack = 32

type opcode uint8

const (
	opConst opcode = iota
	opZ
	opPixel
	opP1
	opP2
	opN

	opAdd
	opSub
	opMul
	opDiv
	opPow
	opNeg
	opIPow // raise to a small constant integer power, held in arg

	opFunc
)

// fn identifies a single-argument function.
type fn uint8

const (
	fnSin fn = iota
	fnCos
	fnTan
	fnSinh
	fnCosh
	fnTanh
	fnExp
	fnLog
	fnSqrt
	fnSqr
	fnCube
	fnRecip
	fnAbs
	fnCAbs
	fnConj
	fnReal
	fnImag
	fnFlip
	fnIdent
)

var funcs = map[string]fn{
	"sin": fnSin, "cos": fnCos, "tan": fnTan,
	"sinh": fnSinh, "cosh": fnCosh, "tanh": fnTanh,
	"exp": fnExp, "log": fnLog, "sqrt": fnSqrt,
	"sqr": fnSqr, "cube": fnCube, "recip": fnRecip,
	"abs": fnAbs, "cabs": fnCAbs, "conj": fnConj,
	"real": fnReal, "imag": fnImag, "flip": fnFlip, "ident": fnIdent,
}

// FuncNames lists the available functions, for documentation and the UI.
var FuncNames = func() []string {
	names := make([]string, 0, len(funcs))
	for k := range funcs {
		names = append(names, k)
	}
	sortStrings(names)
	return names
}()

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

type instr struct {
	op  opcode
	arg int32 // constant index, function id, or integer exponent
}

// Program is a compiled expression.
type Program struct {
	src    string
	code   []instr
	consts []complex128
}

// String returns the source the program was compiled from.
func (p *Program) String() string { return p.src }

// Machine is the scratch space a Program runs in.
//
// It exists so the stack is not re-created per call. A local array would be
// zeroed on entry to every evaluation — a kilobyte of memset against a handful
// of arithmetic instructions, which measured at more than half the run time.
// Programs are immutable and shared; each goroutine keeps its own Machine.
type Machine struct {
	st [maxStack]complex128
}

// Eval runs the program. It allocates nothing and cannot panic: the compiler
// has already proved the stack depth is within bounds.
func (m *Machine) Eval(p *Program, v *Vars) complex128 {
	st := &m.st
	sp := 0
	for _, in := range p.code {
		switch in.op {
		case opConst:
			st[sp] = p.consts[in.arg]
			sp++
		case opZ:
			st[sp] = v.Z
			sp++
		case opPixel:
			st[sp] = v.Pixel
			sp++
		case opP1:
			st[sp] = v.P1
			sp++
		case opP2:
			st[sp] = v.P2
			sp++
		case opN:
			st[sp] = complex(v.N, 0)
			sp++
		case opAdd:
			sp--
			st[sp-1] += st[sp]
		case opSub:
			sp--
			st[sp-1] -= st[sp]
		case opMul:
			sp--
			st[sp-1] *= st[sp]
		case opDiv:
			sp--
			st[sp-1] = safeDiv(st[sp-1], st[sp])
		case opPow:
			sp--
			st[sp-1] = cmplx.Pow(st[sp-1], st[sp])
		case opNeg:
			st[sp-1] = -st[sp-1]
		case opIPow:
			st[sp-1] = ipow(st[sp-1], int(in.arg))
		case opFunc:
			st[sp-1] = apply(fn(in.arg), st[sp-1])
		}
	}
	if sp == 0 {
		return 0
	}
	return st[0]
}

// safeDiv keeps a division by zero from turning the whole orbit into NaN, which
// would look like a hang rather than a mistake in the formula.
func safeDiv(a, b complex128) complex128 {
	if b == 0 {
		return 0
	}
	return a / b
}

// ipow raises to a small integer power by repeated multiplication. z^2 is by
// far the most common thing anyone writes, and cmplx.Pow would compute it with
// a log and an exp.
func ipow(z complex128, n int) complex128 {
	neg := n < 0
	if neg {
		n = -n
	}
	out := complex(1, 0)
	base := z
	for n > 0 {
		if n&1 == 1 {
			out *= base
		}
		base *= base
		n >>= 1
	}
	if neg {
		return safeDiv(1, out)
	}
	return out
}

func apply(f fn, z complex128) complex128 {
	switch f {
	case fnSin:
		return cmplx.Sin(z)
	case fnCos:
		return cmplx.Cos(z)
	case fnTan:
		return cmplx.Tan(z)
	case fnSinh:
		return cmplx.Sinh(z)
	case fnCosh:
		return cmplx.Cosh(z)
	case fnTanh:
		return cmplx.Tanh(z)
	case fnExp:
		return cmplx.Exp(z)
	case fnLog:
		if z == 0 {
			return 0
		}
		return cmplx.Log(z)
	case fnSqrt:
		return cmplx.Sqrt(z)
	case fnSqr:
		return z * z
	case fnCube:
		return z * z * z
	case fnRecip:
		return safeDiv(1, z)
	case fnAbs:
		// Fractint's abs is component-wise, which is what folds the plane and
		// gives the Burning Ship its rigging. The modulus is cabs.
		return complex(math.Abs(real(z)), math.Abs(imag(z)))
	case fnCAbs:
		return complex(cmplx.Abs(z), 0)
	case fnConj:
		return cmplx.Conj(z)
	case fnReal:
		return complex(real(z), 0)
	case fnImag:
		return complex(imag(z), 0)
	case fnFlip:
		return complex(imag(z), real(z))
	}
	return z
}

// --- lexer ------------------------------------------------------------

type tokKind uint8

const (
	tkEOF tokKind = iota
	tkNumber
	tkIdent
	tkOp
	tkLParen
	tkRParen
)

type token struct {
	kind tokKind
	text string
	num  float64
	pos  int
}

func lex(s string) ([]token, error) {
	var out []token
	i := 0
	for i < len(s) {
		ch := rune(s[i])
		switch {
		case unicode.IsSpace(ch):
			i++
		case ch >= '0' && ch <= '9' || ch == '.':
			j := i
			for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == '.') {
				j++
			}
			// An exponent, but only when it is really one: "e" alone is the
			// constant, and "1e" followed by a letter is a syntax error rather
			// than a silent truncation.
			if j < len(s) && (s[j] == 'e' || s[j] == 'E') {
				k := j + 1
				if k < len(s) && (s[k] == '+' || s[k] == '-') {
					k++
				}
				if k < len(s) && s[k] >= '0' && s[k] <= '9' {
					for k < len(s) && s[k] >= '0' && s[k] <= '9' {
						k++
					}
					j = k
				}
			}
			v, err := strconv.ParseFloat(s[i:j], 64)
			if err != nil {
				return nil, fmt.Errorf("bad number %q at %d", s[i:j], i+1)
			}
			out = append(out, token{kind: tkNumber, num: v, text: s[i:j], pos: i})
			i = j
		case unicode.IsLetter(ch) || ch == '_':
			j := i
			for j < len(s) && (unicode.IsLetter(rune(s[j])) || unicode.IsDigit(rune(s[j])) || s[j] == '_') {
				j++
			}
			out = append(out, token{kind: tkIdent, text: strings.ToLower(s[i:j]), pos: i})
			i = j
		case strings.ContainsRune("+-*/^", ch):
			out = append(out, token{kind: tkOp, text: string(ch), pos: i})
			i++
		case ch == '(':
			out = append(out, token{kind: tkLParen, pos: i})
			i++
		case ch == ')':
			out = append(out, token{kind: tkRParen, pos: i})
			i++
		default:
			return nil, fmt.Errorf("unexpected character %q at %d", ch, i+1)
		}
	}
	out = append(out, token{kind: tkEOF, pos: len(s)})
	return out, nil
}

// --- parser -----------------------------------------------------------

type parser struct {
	toks []token
	i    int
	p    *Program
	// depth tracks the stack height as code is emitted, so the compiler can
	// prove Eval never overflows.
	depth, maxDepth int
}

func (ps *parser) peek() token { return ps.toks[ps.i] }
func (ps *parser) next() token { t := ps.toks[ps.i]; ps.i++; return t }
func (ps *parser) emit(in instr, push, pop int) {
	ps.p.code = append(ps.p.code, in)
	ps.depth += push - pop
	ps.maxDepth = max(ps.maxDepth, ps.depth)
}

func (ps *parser) constant(v complex128) {
	for i, c := range ps.p.consts {
		if c == v {
			ps.emit(instr{op: opConst, arg: int32(i)}, 1, 0)
			return
		}
	}
	ps.p.consts = append(ps.p.consts, v)
	ps.emit(instr{op: opConst, arg: int32(len(ps.p.consts) - 1)}, 1, 0)
}

// precedence of the binary operators. Higher binds tighter.
func precOf(op string) (int, bool) {
	switch op {
	case "+", "-":
		return 1, true
	case "*", "/":
		return 2, true
	case "^":
		return 4, true
	}
	return 0, false
}

// expr parses with precedence climbing.
func (ps *parser) expr(minPrec int) error {
	if err := ps.unary(); err != nil {
		return err
	}
	for {
		t := ps.peek()
		if t.kind != tkOp {
			return nil
		}
		prec, ok := precOf(t.text)
		if !ok || prec < minPrec {
			return nil
		}
		ps.next()
		// ^ is right-associative, so a^b^c parses as a^(b^c).
		nextMin := prec + 1
		if t.text == "^" {
			nextMin = prec
		}
		// A constant integer exponent becomes repeated multiplication.
		if t.text == "^" {
			if n, ok := ps.smallIntExponent(); ok {
				ps.emit(instr{op: opIPow, arg: int32(n)}, 0, 0)
				continue
			}
		}
		if err := ps.expr(nextMin); err != nil {
			return err
		}
		switch t.text {
		case "+":
			ps.emit(instr{op: opAdd}, 0, 1)
		case "-":
			ps.emit(instr{op: opSub}, 0, 1)
		case "*":
			ps.emit(instr{op: opMul}, 0, 1)
		case "/":
			ps.emit(instr{op: opDiv}, 0, 1)
		case "^":
			ps.emit(instr{op: opPow}, 0, 1)
		}
	}
}

// smallIntExponent consumes a bare whole-number exponent if that is what comes
// next, so that z^3 compiles to two multiplies instead of a complex pow.
func (ps *parser) smallIntExponent() (int, bool) {
	save := ps.i
	neg := false
	if t := ps.peek(); t.kind == tkOp && (t.text == "-" || t.text == "+") {
		neg = t.text == "-"
		ps.next()
	}
	t := ps.peek()
	if t.kind != tkNumber {
		ps.i = save
		return 0, false
	}
	n := t.num
	if n != math.Trunc(n) || math.Abs(n) > 64 {
		ps.i = save
		return 0, false
	}
	// A following operator that binds tighter than ^ would change the meaning.
	if nt := ps.toks[ps.i+1]; nt.kind == tkOp && nt.text == "^" {
		ps.i = save
		return 0, false
	}
	ps.next()
	v := int(n)
	if neg {
		v = -v
	}
	return v, true
}

func (ps *parser) unary() error {
	t := ps.peek()
	if t.kind == tkOp && (t.text == "-" || t.text == "+") {
		ps.next()
		if err := ps.expr(3); err != nil {
			return err
		}
		if t.text == "-" {
			ps.emit(instr{op: opNeg}, 0, 0)
		}
		return nil
	}
	return ps.atom()
}

func (ps *parser) atom() error {
	t := ps.next()
	switch t.kind {
	case tkNumber:
		ps.constant(complex(t.num, 0))
		return nil

	case tkIdent:
		switch t.text {
		case "z":
			ps.emit(instr{op: opZ}, 1, 0)
			return nil
		case "pixel", "c":
			ps.emit(instr{op: opPixel}, 1, 0)
			return nil
		case "p1":
			ps.emit(instr{op: opP1}, 1, 0)
			return nil
		case "p2":
			ps.emit(instr{op: opP2}, 1, 0)
			return nil
		case "n", "iter":
			ps.emit(instr{op: opN}, 1, 0)
			return nil
		case "i":
			ps.constant(complex(0, 1))
			return nil
		case "pi":
			ps.constant(complex(math.Pi, 0))
			return nil
		case "e":
			ps.constant(complex(math.E, 0))
			return nil
		}
		f, ok := funcs[t.text]
		if !ok {
			return fmt.Errorf("unknown name %q at %d", t.text, t.pos+1)
		}
		if ps.peek().kind != tkLParen {
			return fmt.Errorf("%s needs a bracketed argument, at %d", t.text, t.pos+1)
		}
		ps.next()
		if err := ps.expr(1); err != nil {
			return err
		}
		if ps.peek().kind != tkRParen {
			return fmt.Errorf("missing ) for %s, at %d", t.text, ps.peek().pos+1)
		}
		ps.next()
		ps.emit(instr{op: opFunc, arg: int32(f)}, 0, 0)
		return nil

	case tkLParen:
		if err := ps.expr(1); err != nil {
			return err
		}
		if ps.peek().kind != tkRParen {
			return fmt.Errorf("missing ) at %d", ps.peek().pos+1)
		}
		ps.next()
		return nil

	case tkEOF:
		return fmt.Errorf("expression ends early")
	}
	return fmt.Errorf("unexpected token at %d", t.pos+1)
}

// Compile turns source text into a program.
func Compile(src string) (*Program, error) {
	if strings.TrimSpace(src) == "" {
		return nil, fmt.Errorf("formula: the expression is empty")
	}
	toks, err := lex(src)
	if err != nil {
		return nil, fmt.Errorf("formula: %w", err)
	}
	ps := &parser{toks: toks, p: &Program{src: src}}
	if err := ps.expr(1); err != nil {
		return nil, fmt.Errorf("formula: %w", err)
	}
	if ps.peek().kind != tkEOF {
		return nil, fmt.Errorf("formula: unexpected %q at %d",
			tokenText(ps.peek()), ps.peek().pos+1)
	}
	if ps.maxDepth > maxStack {
		return nil, fmt.Errorf("formula: expression nests too deeply (%d, limit %d)",
			ps.maxDepth, maxStack)
	}
	if ps.depth != 1 {
		return nil, fmt.Errorf("formula: the expression does not reduce to a single value")
	}
	return ps.p, nil
}

func tokenText(t token) string {
	switch t.kind {
	case tkRParen:
		return ")"
	case tkLParen:
		return "("
	case tkEOF:
		return "end of expression"
	}
	return t.text
}
