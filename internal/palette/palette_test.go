package palette

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPresetsAreSeamless is the property colour cycling depends on: the ramp
// must join up at t = 1, with no jump back to t = 0.
func TestPresetsAreSeamless(t *testing.T) {
	for _, p := range Presets {
		a := p.At(1 - 1.0/float64(lutSize))
		b := p.At(0)
		if d := maxDiff(a.R, b.R, a.G, b.G, a.B, b.B); d > 12 {
			t.Errorf("%s: seam at t=1 jumps by %d", p.Name, d)
		}
	}
}

// TestPresetsAreContinuous walks each ramp looking for a step larger than a
// gradient should ever take, which would show up as a hard edge in the image.
func TestPresetsAreContinuous(t *testing.T) {
	for _, p := range Presets {
		prev := p.At(0)
		for i := 1; i < lutSize; i++ {
			c := p.Index(i)
			if d := maxDiff(c.R, prev.R, c.G, prev.G, c.B, prev.B); d > 12 {
				t.Errorf("%s: step of %d at index %d", p.Name, d, i)
				break
			}
			prev = c
		}
	}
}

func TestAtWrapsAndIsOpaque(t *testing.T) {
	p := Presets[0]
	for _, tv := range []float64{-2.25, -0.25, 0, 0.25, 1, 1.25, 7.5} {
		if p.At(tv).A != 255 {
			t.Errorf("At(%v) is not opaque", tv)
		}
	}
	// Wrapping means these must be identical, not merely close.
	if p.At(0.3) != p.At(1.3) || p.At(0.3) != p.At(-0.7) {
		t.Error("At does not wrap into [0,1)")
	}
	if p.Index(5) != p.Index(5+lutSize) || p.Index(-1) != p.Index(lutSize-1) {
		t.Error("Index does not wrap")
	}
}

func TestByNameFallsBack(t *testing.T) {
	if ByName("Fire").Name != "Fire" {
		t.Error("ByName missed a preset")
	}
	if ByName("no such palette") != Presets[0] {
		t.Error("ByName should fall back to the first preset")
	}
}

// TestLoadMap covers the Fractint .map format: entry 0 is the interior colour
// and the rest is the ramp.
func TestLoadMap(t *testing.T) {
	var b strings.Builder
	b.WriteString("; a comment line\n")
	b.WriteString("10 20 30\n") // interior
	for i := 0; i < 255; i++ {
		fmt.Fprintf(&b, "%d %d %d\n", i, 255-i, (i*3)%256)
	}
	path := filepath.Join(t.TempDir(), "test.map")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := LoadMap(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "test" {
		t.Errorf("name = %q, want %q", p.Name, "test")
	}
	if p.Inside.R != 10 || p.Inside.G != 20 || p.Inside.B != 30 {
		t.Errorf("inside colour = %v", p.Inside)
	}
	// The first ramp entry is 0 255 0; sampling at t=0 must land on it.
	if c := p.At(0); c.R != 0 || c.G != 255 {
		t.Errorf("At(0) = %v, want the first ramp entry", c)
	}
}

func TestLoadMapRejectsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.map")
	if err := os.WriteFile(path, []byte("; nothing here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMap(path); err == nil {
		t.Error("expected an error for a map with no colours")
	}
	if _, err := LoadMap(filepath.Join(t.TempDir(), "missing.map")); err == nil {
		t.Error("expected an error for a missing file")
	}
}

func maxDiff(vals ...uint8) int {
	d := 0
	for i := 0; i+1 < len(vals); i += 2 {
		x := int(vals[i]) - int(vals[i+1])
		if x < 0 {
			x = -x
		}
		if x > d {
			d = x
		}
	}
	return d
}
