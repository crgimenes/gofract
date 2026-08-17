// Package renderer turns a params.State into pixels, off the UI thread.
//
// The design has three properties that matter for an explorer:
//
//   - Rendering never blocks the event loop. A job runs on a pool of
//     goroutines and publishes intermediate results; the UI polls.
//   - Rendering is progressive. Coarse passes land in milliseconds so the
//     window always shows something, and each pass refines the last.
//   - The colouring values are kept, not just the pixels. Re-colouring a
//     finished image (new palette, shifted ramp, colour cycling) is a LUT
//     pass over cached data, with no iteration at all.
package renderer

import (
	"image"
	"image/color"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vmaciel/gofract/internal/fractal"
	"github.com/vmaciel/gofract/internal/palette"
	"github.com/vmaciel/gofract/internal/params"
)

// insideMarker is stored in the sample buffer for points that never escaped.
// Colouring values are always >= 0, so a negative value is unambiguous.
const insideMarker = float32(-1)

// passes are the sample strides of the progressive refinement, coarse first.
// Each pass computes only the samples the previous ones skipped and paints
// stride×stride blocks, so the image is complete (if chunky) after pass one.
var passes = []int{16, 8, 4, 2, 1}

// ColorSpec is the colouring half of a request. It is separated from the
// geometry so that palette changes can be applied without recomputing.
//
// Mode is here as well as in the sample buffer's derivation because the
// value-to-palette scaling depends on it; changing Mode still requires a full
// re-render, since the modes need different data out of the iteration loop.
type ColorSpec struct {
	Ramp    *palette.Palette
	Density float64
	Offset  float64
	Mode    params.ColorMode
	LogMap  bool
	Inside  color.RGBA

	Light      bool
	LightAz    float64
	LightEl    float64
	LightDepth float64
	LightAmb   float64
}

// SpecFrom builds a ColorSpec from serialised colouring parameters. The
// formula is needed because not every mode applies to every formula.
func SpecFrom(c params.Coloring, f fractal.Fractal) ColorSpec {
	return ColorSpec{
		Ramp:    c.Ramp(),
		Density: c.Density,
		Offset:  c.Offset,
		Mode:    c.EffectiveMode(f),
		LogMap:  c.LogMap,
		Inside:  color.RGBA{c.Inside.R, c.Inside.G, c.Inside.B, 255},

		Light:      c.Light,
		LightAz:    c.LightAz,
		LightEl:    c.LightEl,
		LightDepth: c.LightDepth,
		LightAmb:   c.LightAmb,
	}
}

// sampler turns a pixel's offset from the view centre into an orbit result.
//
// Which path it takes is the deep-zoom decision. Shallow views iterate absolute
// float64 coordinates directly; once the pixel size drops below what float64 can
// resolve, the same offsets become perturbations of a shared high-precision
// reference orbit instead. Everything above this level is identical either way,
// because in both cases what a pixel contributes is an *offset* — see
// params.View.Offset.
type sampler struct {
	frac   fractal.Fractal
	opts   fractal.Options
	cx, cy float64 // centre, for the direct path

	pert fractal.Perturber // non-nil when perturbation is in use
	ref  fractal.Orbit
}

func (s *sampler) at(dx, dy float64) fractal.Result {
	if s.pert != nil {
		return s.pert.Perturb(dx, dy, s.ref, &s.opts)
	}
	return s.frac.Iterate(s.cx+dx, s.cy+dy, &s.opts)
}

// newSampler decides the path and does the once-per-image work.
func newSampler(req Request) (sampler, bool) {
	s := sampler{frac: req.Frac, opts: req.Opts}
	s.cx, s.cy = req.View.CX.Float(), req.View.CY.Float()

	if !req.View.Deep() {
		return s, false
	}
	p, ok := fractal.AsPerturber(req.Frac)
	if !ok {
		// Too deep for float64 and no perturbation available. Render anyway —
		// the result pixelates into blocks, which is honest about what is
		// happening — and let the UI say so.
		return s, false
	}
	prec := req.View.Prec()
	s.pert = p
	s.ref = p.Reference(req.View.CX.Big(prec), req.View.CY.Big(prec), &req.Opts)
	if s.ref.Len() < 2 {
		s.pert = nil
		return s, false
	}
	return s, true
}

// Request is one complete rendering task.
type Request struct {
	W, H  int // output size in pixels
	SS    int // supersampling factor: SS×SS samples per pixel
	View  params.View
	Frac  fractal.Fractal
	Opts  fractal.Options
	Color ColorSpec
	Guess params.Guess
}

