package renderer

import (
	"image/color"
	"runtime"
	"testing"
	"time"

	"github.com/vmaciel/gofract/internal/fractal"
	"github.com/vmaciel/gofract/internal/palette"
	"github.com/vmaciel/gofract/internal/params"
)

func request(w, h, iter int) Request {
	f := fractal.ByName("Mandelbrot")
	return Request{
		W:    w,
		H:    h,
		SS:   1,
		View: params.HomeView(f, w, h),
		Frac: f,
		Opts: fractal.Options{MaxIter: iter, Bailout: 256}.Normalize(),
		Color: ColorSpec{
			Ramp:    palette.Presets[0],
			Density: 8,
			Mode:    params.ColorSmooth,
			Inside:  color.RGBA{0, 0, 0, 255},
		},
		// The equality tests below compare against a direct render, which has
		// no coarse pass for guessing to consult; keep them on the exact path.
		Guess: params.GuessOff,
	}
}

// TestRenderImageProducesPixels checks the synchronous export path end to
// end: the home view must contain both interior (black) and exterior pixels.
func TestRenderImageProducesPixels(t *testing.T) {
	img := RenderImage(request(160, 120, 256), 4)
	if img.Bounds().Dx() != 160 || img.Bounds().Dy() != 120 {
		t.Fatalf("unexpected bounds %v", img.Bounds())
	}
	var black, colored int
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i]|img.Pix[i+1]|img.Pix[i+2] == 0 {
			black++
		} else {
			colored++
		}
		if img.Pix[i+3] != 255 {
			t.Fatalf("pixel %d is not opaque", i/4)
		}
	}
	if black == 0 {
		t.Error("no interior pixels: the set itself is missing")
	}
	if colored == 0 {
		t.Error("no exterior pixels: nothing escaped")
	}
}

// TestProgressiveRenderConverges checks that the async path reaches a
// complete frame and that it matches a direct synchronous render, i.e. the
// coarse passes leave no stale blocks behind.
func TestProgressiveRenderConverges(t *testing.T) {
	req := request(96, 72, 128)
	r := New()
	r.Start(req)

	deadline := time.Now().Add(20 * time.Second)
	var done bool
	var pix []byte
	for time.Now().Before(deadline) && !done {
		r.WithFrame(func(f Frame) {
			if f.Img != nil && f.Done {
				done = true
				pix = append([]byte(nil), f.Img.Pix...)
			}
		})
		time.Sleep(5 * time.Millisecond)
	}
	if !done {
		t.Fatal("progressive render did not finish")
	}

	want := RenderImage(req, runtime.NumCPU())
	for i := range want.Pix {
		if pix[i] != want.Pix[i] {
			t.Fatalf("progressive result differs from direct render at byte %d: %d != %d",
				i, pix[i], want.Pix[i])
		}
	}
}

// TestRecolorKeepsGeometry verifies that changing the palette repaints the
// published frame without re-iterating.
func TestRecolorKeepsGeometry(t *testing.T) {
	req := request(64, 48, 96)
	r := New()
	r.Start(req)
	waitDone(t, r)

	var before []byte
	r.WithFrame(func(f Frame) { before = append([]byte(nil), f.Img.Pix...) })

	cs := req.Color
	cs.Ramp = palette.ByName("Rainbow")
	r.Recolor(cs)

	var after []byte
	r.WithFrame(func(f Frame) { after = append([]byte(nil), f.Img.Pix...) })
	if string(before) == string(after) {
		t.Error("Recolor did not change the frame")
	}
}

// TestRapidRestartsSettleOnTheLastRequest simulates a pan or wheel gesture,
// which restarts the renderer every tick. Superseded jobs must not publish
// over the newest one, and the final frame must match the final request.
func TestRapidRestartsSettleOnTheLastRequest(t *testing.T) {
	r := New()
	var last Request
	for i := 0; i < 40; i++ {
		req := request(120, 90, 400)
		req.View = req.View.Pan(float64(-i), 0)
		last = req
		r.Start(req)
		time.Sleep(time.Millisecond)
	}
	r.Start(last)
	waitDone(t, r)

	var gotView params.View
	var pix []byte
	r.WithFrame(func(f Frame) {
		gotView = f.View
		pix = append([]byte(nil), f.Img.Pix...)
	})
	if !gotView.Equal(last.View) {
		t.Fatalf("settled on view %+v, want %+v", gotView, last.View)
	}
	want := RenderImage(last, runtime.NumCPU())
	if string(pix) != string(want.Pix) {
		t.Error("the settled frame does not match the last request")
	}
}

// TestCancelledRenderLeavesFrameUsable checks that cancelling mid-flight does
// not corrupt or nil out whatever was already published.
func TestCancelledRenderLeavesFrameUsable(t *testing.T) {
	r := New()
	r.Start(request(64, 48, 64))
	waitDone(t, r)

	// Start something expensive, then immediately cancel it.
	r.Start(request(64, 48, 200000))
	r.Cancel()

	ok := false
	r.WithFrame(func(f Frame) { ok = f.Img != nil && f.Img.Bounds().Dx() == 64 })
	if !ok {
		t.Error("the previously published frame was lost")
	}
}

