package engine

import (
	"bufio"
	"cmp"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"

	"gitlab.com/gomidi/midi/v2"
	"gitlab.com/gomidi/midi/v2/smf"
)

// resolution is the exported file's ticks per quarter note.
const resolution = 128

// minBPM is the slowest tempo a set-tempo meta event can encode (its value
// is a 24-bit count of microseconds per quarter note).
const minBPM = 60e6 / 0xFFFFFF

// writeMIDIFile writes events as a type 0 MIDI file. The file is written
// next to path and renamed into place, so readers never see it partially
// written.
func writeMIDIFile(path string, events []event) (err error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".alda-export-*.mid")
	if err != nil {
		return fmt.Errorf("exporting MIDI: %w", err)
	}
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(f.Name())
			err = fmt.Errorf("exporting MIDI to %s: %w", path, err)
		}
	}()
	w := bufio.NewWriter(f)
	if err := writeMIDI(w, events); err != nil {
		return err
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if err := f.Chmod(0o644); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

type tempoPoint struct {
	ms, ticks, bpm float64
}

// tempoMap converts milliseconds to ticks. Tempo changes are applied in
// order of offset; of several at the same offset the last one wins.
type tempoMap []tempoPoint

func newTempoMap(events []event) tempoMap {
	var changes []event
	for _, ev := range events {
		if ev.kind == kindTempo {
			changes = append(changes, ev)
		}
	}
	slices.SortStableFunc(changes, func(a, b event) int { return cmp.Compare(a.ms, b.ms) })

	m := tempoMap{{bpm: 120}}
	for _, c := range changes {
		ms := float64(c.ms)
		m = append(m, tempoPoint{ms: ms, ticks: m.at(ms), bpm: float64(c.bpm)})
	}
	return m
}

// at returns the (fractional) tick at ms.
func (m tempoMap) at(ms float64) float64 {
	i, found := slices.BinarySearchFunc(m, ms, func(p tempoPoint, ms float64) int {
		return cmp.Compare(p.ms, ms)
	})
	if found {
		for i+1 < len(m) && m[i+1].ms == ms {
			i++
		}
	} else {
		i--
	}
	p := m[i]
	return p.ticks + (ms-p.ms)*p.bpm*resolution/60000
}

// writeMIDI writes events as a type 0 MIDI file. Like the original player,
// it shifts everything so that the first note or control change is at tick
// 0, which drops the silence before the score starts.
func writeMIDI(w io.Writer, events []event) error {
	tempo := newTempoMap(events)
	ticks := make([]int64, len(events))
	first := int64(math.MaxInt64)
	for i, ev := range events {
		ticks[i] = int64(math.Round(tempo.at(float64(ev.ms))))
		if ev.kind == kindNoteOn || ev.kind == kindControl {
			first = min(first, ticks[i])
		}
	}
	if first == math.MaxInt64 {
		first = 0
	}

	order := make([]int, len(events))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(ticks[a], ticks[b]) })

	var track smf.Track
	var prev int64
	for _, i := range order {
		ev := events[i]
		msg := message(ev)
		if msg == nil {
			continue
		}
		tick := max(ticks[i]-first, 0)
		track.Add(uint32(tick-prev), msg)
		prev = tick
	}
	track.Close(0)

	s := smf.New()
	s.TimeFormat = smf.MetricTicks(resolution)
	if err := s.Add(track); err != nil {
		return err
	}
	_, err := s.WriteTo(w)
	return err
}

func message(ev event) []byte {
	switch ev.kind {
	case kindNoteOn:
		return midi.NoteOn(ev.ch, ev.d1, ev.d2)
	case kindNoteOff:
		return midi.NoteOffVelocity(ev.ch, ev.d1, ev.d2)
	case kindProgram:
		return midi.ProgramChange(ev.ch, ev.d1)
	case kindControl:
		return midi.ControlChange(ev.ch, ev.d1, ev.d2)
	case kindTempo:
		if float64(ev.bpm) < minBPM {
			return nil
		}
		return smf.MetaTempo(float64(ev.bpm))
	}
	return nil
}
