// Package view3d projects a rendered fractal — its colours and its colouring
// values, read as a height field — into a 3D scene: a landscape viewed
// obliquely, or the same picture wrapped onto a globe.
//
// The package is deliberately free of any graphics-library dependency. It
// takes plain data in and returns plain, screen-space triangles out; the
// caller (internal/app) is the only place that touches Ebitengine, which
// keeps the geometry here testable without a graphics context.
package view3d

import "math"

// Vec3 is a point or direction in world space.
type Vec3 struct{ X, Y, Z float64 }

func (a Vec3) Add(b Vec3) Vec3      { return Vec3{a.X + b.X, a.Y + b.Y, a.Z + b.Z} }
func (a Vec3) Sub(b Vec3) Vec3      { return Vec3{a.X - b.X, a.Y - b.Y, a.Z - b.Z} }
func (a Vec3) Scale(s float64) Vec3 { return Vec3{a.X * s, a.Y * s, a.Z * s} }
func (a Vec3) Dot(b Vec3) float64   { return a.X*b.X + a.Y*b.Y + a.Z*b.Z }

func (a Vec3) Cross(b Vec3) Vec3 {
	return Vec3{
		a.Y*b.Z - a.Z*b.Y,
		a.Z*b.X - a.X*b.Z,
		a.X*b.Y - a.Y*b.X,
	}
}

func (a Vec3) Len() float64 { return math.Sqrt(a.Dot(a)) }

// Norm returns a unit vector, or Z-up if a has no length — an arbitrary but
// harmless fallback for the one degenerate case (a normal computed from a
// zero-area triangle) that should never occur for a well-formed mesh but
// costs nothing to guard against.
func (a Vec3) Norm() Vec3 {
	l := a.Len()
	if l == 0 {
		return Vec3{0, 0, 1}
	}
	return a.Scale(1 / l)
}

// LightFromAzEl builds a unit vector towards a light given an azimuth
// (degrees, measured in the ground plane) and an elevation (degrees above the
// ground plane). It uses the same two numbers as the 2D relief-lighting
// sliders, so that control doubles as the 3D sun position — moving it changes
// both views consistently, without asking for a second pair of sliders.
func LightFromAzEl(azDeg, elDeg float64) Vec3 {
	az := azDeg * math.Pi / 180
	el := elDeg * math.Pi / 180
	return Vec3{
		X: math.Cos(el) * math.Cos(az),
		Y: math.Sin(el),
		Z: math.Cos(el) * math.Sin(az),
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
