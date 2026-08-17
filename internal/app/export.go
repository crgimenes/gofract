package app

import (
	"fmt"
	"image/png"
	"os"
	"runtime"
	"time"

	"github.com/vmaciel/gofract/internal/params"
	"github.com/vmaciel/gofract/internal/renderer"
)

// Export size multipliers, relative to the current window.
var (
	exportMuls   = []int{1, 2, 4, 8}
	exportLabels = []string{"1x window", "2x window", "4x window", "8x window"}
)

func exportIndex(mul int) int {
	for i, m := range exportMuls {
		if m == mul {
			return i
		}
	}
	return 0
}

// defaultExportMul is the initial PNG export size. Two is a good default: it
// gives a printable image without a wait long enough to need a progress bar.
const defaultExportMul = 2

func (a *App) exportMul() int { return a.expMul }

func (a *App) setExportMul(m int) { a.expMul = m }

// exportPNG renders the current view off-screen at a multiple of the window
// size and writes it to disk. It runs on its own goroutine so a 4x or 8x
// export does not freeze the explorer; the on-screen renderer keeps its own
// worker pool, so the two simply share the machine.
func (a *App) exportPNG() {
	a.statusMu.Lock()
	if a.exporting {
		a.statusMu.Unlock()
		a.setStatus("Export already in progress")
		return
	}
	a.exporting = true
	a.statusMu.Unlock()

	mul := a.exportMul()
	st := a.st
	w, h := a.w*mul, a.h*mul
	// Dividing the scale by the same factor keeps the framing identical: the
	// image covers exactly what is on screen, with more samples.
	view := st.View
	view.Scale /= float64(mul)

	name := fmt.Sprintf("gofract-%s.png", time.Now().Format("20060102-150405"))
	a.setStatus("Rendering %d x %d ...", w, h)

	go func() {
		defer func() {
			a.statusMu.Lock()
			a.exporting = false
			a.statusMu.Unlock()
		}()
		start := time.Now()
		img := renderer.RenderImage(renderer.Request{
			W:     w,
			H:     h,
			SS:    st.Super,
			View:  view,
			Frac:  st.Fractal(),
			Opts:  st.Options(),
			Color: renderer.SpecFrom(st.Color, st.Fractal()),
		}, runtime.NumCPU())

		f, err := os.Create(name)
		if err != nil {
			a.setStatus("Save failed: %v", err)
			return
		}
		err = png.Encode(f, img)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			a.setStatus("Save failed: %v", err)
			return
		}
		a.setStatus("Saved %s (%dx%d, %.1fs)", name, w, h, time.Since(start).Seconds())
	}()
}

// paramPath is where Save/Load params go when none was given.
func (a *App) paramPath() string {
	if a.cfg.ParamPath != "" {
		return a.cfg.ParamPath
	}
	return "gofract.json"
}

func (a *App) saveParams() {
	p := a.paramPath()
	if err := a.st.Save(p); err != nil {
		a.setStatus("Save failed: %v", err)
		return
	}
	a.setStatus("Saved parameters to %s", p)
}

func (a *App) loadParams() {
	p := a.paramPath()
	st, err := params.Load(p)
	if err != nil {
		a.setStatus("Load failed: %v", err)
		return
	}
	a.hist.Push(a.st)
	a.st = st
	a.hist.Push(a.st)
	a.needRender = true
	a.setStatus("Loaded parameters from %s", p)
}
