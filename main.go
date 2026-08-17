// Command gofract is a fractal explorer in the spirit of WinFract/Fractint,
// built on Ebitengine.
package main

import (
	"flag"
	"log"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/vmaciel/gofract/internal/app"
)

func main() {
	var (
		width  = flag.Int("w", 1280, "initial window width")
		height = flag.Int("h", 800, "initial window height")
		hidpi  = flag.Bool("hidpi", true, "render at the display's native pixel density")
		load   = flag.String("load", "", "parameter file to open at start-up")
		par    = flag.String("params", "gofract.json", "file used by save/load parameters")
		full   = flag.Bool("fullscreen", false, "start in full screen")
		vsync  = flag.Bool("vsync", true, "synchronise presentation to the display refresh")
	)
	flag.Parse()

	ebiten.SetWindowTitle("GoFract — fractal explorer")
	ebiten.SetWindowSize(*width, *height)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	ebiten.SetFullscreen(*full)
	ebiten.SetVsyncEnabled(*vsync)
	// The fractal itself is redrawn from a texture, so there is nothing to
	// gain from running the event loop faster than the display.
	ebiten.SetScreenClearedEveryFrame(false)

	g := app.New(app.Config{
		HiDPI:     *hidpi,
		LoadPath:  *load,
		ParamPath: *par,
	})
	if err := ebiten.RunGame(g); err != nil && err != ebiten.Termination {
		log.Fatal(err)
	}
}
