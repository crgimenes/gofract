// Package palette turns escape counts into colour.
//
// A palette is a cyclic gradient: the stop at position 1 wraps back onto the
// stop at position 0, so a continuously increasing escape count sweeps the
// gradient over and over without a seam. That property is what makes smooth
// colouring and (later) colour cycling look right.
package palette

import (
	"bufio"
	"bytes"
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// lutSize is the resolution of the baked lookup table. 2048 entries is far
// beyond what the eye resolves in a gradient but keeps the per-sample cost at
// a single array index.
const lutSize = 2048

// Stop is one control colour of a gradient. Stops are serialised as part of a
// custom palette, hence the JSON tags.
type Stop struct {
	Pos float64 `json:"pos"` // 0..1
	R   uint8   `json:"r"`
	G   uint8   `json:"g"`
	B   uint8   `json:"b"`
}

// RGBA is a shorthand for an opaque colour.
func RGBA(r, g, b uint8) color.RGBA { return color.RGBA{r, g, b, 255} }

// StopsOf returns a copy of a preset's control points, as the starting point
// for editing a custom palette.
func StopsOf(p *Palette) []Stop { return append([]Stop(nil), p.stops...) }

// Palette is a baked, cyclic colour ramp.
type Palette struct {
	Name   string
	Inside color.RGBA // colour of points that never escape
	stops  []Stop
	lut    [lutSize]color.RGBA
}

// New bakes a palette from control stops. Stops must be sorted by position;
// the ramp wraps from the last stop back to the first.
func New(name string, inside color.RGBA, stops []Stop) *Palette {
	p := &Palette{Name: name, Inside: inside, stops: append([]Stop(nil), stops...)}
	if len(stops) == 0 {
		return p
	}
	for i := range p.lut {
		p.lut[i] = sample(stops, float64(i)/lutSize)
	}
	return p
}

// sample linearly interpolates the gradient at t in [0,1), treating the stop
// list as circular: the last stop ramps back into the first across the seam
// at t = 1.
func sample(stops []Stop, t float64) color.RGBA {
	n := len(stops)
	if n == 1 {
		return color.RGBA{stops[0].R, stops[0].G, stops[0].B, 255}
	}
	// Extend the list by one wrapped stop at each end so the seam needs no
	// special case: ... [last-1] [first] [second] ... [last] [first+1] ...
	ext := make([]Stop, 0, n+2)
	first, last := stops[0], stops[n-1]
	last.Pos -= 1
	ext = append(ext, last)
	ext = append(ext, stops...)
	first.Pos += 1
	ext = append(ext, first)

	for i := 0; i+1 < len(ext); i++ {
		a, b := ext[i], ext[i+1]
		if t < a.Pos || t > b.Pos {
			continue
		}
		span := b.Pos - a.Pos
		if span <= 0 {
			return color.RGBA{b.R, b.G, b.B, 255}
		}
		f := (t - a.Pos) / span
		return color.RGBA{
			R: lerp8(a.R, b.R, f),
			G: lerp8(a.G, b.G, f),
			B: lerp8(a.B, b.B, f),
			A: 255,
		}
	}
	return color.RGBA{stops[0].R, stops[0].G, stops[0].B, 255}
}

func lerp8(a, b uint8, f float64) uint8 {
	return uint8(float64(a) + (float64(b)-float64(a))*f + 0.5)
}

// At samples the palette at t, wrapping t into [0,1).
func (p *Palette) At(t float64) color.RGBA {
	t -= math.Floor(t)
	i := int(t * lutSize)
	if i < 0 {
		i = 0
	} else if i >= lutSize {
		i = lutSize - 1
	}
	return p.lut[i]
}

// Index samples the palette by raw table index, wrapping. Used by the
// colouring hot loop, which precomputes the index arithmetic itself.
func (p *Palette) Index(i int) color.RGBA {
	i %= lutSize
	if i < 0 {
		i += lutSize
	}
	return p.lut[i]
}

// Size reports the lookup table resolution.
func (p *Palette) Size() int { return lutSize }

var black = color.RGBA{0, 0, 0, 255}

// Presets are the built-in palettes, in menu order.
var Presets = []*Palette{
	// The Ultra Fractal default ramp, by far the most recognisable modern
	// fractal palette: deep blue, white, amber, near-black.
	New("Ultra", black, []Stop{
		{0.00, 0, 7, 100},
		{0.16, 32, 107, 203},
		{0.42, 237, 255, 255},
		{0.6425, 255, 170, 0},
		{0.8575, 0, 2, 0},
	}),
	// An approximation of Fractint's default 256-colour map: cyan/blue
	// through red and yellow.
	New("Fractint", black, []Stop{
		{0.00, 0, 0, 0},
		{0.10, 0, 40, 120},
		{0.25, 0, 160, 220},
		{0.40, 220, 240, 255},
		{0.55, 230, 60, 20},
		{0.70, 250, 200, 40},
		{0.85, 120, 20, 90},
	}),
	New("Fire", black, []Stop{
		{0.00, 0, 0, 0},
		{0.20, 120, 20, 0},
		{0.45, 240, 90, 10},
		{0.65, 255, 200, 60},
		{0.80, 255, 255, 220},
		{0.92, 90, 30, 10},
	}),
	New("Ice", black, []Stop{
		{0.00, 4, 8, 26},
		{0.22, 20, 70, 130},
		{0.45, 90, 180, 230},
		{0.65, 220, 245, 255},
		{0.85, 40, 90, 150},
	}),
	New("Rainbow", black, []Stop{
		{0.000, 255, 0, 0},
		{0.166, 255, 200, 0},
		{0.333, 0, 220, 60},
		{0.500, 0, 200, 220},
		{0.666, 30, 60, 230},
		{0.833, 200, 0, 200},
	}),
	New("Electric", black, []Stop{
		{0.00, 10, 0, 30},
		{0.25, 90, 0, 190},
		{0.50, 0, 220, 255},
		{0.62, 240, 255, 255},
		{0.80, 30, 90, 200},
	}),
	New("Sunset", black, []Stop{
		{0.00, 25, 10, 60},
		{0.20, 120, 30, 110},
		{0.42, 235, 90, 90},
		{0.60, 255, 180, 90},
		{0.78, 255, 240, 200},
		{0.90, 70, 25, 80},
	}),
	New("Grayscale", black, []Stop{
		{0.00, 0, 0, 0},
		{0.50, 255, 255, 255},
	}),
}

// ByName returns a preset by name, or the first preset when unknown.
func ByName(name string) *Palette {
	for _, p := range Presets {
		if p.Name == name {
			return p
		}
	}
	return Presets[0]
}

// LoadMap reads a Fractint .map file from disk.
func LoadMap(path string) (*Palette, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return ParseMap(name, b)
}

// ParseMap parses the Fractint .map format: a list of "red green blue" lines
// with each component in 0-255. Entry 0 is the interior colour and the rest
// becomes the ramp, which is then treated as cyclic like any other palette.
func ParseMap(name string, b []byte) (*Palette, error) {
	var raw [][3]uint8
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		var c [3]uint8
		ok := true
		for i := range 3 {
			v, err := strconv.Atoi(fields[i])
			if err != nil {
				ok = false
				break
			}
			c[i] = uint8(max(0, min(255, v)))
		}
		if ok {
			raw = append(raw, c)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("palette: %s contains no colour entries", name)
	}

	inside := color.RGBA{raw[0][0], raw[0][1], raw[0][2], 255}
	ramp := raw[1:]
	if len(ramp) == 0 {
		ramp = raw
	}
	// A 256-entry map would give 256 control points, which is more resolution
	// than the gradient needs and makes the stop editor unusable. Thin it to a
	// manageable number of evenly spaced stops.
	const maxStops = 32
	stride := 1
	if len(ramp) > maxStops {
		stride = (len(ramp) + maxStops - 1) / maxStops
	}
	var stops []Stop
	for i := 0; i < len(ramp); i += stride {
		stops = append(stops, Stop{
			Pos: float64(i) / float64(len(ramp)),
			R:   ramp[i][0], G: ramp[i][1], B: ramp[i][2],
		})
	}
	return New(name, inside, stops), nil
}
