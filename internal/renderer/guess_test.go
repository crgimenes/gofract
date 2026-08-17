package renderer

import (
	"image"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/vmaciel/gofract/internal/fractal"
	"github.com/vmaciel/gofract/internal/params"
)

// progressive runs the async path to completion and returns the pixels plus the
// fraction of samples solid guessing skipped.
func progressive(t *testing.T, req Request) ([]byte, float64) {
	t.Helper()
	r := New()
	r.Start(req)
	waitDone(t, r)
	var pix []byte
	var guessed float64
	r.WithFrame(func(f Frame) {
		pix = append([]byte(nil), f.Img.Pix...)
		guessed = f.Guessed
	})
	return pix, guessed
}

func differing(a, b []byte) int {
	n := 0
	for i := 0; i+3 < len(a) && i+3 < len(b); i += 4 {
		if a[i] != b[i] || a[i+1] != b[i+1] || a[i+2] != b[i+2] {
			n++
		}
	}
	return n
}

// TestGuessOffIsExact pins the guarantee that turning guessing off makes the
// progressive path bit-identical to a direct render.
func TestGuessOffIsExact(t *testing.T) {
	req := request(200, 150, 500)
	req.Guess = params.GuessOff
	got, guessed := progressive(t, req)
	if guessed != 0 {
		t.Errorf("guessing skipped %.2f%% of samples with GuessOff", guessed*100)
	}
	want := RenderImage(req, runtime.NumCPU())
	if n := differing(got, want.Pix); n != 0 {
		t.Errorf("%d pixels differ from the exact render", n)
	}
}

// TestGuessOnAgreesWithExact checks the claim made in the docs and the UI:
// bit-for-bit corner agreement only fires over genuinely flat regions, so the
// "safe" setting should reproduce the exact image essentially everywhere.
//
// It is a measurement, not a theorem — a filament thinner than a cell could in
// principle thread between four identical corners — so the bound is empirical
// and the actual figure is logged.
func TestGuessOnAgreesWithExact(t *testing.T) {
	// Two views with very different interior/exterior balance.
	views := []struct {
		name string
		req  Request
	}{
		{"home", request(240, 180, 600)},
		{"seahorse", deepRequest(240, 180, 3000)},
	}
	// Guessing must also stay honest about how much it actually skipped.
	for _, v := range views {
		exact := v.req
		exact.Guess = params.GuessOff
		want := RenderImage(exact, runtime.NumCPU())

		safe := v.req
		safe.Guess = params.GuessOn
		got, guessed := progressive(t, safe)

		n := differing(got, want.Pix)
		total := v.req.W * v.req.H
		frac := float64(n) / float64(total)
		t.Logf("%s: %.1f%% of samples guessed, %d/%d pixels differ (%.4f%%)",
			v.name, guessed*100, n, total, frac*100)
		// Measured on this machine: ~0.2% at the home view, ~0.4% deep. The
		// bound leaves room for machine-to-machine drift while still catching a
		// real regression.
		if frac > 0.01 {
			t.Errorf("%s: %.4f%% of pixels differ, more than 'safe' should cost", v.name, frac*100)
		}
	}
}

// deepRequest frames the seahorse valley, where nearly every pixel runs a long
// orbit — the case solid guessing is supposed to help with.
func deepRequest(w, h, iter int) Request {
	req := request(w, h, iter)
	req.View = params.NewView(-0.7436438870371587, 0.13182590420531197, 1e-6/float64(h))
	return req
}

// TestSampleValueModes covers the reduction from an orbit result to the single
// stored float, including the fallbacks for missing data.
func TestSampleValueModes(t *testing.T) {
	interior := fractal.Result{}
	for _, m := range params.ColorModes {
		if v := sampleValue(interior, m, 1e-3); v != insideMarker {
			t.Errorf("%s: interior point stored %v, want %v", m, v, insideMarker)
		}
	}

	esc := fractal.Result{N: 12.5, Iter: 12, Escaped: true, Mod2: 70000, Dr: 3, Di: 4, Trap: 0.25}
	if v := sampleValue(esc, params.ColorSmooth, 1e-3); v != 12.5 {
		t.Errorf("smooth: %v", v)
	}
	if v := sampleValue(esc, params.ColorIteration, 1e-3); v != 12 {
		t.Errorf("iteration: %v", v)
	}
	if v := sampleValue(esc, params.ColorTrap, 1e-3); v != 0.25 {
		t.Errorf("trap: %v", v)
	}
	// The distance estimate is normalised by the sample size and clamped, so a
	// coarse pixel saturates while a fine one resolves detail.
	coarse := sampleValue(esc, params.ColorDistance, 1e9)
	fine := sampleValue(esc, params.ColorDistance, 1e-9)
	if !(coarse < fine) {
		t.Errorf("distance estimate did not scale with pixel size: %v vs %v", coarse, fine)
	}
	if fine != 1 {
		t.Errorf("distance estimate should clamp at 1, got %v", fine)
	}

	// A formula with no derivative must not divide by zero.
	noDeriv := fractal.Result{N: 5, Iter: 5, Escaped: true, Mod2: 70000}
	if v := sampleValue(noDeriv, params.ColorDistance, 1e-3); v != 0 {
		t.Errorf("missing derivative gave %v, want 0", v)
	}
	// An orbit that never approached the trap reports an infinite distance.
	noTrap := fractal.Result{Escaped: true, Mod2: 70000, Trap: mathInf()}
	if v := sampleValue(noTrap, params.ColorTrap, 1e-3); v != 1 {
		t.Errorf("untouched trap gave %v, want 1", v)
	}
}

func mathInf() float64 {
	var zero float64
	return 1 / zero
}

// BenchmarkColorize measures the cost of a re-colour, which is what colour
// cycling pays every frame.
func BenchmarkColorize(b *testing.B) {
	const w, h = 1920, 1080
	req := request(w, h, 256)
	data := make([]float32, w*h)
	for i := range data {
		data[i] = float32(i % 512)
	}
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		colorize(out, data, w, h, 1, req.Color, runtime.NumCPU(), nil)
	}
	b.StopTimer()
	b.ReportMetric(float64(b.Elapsed().Milliseconds())/float64(b.N), "ms/frame")
}

// BenchmarkGuessing compares the three strategies on an interior-heavy view.
func BenchmarkGuessing(b *testing.B) {
	for _, g := range params.Guesses {
		b.Run(string(g), func(b *testing.B) {
			req := deepRequest(480, 360, 4000)
			req.Guess = g
			// Measure the compute passes directly. Going through Start would
			// mean polling for completion, and the poll loop's mutex traffic
			// would swamp what is being measured.
			sw, sh := req.sampleSize()
			stop := &atomicBool{}
			r := New()
			smp, _ := newSampler(req)
			for i := 0; i < b.N; i++ {
				data := make([]float32, sw*sh)
				prev := 0
				for _, step := range passes {
					r.computePass(req, &smp, data, sw, sh, step, prev, stop)
					prev = step
				}
			}
		})
	}
}

// atomicBool aliases the flag type computePass expects.
type atomicBool = atomic.Bool
