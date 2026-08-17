package view3d

import (
	"math"
	"slices"
)

// Vertex is one projected, shaded mesh corner, ready for a caller to hand to
// whatever triangle rasteriser it has — Ebitengine's DrawTriangles, in this
// project.
type Vertex struct {
	X, Y   float64 // screen pixels
	U, V   float64 // texture coordinates into the source fractal image, 0..1
	Bright float64 // lighting multiplier, 0..1
}

// Height is a resampled grid of colouring values, read as elevations.
//
// Values follow the renderer's own convention: >= 0 for a point that escaped,
// and exactly -1 for a point that never did. A negative entry is not a low
// elevation — the renderer has nothing to report there — so it is treated
// separately, as the floor, rather than folded into the escaped values' range.
type Height struct {
	Grid     []float32
	W, H     int
	min, max float32 // range of the escaped (>= 0) values only
}

// NewHeight wraps a raw grid and measures the range worth normalising against.
func NewHeight(grid []float32, w, h int) Height {
	mn, mx := float32(1), float32(0) // mn > mx signals "no escaped samples"
	for _, v := range grid {
		if v < 0 {
			continue
		}
		if mn > mx {
			mn, mx = v, v
			continue
		}
		if v < mn {
			mn = v
		}
		if v > mx {
			mx = v
		}
	}
	if mn > mx {
		mn, mx = 0, 1
	}
	return Height{Grid: grid, W: w, H: h, min: mn, max: mx}
}

// at returns the elevation at a grid cell, normalised to 0..1, and whether the
// point is inside the set — in which case its elevation is the floor, 0. The
// index is clamped rather than wrapped: a landscape or globe mesh samples a
// slightly coarser or finer grid than Height was built at, and the edge value
// is the reasonable thing to repeat past the border.
func (h Height) at(i, j int) (v float64, inside bool) {
	if h.W <= 0 || h.H <= 0 || len(h.Grid) == 0 {
		return 0, true
	}
	i = clampInt(i, 0, h.W-1)
	j = clampInt(j, 0, h.H-1)
	raw := h.Grid[j*h.W+i]
	if raw < 0 {
		return 0, true
	}
	span := float64(h.max - h.min)
	if span <= 0 {
		return 0.5, false
	}
	return float64(raw-h.min) / span, false
}

// scaleIdx maps an index along a mesh axis of the given step count onto the
// corresponding column or row of a (generally different-resolution) height
// grid.
func scaleIdx(i, steps, size int) int {
	if steps <= 1 || size <= 1 {
		return 0
	}
	return clampInt(i*(size-1)/(steps-1), 0, size-1)
}

// Scene bundles everything a projection needs beyond the mesh's own shape:
// where the camera is, which way the light comes from, and how tall a
// colouring value of 1.0 should stand.
type Scene struct {
	Cam                  Camera
	ViewportW, ViewportH float64
	Light                Vec3
	Ambient              float64 // 0..1 floor under the lighting, so shadowed facets are dim rather than black
	HeightScale          float64
}

// tri is one shaded, projected triangle, kept as a unit through sorting so
// that "sort by depth" cannot separate a triangle's three corners.
type tri struct {
	v     [3]Vertex
	depth float64 // average camera-space depth, for back-to-front ordering
}

// shader carries the lighting inputs that are the same for every triangle in
// one Build call, so BuildLandscape/BuildGlobe do not have to thread them
// through every call individually.
type shader struct {
	light   Vec3
	ambient float64
	camPos  Vec3
}