func (r Request) sampleSize() (int, int) { return r.W * r.SS, r.H * r.SS }

// Frame is a published result. The View is carried along so the UI can tell
// how stale the image is and reproject it during a gesture.
type Frame struct {
	Img      *image.RGBA
	View     params.View
	Version  uint64
	Done     bool
	Progress float64
	Elapsed  time.Duration
	// Guessed is the fraction of samples the final pass skipped by solid
	// guessing, so the UI can show what the setting is actually buying.
	Guessed float64
	// Perturbed reports that the frame was rendered by perturbation rather than
	// by iterating float64 coordinates.
	Perturbed bool
}

// Renderer owns the worker pool and the published frame.
type Renderer struct {
	mu    sync.Mutex
	frame Frame
	// data caches the colouring values of the last completed frame so that
	// re-colouring costs a LUT pass instead of a re-render. The slice is
	// replaced, never mutated, so a reader may use it after unlocking as
	// long as dataGen has not moved on.
	data    []float32
	dataW   int
	dataH   int
	dataSS  int
	dataGen uint64
	// live is the colour spec a running job should use from its next
	// publish onwards, letting palette edits show up mid-render.
	live ColorSpec

	jobMu sync.Mutex
	stop  *atomic.Bool // cancels the job currently in flight
	seq   atomic.Uint64

	// recolorBuf is scratch space for Recolor, reused across calls.
	recolorMu   sync.Mutex
	recolorBuf  *image.RGBA
	recolorCost atomic.Int64 // nanoseconds of the last Recolor

	version atomic.Uint64
	workers int
}

// New creates a renderer sized to the machine.
func New() *Renderer {
	n := runtime.NumCPU()
	if n < 1 {
		n = 1
	}
	return &Renderer{workers: n}
}

// Start cancels any job in flight and begins a new one. It returns
// immediately; results arrive through WithFrame.
//
// Start deliberately does not wait for the outgoing job's workers to drain:
// a worker only tests for cancellation between rows, and blocking the event
// loop on that would stutter panning. Instead each job carries a sequence
// number and a stale job's results are dropped at publish time.
func (r *Renderer) Start(req Request) {
	seq := r.seq.Add(1)

	r.jobMu.Lock()
	if r.stop != nil {
		r.stop.Store(true)
	}
	stop := &atomic.Bool{}
	r.stop = stop
	r.jobMu.Unlock()

	r.mu.Lock()
	r.live = req.Color
	r.dataGen++ // invalidate the re-colouring cache
	r.data = nil
	r.mu.Unlock()

	go r.run(req, stop, seq)
}

// Cancel stops the job in flight, if any.
func (r *Renderer) Cancel() {
	r.jobMu.Lock()
	if r.stop != nil {
		r.stop.Store(true)
	}
	r.jobMu.Unlock()
}

// WithFrame calls fn with the published frame while holding the lock. fn must
// not block; copy out whatever it needs. f.Img is nil until the first pass of
// the first job completes.
func (r *Renderer) WithFrame(fn func(f Frame)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn(r.frame)
}

// Recolor re-maps the cached colouring values through a new colour spec. When a
// render is in flight the cache is stale, so the spec is handed to the job
// instead and takes effect at its next publish.
func (r *Renderer) Recolor(cs ColorSpec) {
	r.mu.Lock()
	r.live = cs
	data := r.data
	gen := r.dataGen
	w, h, ss := r.dataW, r.dataH, r.dataSS
	r.mu.Unlock()

	if data == nil {
		return // a render is in flight; it will pick up r.live instead
	}
	// Colour into a scratch image, then copy it in under the lock: the UI
	// must never observe a half-written published buffer. The scratch buffer
	// is reused because dragging a colour slider calls this every tick.
	r.recolorMu.Lock()
	defer r.recolorMu.Unlock()
	rect := image.Rect(0, 0, w, h)
	if r.recolorBuf == nil || r.recolorBuf.Rect != rect {
		r.recolorBuf = image.NewRGBA(rect)
	}
	out := r.recolorBuf
	start := time.Now()
	colorize(out, data, w, h, ss, cs, r.workers, nil)
	r.recolorCost.Store(int64(time.Since(start)))

	r.mu.Lock()
	if r.dataGen == gen && r.frame.Img != nil && r.frame.Img.Rect == out.Rect {
		copy(r.frame.Img.Pix, out.Pix)
		r.frame.Version = r.version.Add(1)
	}
	r.mu.Unlock()
}