func waitDone(t *testing.T, r *Renderer) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		ok := false
		r.WithFrame(func(f Frame) { ok = f.Img != nil && f.Done })
		if ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("render did not finish")
}

// BenchmarkMandelbrot measures raw throughput in pixels per second at a fixed
// iteration ceiling, which is the number the README quotes.
func BenchmarkMandelbrot(b *testing.B) {
	const w, h, iter = 800, 600, 256
	req := request(w, h, iter)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		RenderImage(req, runtime.NumCPU())
	}
	b.StopTimer()
	px := float64(w*h) * float64(b.N)
	b.ReportMetric(px/b.Elapsed().Seconds()/1e6, "Mpx/s")
}

// TestDeepViewUsesPerturbation checks the renderer switches paths on its own,
// and reports which one it took.
func TestDeepViewUsesPerturbation(t *testing.T) {
	cx, err := params.ParseReal("-0.743643887037158704752191506114774")
	if err != nil {
		t.Fatal(err)
	}
	cy, err := params.ParseReal("0.131825904205311970493132056385139")
	if err != nil {
		t.Fatal(err)
	}

	shallow := request(80, 60, 400)
	if shallow.View.Deep() {
		t.Fatal("the home view should not be deep")
	}
	if _, deep := newSampler(shallow); deep {
		t.Error("the home view took the perturbation path")
	}

	// A view this deep needs a matching iteration ceiling: at 1e-20 the orbits
	// that make the structure run to many thousands of steps, and a low ceiling
	// would paint the whole patch as interior no matter how exact the sampling.
	req := request(80, 60, 12000)
	req.View = params.View{CX: cx, CY: cy, Scale: 1e-20 / 60}
	req.View.CX = req.View.CX.Round(req.View.Prec())
	req.View.CY = req.View.CY.Round(req.View.Prec())
	if !req.View.Deep() {
		t.Fatal("a 1e-20-wide view should be deep")
	}
	smp, deep := newSampler(req)
	if !deep {
		t.Fatal("a deep view did not take the perturbation path")
	}
	if smp.pert == nil || smp.ref.Len() < 2 {
		t.Fatalf("no reference orbit: %d", smp.ref.Len())
	}

	// The whole point: the image must not be one flat block.
	img := RenderImage(req, runtime.NumCPU())
	seen := map[[3]byte]bool{}
	for i := 0; i+3 < len(img.Pix); i += 4 {
		seen[[3]byte{img.Pix[i], img.Pix[i+1], img.Pix[i+2]}] = true
	}
	if len(seen) < 16 {
		t.Errorf("a deep render produced only %d distinct colours; it has pixelated", len(seen))
	}
	t.Logf("reference orbit %d long, %d distinct colours at 1e-20 wide", smp.ref.Len(), len(seen))
}

// TestReliefLightingIsARecolour checks that lighting rides on the cached values
// rather than needing the fractal iterated again.
func TestReliefLightingIsARecolour(t *testing.T) {
	req := request(96, 72, 300)
	r := New()
	r.Start(req)
	waitDone(t, r)

	var before []byte
	r.WithFrame(func(f Frame) { before = append([]byte(nil), f.Img.Pix...) })

	cs := req.Color
	cs.Light = true
	cs.LightAz, cs.LightEl, cs.LightDepth, cs.LightAmb = 135, 45, 4, 0.3
	r.Recolor(cs)

	var after []byte
	r.WithFrame(func(f Frame) { after = append([]byte(nil), f.Img.Pix...) })
	if string(before) == string(after) {
		t.Fatal("switching lighting on did not change the image")
	}

	// Shading only scales brightness, so no pixel may get brighter.
	brighter := 0
	for i := 0; i+3 < len(before); i += 4 {
		for k := range 3 {
			// Compare as ints: before+1 would wrap at 255 and call almost
			// every pixel brighter.
			if int(after[i+k]) > int(before[i+k])+1 {
				brighter++
			}
		}
	}
	if brighter > 0 {
		t.Errorf("%d channels got brighter; relief shading should only attenuate", brighter)
	}
}

// TestHeightGridMatchesData checks that the resampled height grid actually
// tracks the render, rather than returning some placeholder: it must report
// no data before anything has completed, and afterwards must contain more
// than one distinct value for a view with real structure in it.
func TestHeightGridMatchesData(t *testing.T) {
	r := New()
	if _, ok := r.HeightGrid(10, 10); ok {
		t.Fatal("HeightGrid succeeded before any render completed")
	}

	req := request(120, 90, 400)
	r.Start(req)
	waitDone(t, r)

	grid, ok := r.HeightGrid(40, 30)
	if !ok {
		t.Fatal("HeightGrid failed after a completed render")
	}
	if len(grid) != 40*30 {
		t.Fatalf("grid has %d entries, want %d", len(grid), 40*30)
	}
	seen := map[float32]bool{}
	for _, v := range grid {
		seen[v] = true
	}
	if len(seen) < 3 {
		t.Errorf("only %d distinct values in a 40x30 grid over the home view; "+
			"HeightGrid does not appear to be sampling real structure", len(seen))
	}

	// Degenerate sizes must not panic.
	if _, ok := r.HeightGrid(0, 10); ok {
		t.Error("HeightGrid(0, 10) should fail rather than return something")
	}
}
