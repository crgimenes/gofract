package app

import (
	"fmt"
	"image/color"
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/vmaciel/gofract/internal/ui"
	"github.com/vmaciel/gofract/internal/view3d"
)

// mode3D selects whether the window shows the flat fractal or a 3D projection
// of it — Fractint's two "landscape" and "planet" transforms of the same
// escape-time picture, revived here as a view mode rather than a one-shot
// export.
type mode3D int

const (
	mode3DOff mode3D = iota
	mode3DLandscape
	mode3DGlobe
	mode3DCount
)

func (m mode3D) String() string {
	switch m {
	case mode3DLandscape:
		return "landscape"
	case mode3DGlobe:
		return "globe"
	}
	return "off"
}

// cam3D is the orbit camera: rotate around the scene, tilt the viewing angle,
// dolly in or out. That is the entire control surface Fractint's own 3D
// transform exposed, and it is enough to look at a landscape or a globe from
// any angle without the complexity — or the risk of turning the scene
// inside-out — of a free-flying camera.
type cam3D struct {
	Yaw, Pitch  float64 // degrees
	Distance    float64
	FOV         float64 // degrees
	HeightScale float64
}

func defaultCam3D() cam3D {
	return cam3D{Yaw: 35, Pitch: 32, Distance: 2.6, FOV: 42, HeightScale: 0.6}
}

// Mesh resolution. The landscape needs to be fine enough that its silhouette
// reads as terrain rather than a low-poly toy, but every vertex is projected
// and every triangle depth-sorted freshly whenever the camera moves — so this
// is a direct trade against how smoothly dragging can rotate the view.
// heightSample is deliberately a little finer than either mesh actually
// walks, so both share one cached grid without either being starved of detail.
const (
	landscapeGridW, landscapeGridH = 150, 110
	globeLonSegs, globeLatSegs     = 96, 48
	heightSampleW, heightSampleH   = 200, 150
)

// clampPitch keeps the camera the right side up. A landscape reads as a
// landscape only from above it; a globe has no "above", so it tolerates a
// wider range, stopping short of the pole where the orbit camera's basis
// degenerates (see view3d.Camera.basis).
func clampPitch(p float64, m mode3D) float64 {
	if m == mode3DLandscape {
		return math.Max(8, math.Min(85, p))
	}
	return math.Max(-85, math.Min(85, p))
}

func clampDistance(d float64) float64 { return math.Max(1.3, math.Min(12, d)) }

// heightScaleRange is the sane range for the height-scale slider in each mode.
// A globe's radius is 1, so anything past a modest bulge pokes terrain through
// its own far side; a landscape has no such ceiling.
func heightScaleRange(m mode3D) (lo, hi float64) {
	if m == mode3DGlobe {
		return 0.02, 0.6
	}
	return 0.02, 3
}

// clamp3D re-applies the current mode's ranges to the camera, so a setting
// left over from the other 3D mode (or the arbitrary zero value before either
// has ever been used) cannot produce a degenerate scene.
func (a *App) clamp3D() {
	a.cam.Pitch = clampPitch(a.cam.Pitch, a.mode3D)
	lo, hi := heightScaleRange(a.mode3D)
	a.cam.HeightScale = math.Max(lo, math.Min(hi, a.cam.HeightScale))
}

// cycle3D steps through Off → Landscape → Globe (or backwards), the one key
// binding that switches views regardless of which one is currently showing.
func (a *App) cycle3D(d int) {
	n := int(mode3DCount)
	a.mode3D = mode3D(((int(a.mode3D)+d)%n + n) % n)
	a.clamp3D()
	a.setStatus("3D view: %s", a.mode3D)
}

// handleMouse3D drags the orbit camera instead of the fractal: there is no 2D
// point under the cursor to zoom into or pick a Julia constant from once the
// screen shows a projection rather than the plane itself.
func (a *App) handleMouse3D(mx, my float64) {
	overUI := a.showPanel && (a.ui.Captured || a.ui.Active())

	if justPressed(ebiten.MouseButtonLeft) && !overUI {
		a.drag3D = true
		a.drag3DX0, a.drag3DY0 = mx, my
		a.camAtDragStart = a.cam
	}
	if a.drag3D {
		if ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) {
			const sensitivity = 0.3 // degrees per pixel
			a.cam.Yaw = a.camAtDragStart.Yaw + (mx-a.drag3DX0)*sensitivity
			a.cam.Pitch = clampPitch(a.camAtDragStart.Pitch-(my-a.drag3DY0)*sensitivity, a.mode3D)
		} else {
			a.drag3D = false
		}
	}

	if _, wheel := ebiten.Wheel(); wheel != 0 && !overUI {
		a.cam.Distance = clampDistance(a.cam.Distance * math.Pow(1.1, -wheel))
	}
}

// meshKey identifies the inputs a built 3D mesh depends on. Rebuilding costs a
// few milliseconds even at a modest mesh resolution — see
// BenchmarkBuildLandscape — so it only happens when one of these actually
// changed, not on every Draw call the way the flat 2D view is re-blitted.
type meshKey struct {
	mode      mode3D
	heightVer uint64
	cam       cam3D
	w, h      int
}