// RecolorCost reports how long the last Recolor took. Colour cycling uses it to
// decide how often it can afford to repaint.
func (r *Renderer) RecolorCost() time.Duration {
	return time.Duration(r.recolorCost.Load())
}

// HeightGrid resamples the last completed frame's colouring values to an
// nx×ny grid, read by the 3D landscape and globe views as elevation. It
// returns false if no frame has completed yet.
//
// Resampling is nearest-neighbour, not an average: averaging an escaped value
// together with the -1 interior marker would invent a height partway between
// "outside the set" and "inside" that neither region actually has, right along
// every coastline in the image.
func (r *Renderer) HeightGrid(nx, ny int) ([]float32, bool) {
	if nx <= 0 || ny <= 0 {
		return nil, false
	}
	r.mu.Lock()
	data := r.data
	w, h, ss := r.dataW, r.dataH, r.dataSS
	r.mu.Unlock()
	if data == nil {
		return nil, false
	}

	sw, sh := w*ss, h*ss
	out := make([]float32, nx*ny)
	for j := 0; j < ny; j++ {
		sy := min(j*sh/ny, sh-1)
		for i := 0; i < nx; i++ {
			sx := min(i*sw/nx, sw-1)
			out[j*nx+i] = data[sy*sw+sx]
		}
	}
	return out, true
}

// run executes one job: coarse-to-fine passes, publishing after each.
func (r *Renderer) run(req Request, stop *atomic.Bool, seq uint64) {
	if req.W <= 0 || req.H <= 0 || req.Frac == nil {
		return
	}
	if req.SS < 1 {
		req.SS = 1
	}
	start := time.Now()
	// The reference orbit is per-image work, so it happens here rather than in
	// a pass. At deep zoom it is also the only arbitrary-precision arithmetic
	// in the renderer.
	smp, deep := newSampler(req)
	if stop.Load() {
		return
	}

	sw, sh := req.sampleSize()
	data := make([]float32, sw*sh)
	out := image.NewRGBA(image.Rect(0, 0, req.W, req.H))

	prev := 0 // stride of the last pass actually executed
	for _, step := range passes {
		// Skip strides so coarse they would resolve to a single block.
		if step > 1 && step >= sw && step >= sh {
			continue
		}
		done, guessed := r.computePass(req, &smp, data, sw, sh, step, prev, stop)
		if !done {
			return // cancelled
		}
		prev = step

		r.mu.Lock()
		cs := r.live
		r.mu.Unlock()
		colorize(out, data, req.W, req.H, req.SS, cs, r.workers, stop)
		if stop.Load() {
			return
		}

		// Progress is the fraction of samples resolved so far: a stride-s
		// lattice covers 1/s² of the grid.
		prog := 1.0 / float64(step*step)
		r.publish(req, out, data, step == 1, prog, guessed, deep, time.Since(start), seq)
	}
}

// publish copies the working buffers into the published frame, unless a newer
// job has since been started.
func (r *Renderer) publish(req Request, out *image.RGBA, data []float32, done bool, prog, guessed float64, deep bool, elapsed time.Duration, seq uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.seq.Load() != seq {
		return // superseded
	}
	if r.frame.Img == nil || r.frame.Img.Rect != out.Rect {
		r.frame.Img = image.NewRGBA(out.Rect)
	}
	copy(r.frame.Img.Pix, out.Pix)
	r.frame.View = req.View
	r.frame.Done = done
	r.frame.Progress = prog
	r.frame.Elapsed = elapsed
	r.frame.Guessed = guessed
	r.frame.Perturbed = deep
	r.frame.Version = r.version.Add(1)

	// Only the finished grid is worth caching for re-colouring; an
	// intermediate one would be superseded moments later anyway. The copy is
	// never mutated afterwards, so Recolor can read it without the lock.
	if done {
		snap := make([]float32, len(data))
		copy(snap, data)
		r.data = snap
		r.dataW, r.dataH, r.dataSS = req.W, req.H, req.SS
		r.dataGen++
	}
}

