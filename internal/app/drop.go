package app

import (
	"io/fs"
	"path"
	"strings"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/vmaciel/gofract/internal/palette"
	"github.com/vmaciel/gofract/internal/params"
)

// handleDrop accepts files dropped onto the window: a Fractint .map becomes the
// custom palette, and a .json becomes the whole state.
//
// Dropping is the only file picker here. Ebitengine has no native dialog, and
// wiring one up per platform would dwarf everything it enables — whereas
// dragging a .map onto the window is arguably nicer than a dialog anyway.
func (a *App) handleDrop() {
	files := ebiten.DroppedFiles()
	if files == nil {
		return
	}
	var names []string
	_ = fs.WalkDir(files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // skip unreadable entries, keep walking
		}
		names = append(names, p)
		return nil
	})

	for _, name := range names {
		switch strings.ToLower(path.Ext(name)) {
		case ".map":
			a.loadMapFS(files, name)
			return
		case ".json", ".par":
			a.loadParamsFS(files, name)
			return
		}
	}
	if len(names) > 0 {
		a.setStatus("Don't know what to do with %s — drop a .map or .json", path.Base(names[0]))
	}
}

func (a *App) loadMapFS(fsys fs.FS, name string) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		a.setStatus("Could not read %s: %v", path.Base(name), err)
		return
	}
	pal, err := palette.ParseMap(path.Base(name), b)
	if err != nil {
		a.setStatus("%v", err)
		return
	}
	a.hist.Push(a.st)
	a.st.Color.Palette = params.CustomPalette
	a.st.Color.Stops = palette.StopsOf(pal)
	a.st.Color.Inside = params.RGB{R: pal.Inside.R, G: pal.Inside.G, B: pal.Inside.B}
	a.stopSel = 0
	a.hist.Push(a.st)
	a.needColor = true
	a.setStatus("Loaded palette %s (%d stops)", path.Base(name), len(a.st.Color.Stops))
}

func (a *App) loadParamsFS(fsys fs.FS, name string) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		a.setStatus("Could not read %s: %v", path.Base(name), err)
		return
	}
	st, err := params.Parse(b)
	if err != nil {
		a.setStatus("%s: %v", path.Base(name), err)
		return
	}
	a.hist.Push(a.st)
	a.st = st
	a.hist.Push(a.st)
	a.needRender = true
	a.setStatus("Loaded parameters from %s", path.Base(name))
}
