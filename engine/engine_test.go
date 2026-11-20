package engine

import (
	"fmt"
	"io"
	"log/slog"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/daveyarwood/go-osc/osc"
	"github.com/google/go-cmp/cmp"
	"gitlab.com/gomidi/midi/v2"
	"gitlab.com/gomidi/midi/v2/smf"
)

// rate makes one frame one millisecond.
const rate = 1000

type midiMsg struct {
	At              int
	Ch, Cmd, D1, D2 int32
}

// fakeSynth records MIDI messages with the frame at which they arrived.
type fakeSynth struct {
	frames   int
	msgs     []midiMsg
	silenced int
}

func (s *fakeSynth) ProcessMidiMessage(ch, cmd, d1, d2 int32) {
	s.msgs = append(s.msgs, midiMsg{s.frames, ch, cmd, d1, d2})
}
func (s *fakeSynth) NoteOffAll(bool)       { s.silenced++ }
func (s *fakeSynth) Render(l, _ []float32) { s.frames += len(l) }

func newEngine() *Engine {
	return New(rate, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func msg(addr string, args ...any) *osc.Message {
	m := osc.NewMessage(addr)
	for _, a := range args {
		if i, ok := a.(int); ok {
			a = int32(i)
		}
		m.Append(a)
	}
	return m
}

func bundle(msgs ...*osc.Message) *osc.Bundle {
	b := osc.NewBundle(time.Now())
	for _, m := range msgs {
		b.Append(m)
	}
	return b
}

func handle(t *testing.T, e *Engine, p osc.Packet) Result {
	t.Helper()
	b, err := p.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	return res
}

func render(e *Engine, s *fakeSynth, frames int) {
	buf := make([]float32, frames)
	e.Render(s, buf, buf)
}

func note(track, ch, off, key, dur, audible, vel int) *osc.Message {
	return msg(fmt.Sprintf("/track/%d/midi/note", track), ch, off, key, dur, audible, vel)
}

func received(c <-chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

func TestOneOffScore(t *testing.T) {
	e := newEngine()
	s := &fakeSynth{}
	res := handle(t, e, bundle(
		msg("/system/tempo", 0, float32(120)),
		msg("/track/1/midi/patch", 0, 0, 40),
		msg("/track/1/midi/volume", 0, 0, 100),
		msg("/track/1/midi/panning", 0, 0, 64),
		note(1, 0, 0, 60, 500, 450, 90),
		note(1, 0, 500, 62, 500, 450, 90),
		note(2, 9, 250, 36, 250, 250, 127),
		msg("/system/play"),
		msg("/system/playback-finished", 950),
		msg("/system/shutdown", 10950),
	))
	want := Result{Play: true, Scheduled: true, Until: 950 * time.Millisecond}
	if res != want {
		t.Errorf("Result = %+v, want %+v", res, want)
	}

	render(e, s, 950)
	if received(e.Finished()) {
		t.Fatal("finished too early")
	}
	render(e, s, 1)
	if !received(e.Finished()) {
		t.Fatal("not finished at 950 ms")
	}
	wantMsgs := []midiMsg{
		{0, 0, statusProgram, 40, 0},
		{0, 0, statusControl, ccExpression, 100},
		{0, 0, statusControl, ccPanning, 64},
		{0, 0, statusNoteOn, 60, 90},
		{250, 9, statusNoteOn, 36, 127},
		{450, 0, statusNoteOff, 60, 90},
		{500, 0, statusNoteOn, 62, 90},
		{500, 9, statusNoteOff, 36, 127},
		{950, 0, statusNoteOff, 62, 90},
	}
	if diff := cmp.Diff(wantMsgs, s.msgs); diff != "" {
		t.Errorf("MIDI mismatch (-want +got):\n%s", diff)
	}

	render(e, s, 9999)
	if received(e.Shutdown()) {
		t.Fatal("shut down too early")
	}
	render(e, s, 1)
	if !received(e.Shutdown()) {
		t.Fatal("not shut down at 10950 ms")
	}
}

func TestNothingPlaysBeforePlay(t *testing.T) {
	e := newEngine()
	s := &fakeSynth{}
	handle(t, e, note(1, 0, 0, 60, 100, 100, 100))
	render(e, s, 1000)
	if len(s.msgs) != 0 {
		t.Fatalf("got %v before play", s.msgs)
	}
	handle(t, e, msg("/system/play"))
	render(e, s, 101)
	if len(s.msgs) != 2 {
		t.Fatalf("got %v after play, want note on and off", s.msgs)
	}
}

func TestTrackPacketsConcatenate(t *testing.T) {
	e := newEngine()
	s := &fakeSynth{}
	// Ends at 1000 (duration), although audible only until 800.
	handle(t, e, bundle(note(1, 0, 0, 60, 1000, 800, 100), msg("/system/play")))
	render(e, s, 200)
	handle(t, e, bundle(note(1, 0, 0, 62, 100, 100, 100), note(2, 1, 0, 64, 100, 100, 100)))
	render(e, s, 1000)
	var ons []midiMsg
	for _, m := range s.msgs {
		if m.Cmd == statusNoteOn {
			ons = append(ons, m)
		}
	}
	want := []midiMsg{
		{0, 0, statusNoteOn, 60, 100},
		{200, 1, statusNoteOn, 64, 100}, // new track starts now
		{1000, 0, statusNoteOn, 62, 100},
	}
	if diff := cmp.Diff(want, ons); diff != "" {
		t.Errorf("note on mismatch (-want +got):\n%s", diff)
	}
}

func TestStopAndResume(t *testing.T) {
	e := newEngine()
	s := &fakeSynth{}
	handle(t, e, bundle(note(1, 0, 0, 60, 100, 100, 100), note(1, 0, 100, 62, 100, 100, 100), msg("/system/play")))
	render(e, s, 50)
	handle(t, e, msg("/system/stop"))
	render(e, s, 1000)
	if s.silenced != 1 {
		t.Errorf("NoteOffAll called %d times, want 1", s.silenced)
	}
	if len(s.msgs) != 1 {
		t.Fatalf("got %v while stopped", s.msgs)
	}
	handle(t, e, msg("/system/play"))
	render(e, s, 51)
	if got := s.msgs[len(s.msgs)-1]; got != (midiMsg{1100, 0, statusNoteOn, 62, 100}) {
		t.Errorf("after resume got %+v, want second note 50 ms later", got)
	}
}

func TestSeek(t *testing.T) {
	e := newEngine()
	s := &fakeSynth{}
	handle(t, e, bundle(
		msg("/track/1/midi/patch", 0, 0, 10),
		note(1, 0, 0, 60, 500, 500, 100),
		msg("/track/1/midi/patch", 0, 500, 20),
		note(1, 0, 500, 62, 500, 500, 100),
		note(1, 0, 1000, 64, 500, 500, 100),
	))
	handle(t, e, bundle(msg("/system/offset", 600), msg("/system/play")))
	render(e, s, 1000)
	want := []midiMsg{
		{0, 0, statusProgram, 20, 0}, // chased
		{400, 0, statusNoteOff, 62, 100},
		{400, 0, statusNoteOn, 64, 100},
		{900, 0, statusNoteOff, 64, 100},
	}
	if diff := cmp.Diff(want, s.msgs); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestImmediateActions(t *testing.T) {
	e := newEngine()
	tests := []struct {
		packet osc.Packet
		want   Result
	}{
		{msg("/ping"), Result{Ping: true}},
		{msg("/system/shutdown", 0), Result{Shutdown: true}},
		{msg("/system/playback-finished", 0), Result{Finished: true}},
	}
	for _, tt := range tests {
		if got := handle(t, e, tt.packet); got != tt.want {
			t.Errorf("%v: got %+v, want %+v", tt.packet, got, tt.want)
		}
	}
}

func TestInvalidMessagesIgnored(t *testing.T) {
	e := newEngine()
	s := &fakeSynth{}
	handle(t, e, bundle(
		msg("/track/1/midi/note", 0, 0, 60), // too few args
		msg("/track/x/midi/note", 0, 0, 60, 1, 1, 1),
		note(1, 16, 0, 60, 1, 1, 1), // channel out of range
		note(1, 0, 0, 128, 1, 1, 1), // key out of range
		msg("/track/1/pattern", 0, 0, "p", 2),
		msg("/pattern/p/midi/note", 0, 60, 1, 1, 1),
		msg("/nope"),
		msg("/system/play"),
	))
	render(e, s, 100)
	if len(s.msgs) != 0 {
		t.Fatalf("got %v", s.msgs)
	}
}

func TestMalformedPacket(t *testing.T) {
	if _, err := newEngine().Handle([]byte("garbage")); err == nil {
		t.Fatal("want error")
	}
}

func TestExport(t *testing.T) {
	e := newEngine()
	path := filepath.Join(t.TempDir(), "out.mid")
	handle(t, e, msg("/system/play"))
	render(e, &fakeSynth{}, 250) // export shifts the start back to tick 0
	handle(t, e, bundle(
		msg("/system/tempo", 0, float32(60)),
		msg("/track/1/midi/patch", 0, 0, 5),
		note(1, 0, 0, 60, 1000, 900, 100),
		note(1, 0, 1000, 64, 1000, 900, 80),
		msg("/system/midi/export", path),
	))

	f, err := smf.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.Format() != 0 || f.TimeFormat != smf.MetricTicks(resolution) || len(f.Tracks) != 1 {
		t.Fatalf("format %d, time format %v, %d tracks", f.Format(), f.TimeFormat, len(f.Tracks))
	}
	type ev struct {
		Tick int64
		Msg  string
	}
	var got []ev
	var tick int64
	for _, e := range f.Tracks[0] {
		tick += int64(e.Delta)
		got = append(got, ev{tick, e.Message.String()})
	}
	at60 := func(ms int64) int64 { return int64(math.Round(float64(ms) * resolution / 1000)) }
	want := []ev{
		{0, smf.MetaTempo(60).String()},
		{0, midi.ProgramChange(0, 5).String()},
		{0, midi.NoteOn(0, 60, 100).String()},
		{at60(900), midi.NoteOffVelocity(0, 60, 100).String()},
		{at60(1000), midi.NoteOn(0, 64, 80).String()},
		{at60(1900), midi.NoteOffVelocity(0, 64, 80).String()},
		{at60(1900), smf.EOT.String()},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestTempoMap(t *testing.T) {
	m := newTempoMap([]event{
		{ms: 0, kind: kindTempo, bpm: 60},
		{ms: 1000, kind: kindTempo, bpm: 90},
		{ms: 1000, kind: kindTempo, bpm: 240},
	})
	tests := []struct {
		ms   float64
		want float64
	}{
		{0, 0},
		{500, 64},
		{1000, 128},
		{1250, 256}, // 240 bpm: one beat per 250 ms
	}
	for _, tt := range tests {
		if got := m.at(tt.ms); got != tt.want {
			t.Errorf("at(%v) = %v, want %v", tt.ms, got, tt.want)
		}
	}
}

func BenchmarkHandleAndRender(b *testing.B) {
	bd := osc.NewBundle(time.Now())
	for i := range 1000 {
		bd.Append(note(1, 0, i*100, 60, 100, 90, 100))
	}
	bd.Append(msg("/system/play"))
	p, _ := bd.MarshalBinary()
	buf := make([]float32, 512)
	s := &fakeSynth{}
	b.ReportAllocs()
	for b.Loop() {
		e := newEngine()
		if _, err := e.Handle(p); err != nil {
			b.Fatal(err)
		}
		for range 200 {
			e.Render(s, buf, buf)
		}
		s.msgs = s.msgs[:0]
	}
}