// computePass iterates every sample on the stride-`step` lattice that was not
// already covered by the stride-`prev` pass, filling each result across its
// step×step block. It reports whether it ran to completion (false means the job
// was cancelled) and what fraction of candidate samples solid guessing skipped.
func (r *Renderer) computePass(req Request, smp *sampler, data []float32, sw, sh, step, prev int, stop *atomic.Bool) (bool, float64) {
	view := req.View
	mode := req.Color.Mode
	ssf := float64(req.SS)

	// Sample (sx, sy) sits at pixel (sx/ss, sy/ss) plus a half-sample offset,
	// so an SS×SS grid straddles the pixel centre symmetrically.
	dx := view.Scale / ssf
	// Offset of sample column 0 / row 0 from the view centre. Offsets, not
	// absolute coordinates: at deep zoom the centre has hundreds of bits and
	// only these differences are float64-sized.
	x0 := -(float64(req.W)/2)*view.Scale + 0.5*dx
	y0 := (float64(req.H)/2)*view.Scale - 0.5*dx

	// Solid guessing only has neighbours to consult once a coarser pass has
	// run, so the very first pass always computes everything.
	guess := req.Guess
	if prev == 0 {
		guess = params.GuessOff
	}

	rows := (sh + step - 1) / step
	var next atomic.Int64
	var candidates, skipped atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < r.workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each worker gets its own sampler: Perturb takes the options by
			// pointer, and the reference orbit is shared read-only.
			smp := *smp
			var cand, skip int64
			defer func() {
				candidates.Add(cand)
				skipped.Add(skip)
			}()
			for {
				ri := int(next.Add(1) - 1)
				if ri >= rows {
					return
				}
				if stop.Load() {
					return
				}
				sy := ri * step
				cy := y0 - float64(sy)*dx
				// Rows whose y is on the previous lattice only need the
				// columns the previous pass skipped.
				yOnPrev := prev > 0 && sy%prev == 0
				for sx := 0; sx < sw; sx += step {
					if yOnPrev && sx%prev == 0 {
						continue // already computed by an earlier pass
					}
					cand++
					if solid(data, sw, sh, sx, sy, prev, guess) {
						// The enclosing neighbours agree, so the block fill
						// from the coarser pass is already the right answer.
						skip++
						continue
					}
					res := smp.at(x0+float64(sx)*dx, cy)
					fillBlock(data, sw, sh, sx, sy, step, sampleValue(res, mode, dx))
				}
			}
		}()
	}
	wg.Wait()
	if stop.Load() {
		return false, 0
	}
	frac2 := 0.0
	if c := candidates.Load(); c > 0 {
		frac2 = float64(skipped.Load()) / float64(c)
	}
	return true, frac2
}

// sampleValue reduces an orbit result to the single float32 the sample buffer
// stores. Which quantity that is depends on the colouring mode, which is why
// changing mode needs a re-render rather than a re-colour.
//
// pixel is the world-space size of one sample, needed to express the distance
// estimate in units that do not change as you zoom.
func sampleValue(r fractal.Result, mode params.ColorMode, pixel float64) float32 {
	if !r.Escaped {
		return insideMarker
	}
	switch mode {
	case params.ColorIteration:
		return float32(r.Iter)

	case params.ColorDistance:
		// The exterior distance estimate for an escaping orbit is
		//
		//	d ≈ 2·|z|·ln|z| / |dz|
		//
		// Dividing by the sample size turns it into a boundary line whose
		// width in pixels is the same at every magnification, and clamping at
		// 1 leaves everything further away flat.
		dz2 := r.Dr*r.Dr + r.Di*r.Di
		if dz2 <= 0 || r.Mod2 <= 1 || pixel <= 0 {
			return 0
		}
		mod := math.Sqrt(r.Mod2)
		d := 2 * mod * math.Log(mod) / math.Sqrt(dz2)
		return float32(math.Min(1, d/pixel))

	case params.ColorTrap:
		if math.IsInf(r.Trap, 1) {
			return 1
		}
		return float32(math.Min(1, r.Trap))
	}
	// params.ColorSmooth, and anything unrecognised.
	v := float32(r.N)
	if v < 0 {
		v = 0
	}
	return v
}