// tri shades and projects one triangle already known to have valid indices,
// returning false if any corner is behind the camera or — for a culled mesh —
// if the triangle faces away from the viewer.
//
// outward is a direction the true surface normal is known to lie within 90° of
// — world up for a heightfield, which can never fold back on itself, or the
// sphere's centre-to-point direction for a globe. Because a raw cross product
// carries an arbitrary sign depending on vertex winding, this is what turns it
// into a normal that actually points out of the surface, without the caller
// having to reason about winding order by hand.
func (sh shader) tri(pos []Vec3, proj []projPoint, ia, ib, ic int, uva, uvb, uvc [2]float64, outward Vec3, cull bool) (tri, bool) {
	pa, pb, pc := proj[ia], proj[ib], proj[ic]
	if !pa.visible || !pb.visible || !pc.visible {
		return tri{}, false
	}
	a, b, c := pos[ia], pos[ib], pos[ic]
	n := b.Sub(a).Cross(c.Sub(a))
	if n.Dot(outward) < 0 {
		n = n.Scale(-1)
	}
	n = n.Norm()

	if cull {
		centroid := a.Add(b).Add(c).Scale(1.0 / 3)
		if n.Dot(sh.camPos.Sub(centroid)) <= 0 {
			return tri{}, false
		}
	}

	bright := sh.ambient + (1-sh.ambient)*max(0, n.Dot(sh.light))
	return tri{
		v: [3]Vertex{
			{X: pa.x, Y: pa.y, U: uva[0], V: uva[1], Bright: bright},
			{X: pb.x, Y: pb.y, U: uvb[0], V: uvb[1], Bright: bright},
			{X: pc.x, Y: pc.y, U: uvc[0], V: uvc[1], Bright: bright},
		},
		depth: (pa.z + pb.z + pc.z) / 3,
	}, true
}

// flatten sorts triangles back-to-front (the painter's algorithm) and lays
// them out as a flat, triangle-list vertex stream.
//
// For the globe this ordering is not even load-bearing — once back faces are
// culled, a convex surface's remaining facets cannot occlude one another, so
// any order would do. For the landscape it matters: a heightfield can and does
// have hills hiding valleys behind them from an oblique camera, and this
// keeps nearer terrain painted over farther terrain without a per-pixel depth
// buffer.
// flatten sorts tris back-to-front and returns the flattened Vertex stream. As
// a postcondition, tris itself ends up in the same order — a cheap linear
// reorder once the sort is done, and a useful guarantee for a caller (or a
// test) that wants to inspect the order that was chosen.
func flatten(tris []tri) []Vertex {
	// Sorting a slice of *indices* rather than the 128-byte tri values
	// themselves is the difference between moving 8 bytes per swap and moving
	// 128: for a landscape mesh with tens of thousands of triangles, that was
	// measured to roughly halve the cost of this function, which otherwise
	// dominates the per-frame budget while the camera is being dragged.
	order := make([]int, len(tris))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(a, b int) int {
		switch da, db := tris[a].depth, tris[b].depth; {
		case da > db:
			return -1
		case da < db:
			return 1
		}
		return 0
	})

	sorted := make([]tri, len(tris))
	out := make([]Vertex, 0, len(tris)*3)
	for i, idx := range order {
		t := tris[idx]
		sorted[i] = t
		out = append(out, t.v[0], t.v[1], t.v[2])
	}
	copy(tris, sorted)
	return out
}

// BuildLandscape projects a heightfield of gridW×gridH vertices — X and Z in
// [-1,1], Y the normalised colouring value times HeightScale — into screen
// space. A point inside the set sits at the floor, Y = 0, which is what gives
// the escape-time boundary the look of an island's coastline rather than an
// arbitrary crease in the terrain.
func (s Scene) BuildLandscape(h Height, gridW, gridH int) []Vertex {
	return flatten(s.landscapeTriangles(h, gridW, gridH))
}