// meshFor returns the projected mesh for the current camera and height data,
// rebuilding only when meshKey has changed since the last call.
func (a *App) meshFor() []view3d.Vertex {
	key := meshKey{mode: a.mode3D, heightVer: a.heightVer, cam: a.cam, w: a.w, h: a.h}
	if key == a.meshKeyCache {
		return a.meshCache
	}
	a.meshKeyCache = key

	h := view3d.NewHeight(a.heightGrid, heightSampleW, heightSampleH)
	scene := view3d.Scene{
		Cam: view3d.Camera{
			Yaw:      a.cam.Yaw * math.Pi / 180,
			Pitch:    a.cam.Pitch * math.Pi / 180,
			Distance: a.cam.Distance,
			FOV:      a.cam.FOV * math.Pi / 180,
		},
		ViewportW: float64(a.w),
		ViewportH: float64(a.h),
		Light:     view3d.LightFromAzEl(a.st.Color.LightAz, a.st.Color.LightEl),
		// A 3D scene reads as flat black facets without some ambient floor,
		// even when the 2D relief-lighting control is set for high contrast —
		// there the interior colour fills in what shadow leaves black, but a
		// 3D facet with nothing behind it would just be void.
		Ambient:     math.Max(a.st.Color.LightAmb, 0.15),
		HeightScale: a.cam.HeightScale,
	}
	switch a.mode3D {
	case mode3DLandscape:
		a.meshCache = scene.BuildLandscape(h, landscapeGridW, landscapeGridH)
	case mode3DGlobe:
		a.meshCache = scene.BuildGlobe(h, globeLonSegs, globeLatSegs)
	default:
		a.meshCache = nil
	}
	return a.meshCache
}

// maxVertsPerDraw keeps each DrawTriangles call under the uint16 index limit,
// as a multiple of 3 so a batch boundary never falls inside a triangle.
const maxVertsPerDraw = 65535 - 65535%3

// draw3D paints the projected landscape or globe in place of the flat 2D
// fractal. The projection is built from the same rendered texture and cached
// colouring values that the 2D view uses — a 3D view is a different way of
// looking at one picture, not a second render.
func (a *App) draw3D(screen *ebiten.Image) {
	screen.Fill(color.RGBA{8, 8, 14, 255})
	if a.tex == nil || a.heightGrid == nil {
		a.ui.Begin(ui.PassDraw, screen)
		a.ui.Text(16*a.scale, 16*a.scale, "rendering…", ui.FaceUI, ui.Theme.TextDim)
		return
	}

	verts := a.meshFor()
	if len(verts) == 0 {
		return
	}
	if cap(a.idx3D) < maxVertsPerDraw {
		a.idx3D = make([]uint16, maxVertsPerDraw)
		for i := range a.idx3D {
			a.idx3D[i] = uint16(i)
		}
	}
	if cap(a.vbuf3D) < maxVertsPerDraw {
		a.vbuf3D = make([]ebiten.Vertex, maxVertsPerDraw)
	}

	tb := a.tex.Bounds()
	tw, th := float32(tb.Dx()), float32(tb.Dy())
	op := &ebiten.DrawTrianglesOptions{Filter: ebiten.FilterLinear}
	for start := 0; start < len(verts); start += maxVertsPerDraw {
		end := min(start+maxVertsPerDraw, len(verts))
		chunk := verts[start:end]
		vbuf := a.vbuf3D[:len(chunk)]
		for i, v := range chunk {
			vbuf[i] = ebiten.Vertex{
				DstX: float32(v.X), DstY: float32(v.Y),
				SrcX: float32(v.U) * tw, SrcY: float32(v.V) * th,
				ColorR: float32(v.Bright), ColorG: float32(v.Bright), ColorB: float32(v.Bright),
				ColorA: 1,
			}
		}
		screen.DrawTriangles(vbuf, a.idx3D[:len(chunk)], a.tex, op)
	}
}

// drawHUD3D is the compact status line shown over a 3D view: the ordinary HUD
// talks about coordinates and iteration counts, neither of which describes
// what the camera is doing.
func (a *App) drawHUD3D() {
	if !a.showHUD {
		return
	}
	s := a.scale
	msg := fmt.Sprintf("%s   yaw %.0f°  pitch %.0f°  dist %.2f   drag to rotate, wheel to zoom, 4 to leave",
		a.mode3D, a.cam.Yaw, a.cam.Pitch, a.cam.Distance)
	pad := 8 * s
	w := ui.TextWidth(msg, ui.FaceSmall) + 2*pad
	r := ui.Rect{X: 12 * s, Y: 12 * s, W: w, H: 22 * s}
	a.ui.Fill(r, ui.Theme.Overlay)
	a.ui.Text(r.X+pad, r.Y+(r.H-ui.LineH)/2, msg, ui.FaceSmall, ui.Theme.Text)
}