// solid implements solid guessing: the sample at (sx, sy) is assumed to match
// its neighbours when the four corners of the enclosing coarse cell all agree.
// Those corners were computed by the previous pass, whose block fill already
// wrote the agreed value across this position — so agreeing means there is
// simply nothing to do.
//
// This is an assumption, not a theorem: a filament thinner than the cell can
// thread between four agreeing corners and be missed. The two strategies differ
// in how much they are willing to risk that.
func solid(data []float32, sw, sh, sx, sy, prev int, g params.Guess) bool {
	if g == params.GuessOff || prev <= 0 {
		return false
	}
	x0 := (sx / prev) * prev
	y0 := (sy / prev) * prev
	// The far corners must stay on the coarse lattice. Clamping them to the
	// grid edge instead would read a position this very pass may be writing
	// from another goroutine — the lattice positions are the only ones
	// guaranteed to have been finished by the previous pass and left alone by
	// this one. Where the cell runs off the edge, fold it back onto the near
	// corner rather than sampling somewhere unsynchronised.
	x1, y1 := x0+prev, y0+prev
	if x1 > sw-1 {
		x1 = x0
	}
	if y1 > sh-1 {
		y1 = y0
	}

	a := data[y0*sw+x0]
	b := data[y0*sw+x1]
	c := data[y1*sw+x0]
	d := data[y1*sw+x1]

	// Bit-for-bit. In a smooth gradient neighbouring samples are essentially
	// never exactly equal, so this fires only over genuinely flat regions —
	// mostly the interior, which is also the expensive part.
	return a == b && a == c && a == d
}

// fillBlock writes v across the step×step block anchored at (sx, sy),
// clipped to the sample grid. This is what makes a coarse pass look like a
// complete, blocky image rather than a sparse dot pattern.
func fillBlock(data []float32, sw, sh, sx, sy, step int, v float32) {
	if step == 1 {
		data[sy*sw+sx] = v
		return
	}
	ymax := min(sy+step, sh)
	xmax := min(sx+step, sw)
	for y := sy; y < ymax; y++ {
		row := data[y*sw+sx : y*sw+xmax]
		for i := range row {
			row[i] = v
		}
	}
}

// modeScale puts each colouring mode's natural value range on the same
// footing, so that the density control means roughly the same thing whichever
// mode is active. Escape counts run to hundreds; distance and trap values are
// already normalised to 0..1.
func modeScale(m params.ColorMode) float64 {
	switch m {
	case params.ColorDistance, params.ColorTrap:
		return 1.0 / 8
	}
	return 1.0 / 256
}

// mapping is the value-to-colour function, resolved once per pass from a
// ColorSpec. It exists so that colouring and solid guessing cannot drift apart:
// guessing decides whether two samples "agree" by asking this exact function
// whether they would be painted the same colour.
type mapping struct {
	pal    *palette.Palette
	gain   float64
	off    float64
	lut    float64
	logMap bool
	inside color.RGBA
}

func newMapping(cs ColorSpec) mapping {
	pal := cs.Ramp
	if pal == nil {
		pal = palette.Presets[0]
	}
	return mapping{
		pal:  pal,
		gain: cs.Density * modeScale(cs.Mode),
		off:  cs.Offset,
		lut:  float64(pal.Size()),
		// The logarithmic option only makes sense for escape counts; distance
		// and trap values are already compressed into 0..1.
		logMap: cs.LogMap && (cs.Mode == params.ColorSmooth ||
			cs.Mode == params.ColorIteration || cs.Mode == ""),
		inside: cs.Inside,
	}
}

// index is the palette entry a stored value lands on.
func (m mapping) index(v float32) int {
	t := float64(v)
	if m.logMap {
		// Deep zooms pile most pixels into a narrow band of high counts; the
		// log spreads them back out.
		t = math.Log1p(t) * 24
	}
	return int((t*m.gain + m.off) * m.lut)
}

// at is the colour a stored value paints.
func (m mapping) at(v float32) color.RGBA {
	if v < 0 {
		return m.inside
	}
	return m.pal.Index(m.index(v))
}

// relief lights the colouring value as a height field.
//
// The value that picks the colour also stands in for altitude, so the escape
// bands become terrain and the whole image gains the embossed look that
// Fractint got from its 3D mode. This is the lighting half of that idea, not
// the projection half: the surface is shaded in place rather than rotated into
// perspective.
type relief struct {
	on         bool
	lx, ly, lz float64 // unit vector towards the light
	depth      float64 // height exaggeration, in palette cycles per sample
	amb        float64
}

func newRelief(cs ColorSpec, gain float64) relief {
	if !cs.Light {
		return relief{}
	}
	az := cs.LightAz * math.Pi / 180
	el := cs.LightEl * math.Pi / 180
	// Height is measured in palette cycles, so the control means the same
	// thing whichever colouring mode is active.
	return relief{
		on:    true,
		lx:    math.Cos(el) * math.Cos(az),
		ly:    math.Cos(el) * math.Sin(az),
		lz:    math.Sin(el),
		depth: cs.LightDepth * gain * 40,
		amb:   cs.LightAmb,
	}
}