// landscapeTriangles is BuildLandscape before the final sort-and-flatten, kept
// separate so tests can inspect triangle depth directly rather than only the
// flattened, already-sorted vertex stream.
func (s Scene) landscapeTriangles(h Height, gridW, gridH int) []tri {
	if gridW < 2 || gridH < 2 {
		return nil
	}
	pos := make([]Vec3, gridW*gridH)
	for j := range gridH {
		z := (float64(j)/float64(gridH-1) - 0.5) * 2
		hj := scaleIdx(j, gridH, h.H)
		row := j * gridW
		for i := range gridW {
			x := (float64(i)/float64(gridW-1) - 0.5) * 2
			hi := scaleIdx(i, gridW, h.W)
			elev, _ := h.at(hi, hj)
			pos[row+i] = Vec3{X: x, Y: elev * s.HeightScale, Z: z}
		}
	}

	proj := s.Cam.projectAll(pos, s.ViewportW, s.ViewportH)
	sh := shader{light: s.Light, ambient: s.Ambient, camPos: s.Cam.Pos()}
	up := Vec3{0, 1, 0}

	tris := make([]tri, 0, (gridW-1)*(gridH-1)*2)
	for j := range gridH - 1 {
		v0, v1 := float64(j)/float64(gridH-1), float64(j+1)/float64(gridH-1)
		for i := range gridW - 1 {
			u0, u1 := float64(i)/float64(gridW-1), float64(i+1)/float64(gridW-1)
			i00, i10 := j*gridW+i, j*gridW+i+1
			i01, i11 := (j+1)*gridW+i, (j+1)*gridW+i+1

			if t, ok := sh.tri(pos, proj, i00, i10, i11,
				[2]float64{u0, v0}, [2]float64{u1, v0}, [2]float64{u1, v1}, up, false); ok {
				tris = append(tris, t)
			}
			if t, ok := sh.tri(pos, proj, i00, i11, i01,
				[2]float64{u0, v0}, [2]float64{u1, v1}, [2]float64{u0, v1}, up, false); ok {
				tris = append(tris, t)
			}
		}
	}
	return tris
}

// BuildGlobe wraps the source image onto a sphere of unit radius, optionally
// bulging its surface outward by the colouring value at each point — a subtle
// touch, but it is the same data the landscape uses for its mountains, so a
// fractal already reads as a planet's terrain rather than a texture pasted
// onto a ball.
//
// Longitude runs across lonSegs+1 columns and latitude across latSegs+1 rows
// so that the seam at longitude 0/2π closes exactly, at the cost of a
// duplicated column of vertices — the standard trade for a UV sphere.
func (s Scene) BuildGlobe(h Height, lonSegs, latSegs int) []Vertex {
	if lonSegs < 3 || latSegs < 2 {
		return nil
	}
	gw, gh := lonSegs+1, latSegs+1
	pos := make([]Vec3, gw*gh)
	for j := range gh {
		lat := (float64(j)/float64(latSegs) - 0.5) * math.Pi
		hj := scaleIdx(j, gh, h.H)
		cosLat, sinLat := math.Cos(lat), math.Sin(lat)
		row := j * gw
		for i := range gw {
			lon := float64(i) / float64(lonSegs) * 2 * math.Pi
			hi := scaleIdx(i, gw, h.W)
			elev, _ := h.at(hi, hj)
			radius := 1 + elev*s.HeightScale
			pos[row+i] = Vec3{
				X: radius * cosLat * math.Sin(lon),
				Y: radius * sinLat,
				Z: radius * cosLat * math.Cos(lon),
			}
		}
	}

	proj := s.Cam.projectAll(pos, s.ViewportW, s.ViewportH)
	sh := shader{light: s.Light, ambient: s.Ambient, camPos: s.Cam.Pos()}

	tris := make([]tri, 0, lonSegs*latSegs*2)
	for j := range latSegs {
		v0, v1 := float64(j)/float64(latSegs), float64(j+1)/float64(latSegs)
		for i := range lonSegs {
			u0, u1 := float64(i)/float64(lonSegs), float64(i+1)/float64(lonSegs)
			i00, i10 := j*gw+i, j*gw+i+1
			i01, i11 := (j+1)*gw+i, (j+1)*gw+i+1

			// The outward direction is only ever needed to fix the sign of a
			// cross product, not as a precise normal, so the sum of the
			// triangle's own corners — all close to the sphere's surface — is
			// a cheap and sufficiently accurate stand-in for "away from the
			// centre" without a second pass over the mesh.
			out1 := pos[i00].Add(pos[i10]).Add(pos[i11])
			if t, ok := sh.tri(pos, proj, i00, i10, i11,
				[2]float64{u0, v0}, [2]float64{u1, v0}, [2]float64{u1, v1}, out1, true); ok {
				tris = append(tris, t)
			}
			out2 := pos[i00].Add(pos[i11]).Add(pos[i01])
			if t, ok := sh.tri(pos, proj, i00, i11, i01,
				[2]float64{u0, v0}, [2]float64{u1, v1}, [2]float64{u0, v1}, out2, true); ok {
				tris = append(tris, t)
			}
		}
	}
	return flatten(tris)
}
