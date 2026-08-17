package view3d

import "math"

// Camera is a classic orbit ("arcball") camera: it always looks at the
// origin, from a point at Distance, offset by Yaw around the vertical axis and
// Pitch above the horizontal. That is the whole control surface Fractint's own
// 3D transform exposed — rotate, tilt, dolly — and it is enough to look at a
// landscape or a globe from any angle without the complexity (or the risk of
// pointing the scene inside-out) of a free-flying camera.
type Camera struct {
	Yaw, Pitch float64 // radians
	Distance   float64
	FOV        float64 // vertical field of view, radians
}

// near is the closest a point may come to the camera before it is clipped.
// Anything closer would divide by a near-zero depth during projection.
const near = 0.05

// Pos is the camera's position in world space.
func (c Camera) Pos() Vec3 {
	return Vec3{
		X: c.Distance * math.Cos(c.Pitch) * math.Sin(c.Yaw),
		Y: c.Distance * math.Sin(c.Pitch),
		Z: c.Distance * math.Cos(c.Pitch) * math.Cos(c.Yaw),
	}
}

// basis returns the camera's right/up/forward axes. Forward points from the
// camera towards the origin, since the camera always looks there.
//
// Pitch is expected to stay short of ±90°: at exactly vertical, forward and
// the world's up vector coincide and right becomes undefined (a zero cross
// product) — the classic gimbal lock. Callers clamp Pitch to a few degrees
// short of that, which is also where an orbit camera stops being useful to
// look through in the first place.
func (c Camera) basis() (right, up, forward Vec3) {
	forward = c.Pos().Scale(-1).Norm()
	worldUp := Vec3{0, 1, 0}
	right = forward.Cross(worldUp).Norm()
	up = right.Cross(forward)
	return
}

// projPoint is a point already carried through the view/projection transform.
type projPoint struct {
	x, y, z float64 // screen pixels; z is camera-space depth
	visible bool
}

// projectAll transforms a batch of world points at once, computing the
// camera's basis and position only once rather than per point — the
// projection itself is cheap, but re-deriving the basis for every one of a
// mesh's tens of thousands of vertices would not be.
func (c Camera) projectAll(pts []Vec3, viewportW, viewportH float64) []projPoint {
	right, up, forward := c.basis()
	camPos := c.Pos()
	f := 1 / math.Tan(c.FOV/2)
	aspect := viewportW / viewportH

	out := make([]projPoint, len(pts))
	for i, p := range pts {
		rel := p.Sub(camPos)
		vx := rel.Dot(right)
		vy := rel.Dot(up)
		vz := rel.Dot(forward)
		if vz <= near {
			out[i] = projPoint{z: vz}
			continue
		}
		ndcX := vx * f / aspect / vz
		ndcY := vy * f / vz
		out[i] = projPoint{
			x:       (ndcX*0.5 + 0.5) * viewportW,
			y:       (1 - (ndcY*0.5 + 0.5)) * viewportH,
			z:       vz,
			visible: true,
		}
	}
	return out
}