// shade returns the brightness multiplier at one output pixel.
func (r relief) shade(data []float32, sw, sh, sx, sy, step int) float64 {
	c := heightAt(data, sw, sh, sx, sy, 0)
	// Central differences, with interior samples pinned to the centre value so
	// the boundary of the set does not read as a cliff and ring the whole
	// silhouette in black.
	dx := (heightAt(data, sw, sh, sx+step, sy, c) - heightAt(data, sw, sh, sx-step, sy, c)) * 0.5
	dy := (heightAt(data, sw, sh, sx, sy+step, c) - heightAt(data, sw, sh, sx, sy-step, c)) * 0.5
	// The surface normal of z = f(x, y) is (-∂f/∂x, -∂f/∂y, 1), normalised.
	nx := -dx * r.depth
	ny := dy * r.depth // screen y runs down, world y runs up
	inv := 1 / math.Sqrt(nx*nx+ny*ny+1)
	lambert := (nx*r.lx + ny*r.ly + r.lz) * inv
	if lambert < 0 {
		lambert = 0
	}
	return r.amb + (1-r.amb)*lambert
}

// heightAt samples the field, clamping to the grid and substituting fallback
// for points inside the set, which have no height of their own.
func heightAt(data []float32, sw, sh, x, y int, fallback float64) float64 {
	x = min(max(x, 0), sw-1)
	y = min(max(y, 0), sh-1)
	v := float64(data[y*sw+x])
	if v < 0 {
		return fallback
	}
	return v
}

// colorize maps colouring values to pixels, averaging the SS×SS samples of
// each output pixel. It is a pure function of (data, spec) and is re-run
// whenever either changes.
func colorize(dst *image.RGBA, data []float32, w, h, ss int, cs ColorSpec, workers int, stop *atomic.Bool) {
	m := newMapping(cs)
	rel := newRelief(cs, m.gain)
	sw := w * ss
	nSamples := float64(ss * ss)

	var next atomic.Int64
	var wg sync.WaitGroup
	for k := 0; k < workers; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				y := int(next.Add(1) - 1)
				if y >= h {
					return
				}
				if stop != nil && stop.Load() {
					return
				}
				di := dst.PixOffset(0, y)
				for x := 0; x < w; x++ {
					var rs, gs, bs float64
					for sy := 0; sy < ss; sy++ {
						row := data[(y*ss+sy)*sw+x*ss:]
						for sx := 0; sx < ss; sx++ {
							c := m.at(row[sx])
							rs += float64(c.R)
							gs += float64(c.G)
							bs += float64(c.B)
						}
					}
					rs /= nSamples
					gs /= nSamples
					bs /= nSamples
					if rel.on {
						// Shade once per output pixel rather than per sample:
						// the gradient is a property of the pixel, and
						// supersampling should smooth the colour, not the light.
						sh := rel.shade(data, sw, h*ss, x*ss+ss/2, y*ss+ss/2, ss)
						rs *= sh
						gs *= sh
						bs *= sh
					}
					dst.Pix[di] = uint8(min(rs, 255) + 0.5)
					dst.Pix[di+1] = uint8(min(gs, 255) + 0.5)
					dst.Pix[di+2] = uint8(min(bs, 255) + 0.5)
					dst.Pix[di+3] = 255
					di += 4
				}
			}
		}()
	}
	wg.Wait()
}

// RenderImage renders a request to completion synchronously and returns the
// image. It bypasses the progressive machinery entirely — and with it solid
// guessing, which has no coarse pass to consult — so an export is always
// computed sample for sample.
func RenderImage(req Request, workers int) *image.RGBA {
	if req.SS < 1 {
		req.SS = 1
	}
	if workers < 1 {
		workers = runtime.NumCPU()
	}
	r := &Renderer{workers: workers}
	smp, _ := newSampler(req)
	sw, sh := req.sampleSize()
	data := make([]float32, sw*sh)
	stop := &atomic.Bool{}
	r.computePass(req, &smp, data, sw, sh, 1, 0, stop)
	out := image.NewRGBA(image.Rect(0, 0, req.W, req.H))
	colorize(out, data, req.W, req.H, req.SS, req.Color, workers, nil)
	return out
}
