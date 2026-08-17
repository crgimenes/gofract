package view3d

import (
	"math"
	"testing"
)

func almostEqual(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// TestOrbitPreservesDistance is the defining property of an orbit camera:
// wherever Yaw and Pitch point it, it stays exactly Distance from the origin.
func TestOrbitPreservesDistance(t *testing.T) {
	for _, yaw := range []float64{0, 0.3, 1.5, -2.1, 6.4} {
		for _, pitch := range []float64{-1.2, -0.3, 0, 0.7, 1.3} {
			c := Camera{Yaw: yaw, Pitch: pitch, Distance: 4.2, FOV: 0.8}
			if d := c.Pos().Len(); !almostEqual(d, 4.2, 1e-9) {
				t.Errorf("yaw=%.2f pitch=%.2f: distance %.6f, want 4.2", yaw, pitch, d)
			}
		}
	}
}

// TestOriginProjectsToCentre checks the projection math directly: a camera
// looking at the origin must always put the origin at the centre of the
// viewport, regardless of where the camera is orbiting from.
func TestOriginProjectsToCentre(t *testing.T) {
	const w, h = 800.0, 600.0
	for _, yaw := range []float64{0, 0.5, 2.2, -1.1} {
		for _, pitch := range []float64{-0.8, 0, 0.9} {
			c := Camera{Yaw: yaw, Pitch: pitch, Distance: 3, FOV: 0.9}
			proj := c.projectAll([]Vec3{{0, 0, 0}}, w, h)
			p := proj[0]
			if !p.visible {
				t.Fatalf("yaw=%.2f pitch=%.2f: origin reported not visible", yaw, pitch)
			}
			if !almostEqual(p.x, w/2, 1e-6) || !almostEqual(p.y, h/2, 1e-6) {
				t.Errorf("yaw=%.2f pitch=%.2f: origin projected to (%.4f, %.4f), want (%.1f, %.1f)",
					yaw, pitch, p.x, p.y, w/2, h/2)
			}
		}
	}
}

// TestBehindCameraIsNotVisible checks the near-plane clip: a point on the far
// side of the camera from the origin must not be reported as visible, or the
// mesh builders would try to draw a triangle that has been turned inside out
// by the projection's division by a near-zero (or negative) depth.
func TestBehindCameraIsNotVisible(t *testing.T) {
	c := Camera{Yaw: 0, Pitch: 0, Distance: 3, FOV: 0.8}
	// The camera sits at (0,0,3) looking at the origin, i.e. looking in -Z.
	// A point further out along +Z is behind it.
	proj := c.projectAll([]Vec3{{0, 0, 10}}, 400, 300)
	if proj[0].visible {
		t.Error("a point behind the camera was reported visible")
	}
}

// flatHeight is a Height with no escaped samples at all — every cell reports
// (0, true) — which stands in for "nothing has rendered yet" or a fractal with
// no exterior in view.
func flatHeight() Height { return NewHeight(nil, 0, 0) }

// bumpHeight is a small height grid with one raised cell in the middle, useful
// for checking that elevation actually reaches the mesh.
func bumpHeight(n int) Height {
	grid := make([]float32, n*n)
	for i := range grid {
		grid[i] = 0
	}
	grid[(n/2)*n+n/2] = 1
	return NewHeight(grid, n, n)
}

// TestLandscapeIsFlatWithoutHeightData checks that an all-interior height grid
// produces a mesh that is, in fact, flat: every vertex should share the same Y
// before projection, which shows up as the projected screen positions forming
// a perfect grid rather than a jumble.
func TestLandscapeIsFlatWithoutHeightData(t *testing.T) {
	s := Scene{
		Cam:         Camera{Pitch: 1.4, Distance: 5, FOV: 0.7}, // near top-down, so screen shape mirrors world shape
		ViewportW:   400,
		ViewportH:   400,
		Light:       Vec3{0, 1, 0},
		Ambient:     0.2,
		HeightScale: 1,
	}
	verts := s.BuildLandscape(flatHeight(), 8, 8)
	if len(verts) == 0 {
		t.Fatal("no vertices produced")
	}
	if len(verts)%3 != 0 {
		t.Fatalf("%d vertices is not a whole number of triangles", len(verts))
	}
	// Lit from directly above, a flat plane's every facet has the same
	// brightness: the surface normal is (0,1,0) everywhere, exactly aligned
	// with the light.
	want := verts[0].Bright
	for _, v := range verts {
		if !almostEqual(v.Bright, want, 1e-9) {
			t.Fatalf("brightness varies over a flat, top-lit plane: %v vs %v", v.Bright, want)
		}
	}
	if want < 0.95 {
		t.Errorf("a flat plane lit from directly above should be at full brightness, got %v", want)
	}
}

// TestLandscapeRisesWithHeight checks that a raised cell actually changes the
// mesh: with the light held fixed, the ridge the bump creates must shade some
// facets away from full brightness, which a perfectly flat mesh (the test
// above) does not do.
func TestLandscapeRisesWithHeight(t *testing.T) {
	s := Scene{
		Cam:         Camera{Pitch: 1.4, Distance: 5, FOV: 0.7},
		ViewportW:   400,
		ViewportH:   400,
		Light:       Vec3{0, 1, 0},
		Ambient:     0.2,
		HeightScale: 1,
	}
	verts := s.BuildLandscape(bumpHeight(9), 40, 40)
	minB, maxB := math.Inf(1), math.Inf(-1)
	for _, v := range verts {
		minB = math.Min(minB, v.Bright)
		maxB = math.Max(maxB, v.Bright)
	}
	if maxB-minB < 0.05 {
		t.Errorf("a raised bump produced almost no shading variation (%v..%v); "+
			"the height data does not appear to reach the mesh", minB, maxB)
	}
}

// TestLandscapeSortedBackToFront pins the painter's-algorithm ordering the
// oblique-camera rendering depends on: after flatten, consecutive triangles in
// the output must have non-increasing depth. Depth is not part of the public
// Vertex type — a caller has no business sorting further and undoing the
// work — so this reaches for the unexported triangle list directly.
func TestLandscapeSortedBackToFront(t *testing.T) {
	s := Scene{
		Cam:         Camera{Yaw: 0.4, Pitch: 0.6, Distance: 4, FOV: 0.8},
		ViewportW:   400,
		ViewportH:   300,
		Light:       Vec3{0, 1, 0},
		Ambient:     0.3,
		HeightScale: 0.8,
	}
	tris := s.landscapeTriangles(bumpHeight(9), 30, 24)
	if len(tris) < 10 {
		t.Fatal("mesh too small to test ordering")
	}
	// flatten sorts its argument in place before building the Vertex stream,
	// so tris itself is in the emitted order once it returns.
	flatten(tris)
	prev := math.Inf(1)
	for i, tr := range tris {
		if tr.depth > prev+1e-9 {
			t.Fatalf("triangle %d has depth %v, greater than the previous %v: not back-to-front", i, tr.depth, prev)
		}
		prev = tr.depth
	}
}

// TestGlobeCullsBackFaces checks that roughly half the sphere's facets are
// removed by back-face culling — the visible hemisphere, not the whole globe.
func TestGlobeCullsBackFaces(t *testing.T) {
	const lonSegs, latSegs = 40, 20
	s := Scene{
		Cam:         Camera{Yaw: 0, Pitch: 0, Distance: 4, FOV: 0.8},
		ViewportW:   400,
		ViewportH:   400,
		Light:       Vec3{0, 0, 1},
		Ambient:     0.2,
		HeightScale: 0.1,
	}
	verts := s.BuildGlobe(flatHeight(), lonSegs, latSegs)
	if len(verts)%3 != 0 {
		t.Fatalf("%d vertices is not a whole number of triangles", len(verts))
	}
	gotTris := len(verts) / 3
	totalTris := lonSegs * latSegs * 2
	frac := float64(gotTris) / float64(totalTris)
	t.Logf("%d of %d triangles survived culling (%.1f%%)", gotTris, totalTris, frac*100)
	if frac < 0.3 || frac > 0.7 {
		t.Errorf("expected roughly half the globe to survive back-face culling, got %.1f%%", frac*100)
	}
}

// TestGlobeFacesOutward is the sharpest check on the winding fix-up: every
// surviving triangle's flat-shaded brightness is derived from a normal that
// project3 orients using the sphere's outward direction, and back-face culling
// depends on that same normal pointing towards the camera. If the sign were
// wrong, culling would keep the *far* hemisphere instead of the near one — so
// this samples a triangle from the known-visible near pole (facing the camera
// at Yaw=0,Pitch=0, i.e. +Z) and checks it survived, then does the same for the
// far pole and checks it did not.
func TestGlobeFacesOutward(t *testing.T) {
	const lonSegs, latSegs = 60, 30
	s := Scene{
		Cam:         Camera{Yaw: 0, Pitch: 0, Distance: 5, FOV: 0.6},
		ViewportW:   400,
		ViewportH:   400,
		Light:       Vec3{0, 0, 1},
		Ambient:     0.5,
		HeightScale: 0,
	}
	verts := s.BuildGlobe(flatHeight(), lonSegs, latSegs)

	// The camera sits on the +Z axis. A point near screen-centre comes from
	// the near pole of the sphere (facing the camera) and must be present;
	// nothing from the far pole should appear at all, because a convex sphere
	// entirely hides its far side.
	foundNearCentre := false
	for i := 0; i+2 < len(verts); i += 3 {
		for _, v := range verts[i : i+3] {
			if almostEqual(v.X, 200, 40) && almostEqual(v.Y, 200, 40) {
				foundNearCentre = true
			}
		}
	}
	if !foundNearCentre {
		t.Error("no geometry near the screen centre: the near hemisphere is missing, " +
			"which means the winding fix-up has the outward direction backwards")
	}
}

// TestHeightAtHandlesEmptyGrid guards the degenerate case the mesh builders
// must survive gracefully: no frame has completed yet, so there is no data at
// all. It must report the floor rather than index out of range.
func TestHeightAtHandlesEmptyGrid(t *testing.T) {
	h := NewHeight(nil, 0, 0)
	v, inside := h.at(5, 5)
	if !inside || v != 0 {
		t.Errorf("at() on an empty grid = (%v, %v), want (0, true)", v, inside)
	}
}

// TestBuildingWithEmptyHeightDoesNotPanic exercises both mesh builders with no
// height data at all, which is the state the app is in for one frame before
// the first render completes.
func TestBuildingWithEmptyHeightDoesNotPanic(t *testing.T) {
	s := Scene{
		Cam:         Camera{Pitch: 1, Distance: 4, FOV: 0.8},
		ViewportW:   200,
		ViewportH:   200,
		Light:       Vec3{0, 1, 0},
		Ambient:     0.3,
		HeightScale: 1,
	}
	h := NewHeight(nil, 0, 0)
	if v := s.BuildLandscape(h, 20, 20); len(v)%3 != 0 {
		t.Errorf("landscape: %d vertices", len(v))
	}
	if v := s.BuildGlobe(h, 20, 10); len(v)%3 != 0 {
		t.Errorf("globe: %d vertices", len(v))
	}
}

// TestSmallMeshesReturnNothing checks the degenerate-size guards rather than
// letting them build a mesh too thin to mean anything. flatten always
// allocates via make, so the result is an empty-but-non-nil slice — length is
// the property that matters, not nil-ness.
func TestSmallMeshesReturnNothing(t *testing.T) {
	s := Scene{Cam: Camera{Distance: 3, FOV: 0.8}, ViewportW: 100, ViewportH: 100, HeightScale: 1}
	h := flatHeight()
	if v := s.BuildLandscape(h, 1, 5); len(v) != 0 {
		t.Errorf("a 1-wide landscape grid should produce nothing, got %d vertices", len(v))
	}
	if v := s.BuildGlobe(h, 2, 5); len(v) != 0 {
		t.Errorf("a 2-segment globe should produce nothing, got %d vertices", len(v))
	}
}

func BenchmarkBuildLandscape(b *testing.B) {
	s := Scene{
		Cam:         Camera{Yaw: 0.4, Pitch: 0.5, Distance: 4, FOV: 0.8},
		ViewportW:   1280,
		ViewportH:   800,
		Light:       Vec3{0, 1, 0},
		Ambient:     0.3,
		HeightScale: 0.7,
	}
	h := bumpHeight(220)
	for b.Loop() {
		s.BuildLandscape(h, 180, 130)
	}
}

func BenchmarkBuildGlobe(b *testing.B) {
	s := Scene{
		Cam:         Camera{Yaw: 0.4, Pitch: 0.5, Distance: 3, FOV: 0.8},
		ViewportW:   1280,
		ViewportH:   800,
		Light:       Vec3{0, 0, 1},
		Ambient:     0.3,
		HeightScale: 0.2,
	}
	h := bumpHeight(220)
	for b.Loop() {
		s.BuildGlobe(h, 96, 48)
	}
}
