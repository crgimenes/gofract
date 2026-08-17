package app

import (
	"slices"

	"github.com/vmaciel/gofract/internal/palette"
	"github.com/vmaciel/gofract/internal/params"
)

// forkPalette copies the palette currently in use into an editable custom ramp.
// Presets stay immutable: editing always works on a copy, so switching back to
// a preset always gets the original.
func (a *App) forkPalette() {
	src := a.st.Color.Ramp()
	a.hist.Push(a.st)
	a.st.Color.Stops = palette.StopsOf(src)
	a.st.Color.Palette = params.CustomPalette
	a.stopSel = 0
	a.hist.Push(a.st)
	a.needColor = true
	a.setStatus("Editing a copy of %s (%d stops)", src.Name, len(a.st.Color.Stops))
}

// addStop inserts a control point midway between the selected stop and the
// next one, taking the colour the gradient already has there — so adding a stop
// never changes the ramp, it only gives you a new handle on it.
func (a *App) addStop() {
	stops := a.st.Color.Stops
	if len(stops) == 0 {
		return
	}
	i := min(max(a.stopSel, 0), len(stops)-1)
	next := stops[0].Pos + 1 // wrap past the end
	if i+1 < len(stops) {
		next = stops[i+1].Pos
	}
	mid := (stops[i].Pos + next) / 2
	if mid >= 1 {
		mid -= 1
	}
	c := a.st.Color.Ramp().At(mid)

	a.hist.Push(a.st)
	a.st.Color.Stops = slices.Insert(slices.Clone(stops), i+1,
		palette.Stop{Pos: mid, R: c.R, G: c.G, B: c.B})
	a.sortStops()
	// Keep the new stop selected wherever the sort put it.
	for j, s := range a.st.Color.Stops {
		if s.Pos == mid {
			a.stopSel = j
			break
		}
	}
	a.hist.Push(a.st)
	a.needColor = true
}

// deleteStop removes the selected control point. Two stops are the minimum a
// gradient can be built from, so the last pair is protected.
func (a *App) deleteStop() {
	if len(a.st.Color.Stops) <= 2 {
		a.setStatus("A gradient needs at least two stops")
		return
	}
	a.hist.Push(a.st)
	a.st.Color.Stops = slices.Delete(slices.Clone(a.st.Color.Stops), a.stopSel, a.stopSel+1)
	a.stopSel = min(a.stopSel, len(a.st.Color.Stops)-1)
	a.hist.Push(a.st)
	a.needColor = true
}

// reversePalette mirrors the ramp. Positions are reflected about the seam, so
// the cyclic gradient stays seamless.
func (a *App) reversePalette() {
	stops := slices.Clone(a.st.Color.Stops)
	if len(stops) == 0 {
		return
	}
	a.hist.Push(a.st)
	for i := range stops {
		stops[i].Pos = 1 - stops[i].Pos
		if stops[i].Pos >= 1 {
			stops[i].Pos -= 1
		}
	}
	a.st.Color.Stops = stops
	a.sortStops()
	a.hist.Push(a.st)
	a.needColor = true
}

// sortStops restores the ascending-position invariant that the gradient
// sampler relies on, following the stop the user was editing.
func (a *App) sortStops() {
	stops := a.st.Color.Stops
	if a.stopSel < 0 || a.stopSel >= len(stops) {
		slices.SortStableFunc(stops, byPos)
		return
	}
	sel := stops[a.stopSel]
	slices.SortStableFunc(stops, byPos)
	for i := range stops {
		if stops[i] == sel {
			a.stopSel = i
			break
		}
	}
}

func byPos(a, b palette.Stop) int {
	switch {
	case a.Pos < b.Pos:
		return -1
	case a.Pos > b.Pos:
		return 1
	}
	return 0
}
