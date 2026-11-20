// Package engine is the alda-player sequencer. It applies OSC packets from
// the Alda client to a millisecond clock and dispatches the scheduled MIDI
// events to a synthesizer while audio is rendered.
package engine

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"alda-player/osc"
)

// LevelTrace logs every received OSC message.
const LevelTrace = slog.LevelDebug - 4

// Synth receives MIDI messages and renders audio.
// *meltysynth.Synthesizer implements it.
type Synth interface {
	ProcessMidiMessage(channel, command, data1, data2 int32)
	NoteOffAll(immediate bool)
	Render(left, right []float32)
}

// Result summarizes what a packet asked for.
type Result struct {
	Ping      bool          // /ping was received
	Play      bool          // /system/play was received
	Scheduled bool          // MIDI events were scheduled
	Until     time.Duration // from now until the last scheduled event
	Finished  bool          // playback finished immediately
	Shutdown  bool          // shut down immediately
}

// Engine is safe for concurrent use.
type Engine struct {
	log      *slog.Logger
	rate     int64
	finished chan struct{}
	shutdown chan struct{}

	mu      sync.Mutex
	dec     osc.Decoder
	b       batch
	history []event // MIDI and tempo events in scheduling order, for export
	pending queue
	seq     uint64
	pos     int64 // clock position in frames
	playing bool
	silence bool            // release sounding notes on the next render
	tracks  map[int32]int64 // track number → ms where its next events start
	bases   map[int32]int64 // per-packet scratch: track → base offset
	ends    map[int32]int64 // per-packet scratch: track → end of last note
}

// New returns a stopped engine whose clock runs at sampleRate frames per
// second.
func New(sampleRate int, log *slog.Logger) *Engine {
	return &Engine{
		log:      log,
		rate:     int64(sampleRate),
		finished: make(chan struct{}, 1),
		shutdown: make(chan struct{}, 1),
		tracks:   make(map[int32]int64),
		bases:    make(map[int32]int64),
		ends:     make(map[int32]int64),
	}
}

// Finished receives when a scheduled playback-finished point is reached.
func (e *Engine) Finished() <-chan struct{} { return e.finished }

// Shutdown receives when a scheduled shutdown point is reached.
func (e *Engine) Shutdown() <-chan struct{} { return e.shutdown }

// Handle applies one OSC packet (a message or a bundle). As in the original
// player, all messages of a packet are parsed first and then applied in a
// fixed order: stop/clear/offset, tempo, track events, export, scheduled
// shutdown and playback-finished, play.
func (e *Engine) Handle(packet []byte) (Result, error) {
	e.mu.Lock()
	e.b.reset()
	if err := e.dec.Decode(packet, e.parse); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	res := e.apply()
	exports := e.b.exports
	var snapshot []event
	if len(exports) > 0 {
		snapshot = slices.Clone(e.history)
	}
	e.mu.Unlock()

	var errs []error
	for _, path := range exports {
		if err := writeMIDIFile(path, snapshot); err != nil {
			errs = append(errs, err)
			continue
		}
		e.log.Info("exported MIDI file", "path", path)
	}
	return res, errors.Join(errs...)
}

// Render renders len(left) frames into left and right, advancing the clock
// while playing and dispatching due events to s.
func (e *Engine) Render(s Synth, left, right []float32) {
	e.render(s, len(left), left, right)
}

// Advance advances the clock by frames without producing audio. It drives
// the clock when no audio output is available.
func (e *Engine) Advance(frames int) {
	e.render(nil, frames, nil, nil)
}

func (e *Engine) render(s Synth, n int, left, right []float32) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.silence {
		if s != nil {
			s.NoteOffAll(false)
		}
		e.silence = false
	}
	for i := 0; i < n; {
		seg := n - i
		if e.playing {
			for len(e.pending) > 0 {
				at := e.pending[0].ms * e.rate / 1000
				if at > e.pos {
					seg = int(min(int64(seg), at-e.pos))
					break
				}
				e.dispatch(s, e.pending.pop())
			}
			e.pos += int64(seg)
		}
		if s != nil {
			s.Render(left[i:i+seg], right[i:i+seg])
		}
		i += seg
	}
}

func (e *Engine) dispatch(s Synth, ev event) {
	switch ev.kind {
	case kindFinished:
		notify(e.finished)
	case kindShutdown:
		notify(e.shutdown)
	}
	if s == nil {
		return
	}
	ch, d1, d2 := int32(ev.ch), int32(ev.d1), int32(ev.d2)
	switch ev.kind {
	case kindNoteOn:
		s.ProcessMidiMessage(ch, statusNoteOn, d1, d2)
	case kindNoteOff:
		s.ProcessMidiMessage(ch, statusNoteOff, d1, d2)
	case kindProgram:
		s.ProcessMidiMessage(ch, statusProgram, d1, 0)
	case kindControl:
		s.ProcessMidiMessage(ch, statusControl, d1, d2)
	}
}

func notify(c chan struct{}) {
	select {
	case c <- struct{}{}:
	default:
	}
}

func (e *Engine) nowMs() int64 { return e.pos * 1000 / e.rate }

func (e *Engine) apply() Result {
	b := &e.b
	now := e.nowMs()

	if b.stop {
		e.playing = false
		e.silence = true
	}
	if b.clear {
		clear(e.tracks)
	}
	if b.seek {
		e.seek(b.offset)
		now = b.offset
	}
	for _, t := range b.clearTracks {
		delete(e.tracks, t)
	}

	for _, t := range b.tempos {
		t.seq = e.next()
		e.history = append(e.history, t)
	}

	clear(e.bases)
	clear(e.ends)
	latest := int64(-1)
	for _, te := range b.events {
		base, ok := e.bases[te.track]
		if !ok {
			base = max(e.tracks[te.track], now)
			e.bases[te.track] = base
		}
		at := base + te.offset
		ev := event{ms: at, kind: te.kind, ch: te.ch, d1: te.d1, d2: te.d2}
		e.schedule(ev)
		latest = max(latest, at)
		if te.kind != kindNoteOn {
			continue
		}
		ev.kind = kindNoteOff
		ev.ms = at + te.audible
		e.schedule(ev)
		latest = max(latest, ev.ms)
		e.ends[te.track] = max(e.ends[te.track], at+te.duration)
	}
	for t, end := range e.ends {
		e.tracks[t] = end
	}
	if latest >= 0 {
		b.res.Scheduled = true
		b.res.Until = time.Duration(max(latest-now, 0)) * time.Millisecond
	}

	for _, off := range b.shutdownAt {
		e.schedule(event{ms: now + off, kind: kindShutdown})
	}
	for _, off := range b.finishedAt {
		e.schedule(event{ms: now + off, kind: kindFinished})
	}
	if b.res.Play {
		e.playing = true
	}
	return b.res
}

func (e *Engine) next() uint64 {
	e.seq++
	return e.seq
}

func (e *Engine) schedule(ev event) {
	ev.seq = e.next()
	if ev.isMIDI() {
		e.history = append(e.history, ev)
	}
	e.pending.push(ev)
}

// seek moves the clock to ms. MIDI events from ms onward are queued again;
// the last program and controller values before ms are re-sent so that
// channels sound as they would have at that point.
func (e *Engine) seek(ms int64) {
	e.pos = ms * e.rate / 1000
	e.silence = true

	keep := e.pending[:0]
	for _, ev := range e.pending {
		if !ev.isMIDI() {
			keep = append(keep, ev)
		}
	}
	var programs [16]*event
	var controls [16][128]*event
	for i := range e.history {
		ev := &e.history[i]
		switch {
		case ev.kind == kindTempo:
		case ev.ms >= ms:
			keep = append(keep, *ev)
		case ev.kind == kindProgram:
			programs[ev.ch] = ev
		case ev.kind == kindControl:
			controls[ev.ch][ev.d1] = ev
		}
	}
	chase := func(ev *event) {
		if ev != nil {
			c := *ev
			c.ms = ms
			keep = append(keep, c)
		}
	}
	for ch := range programs {
		chase(programs[ch])
		for _, ev := range controls[ch] {
			chase(ev)
		}
	}
	keep.init()
	e.pending = keep
}

type trackEvent struct {
	track    int32
	kind     kind
	ch       uint8
	d1, d2   uint8
	offset   int64
	duration int64 // kindNoteOn
	audible  int64 // kindNoteOn
}

// batch collects the parsed messages of one packet.
type batch struct {
	res         Result
	stop, clear bool
	seek        bool
	offset      int64
	clearTracks []int32
	tempos      []event
	events      []trackEvent
	exports     []string
	shutdownAt  []int64
	finishedAt  []int64
}

func (b *batch) reset() {
	*b = batch{
		clearTracks: b.clearTracks[:0],
		tempos:      b.tempos[:0],
		events:      b.events[:0],
		shutdownAt:  b.shutdownAt[:0],
		finishedAt:  b.finishedAt[:0],
	}
}

// parse adds one message to the batch. Invalid messages are logged and
// dropped, so it never fails.
func (e *Engine) parse(m *osc.Message) error {
	if e.log.Enabled(context.Background(), LevelTrace) {
		e.log.Log(context.Background(), LevelTrace, "received message",
			"address", string(m.Address), "args", formatArgs(m))
	}
	b := &e.b
	switch string(m.Address) {
	case "/ping":
		e.log.Debug("received ping")
		b.res.Ping = true
	case "/system/play":
		b.res.Play = true
	case "/system/stop":
		b.stop = true
	case "/system/clear":
		b.clear = true
	case "/system/offset":
		if e.check(m, "i") {
			b.seek = true
			b.offset = max(m.Args[0].Int, 0)
		}
	case "/system/tempo":
		if e.check(m, "if") {
			ms, bpm := max(m.Args[0].Int, 0), float32(m.Args[1].Float)
			if bpm <= 0 {
				e.log.Warn("ignoring non-positive tempo", "bpm", bpm)
				break
			}
			b.tempos = append(b.tempos, event{ms: ms, kind: kindTempo, bpm: bpm})
		}
	case "/system/midi/export":
		if e.check(m, "s") {
			b.exports = append(b.exports, string(m.Args[0].Bytes))
		}
	case "/system/shutdown":
		if e.check(m, "i") {
			if off := m.Args[0].Int; off > 0 {
				b.shutdownAt = append(b.shutdownAt, off)
			} else {
				b.res.Shutdown = true
			}
		}
	case "/system/playback-finished":
		if e.check(m, "i") {
			if off := m.Args[0].Int; off > 0 {
				b.finishedAt = append(b.finishedAt, off)
			} else {
				b.res.Finished = true
			}
		}
	default:
		if rest, ok := bytes.CutPrefix(m.Address, []byte("/track/")); ok {
			e.parseTrack(m, rest)
		} else if bytes.HasPrefix(m.Address, []byte("/pattern/")) {
			e.log.Warn("patterns are not supported", "address", string(m.Address))
		} else {
			e.log.Warn("unrecognized address", "address", string(m.Address))
		}
	}
	return nil
}

func (e *Engine) parseTrack(m *osc.Message, rest []byte) {
	num, op, _ := bytes.Cut(rest, []byte("/"))
	track, ok := parseTrackNumber(num)
	if !ok {
		e.log.Warn("invalid track number", "address", string(m.Address))
		return
	}
	b := &e.b
	switch string(op) {
	case "midi/note":
		if !e.check(m, "iiiiii") || !e.midi(m, 0, 2, 5) {
			return
		}
		a := m.Args
		b.events = append(b.events, trackEvent{
			track:    track,
			kind:     kindNoteOn,
			ch:       uint8(a[0].Int),
			d1:       uint8(a[2].Int),
			d2:       uint8(a[5].Int),
			offset:   max(a[1].Int, 0),
			duration: max(a[3].Int, 0),
			audible:  max(a[4].Int, 0),
		})
	case "midi/patch":
		e.trackControl(m, track, kindProgram, 0)
	case "midi/volume":
		e.trackControl(m, track, kindControl, ccExpression)
	case "midi/panning":
		e.trackControl(m, track, kindControl, ccPanning)
	case "clear":
		b.clearTracks = append(b.clearTracks, track)
	case "pattern", "pattern-loop", "finish-loop":
		e.log.Warn("patterns are not supported", "address", string(m.Address))
	default:
		e.log.Warn("unrecognized address", "address", string(m.Address))
	}
}

// trackControl adds a program change (cc unused) or a control change whose
// value is the message's third argument.
func (e *Engine) trackControl(m *osc.Message, track int32, k kind, cc uint8) {
	if !e.check(m, "iii") || !e.midi(m, 0, 2) {
		return
	}
	te := trackEvent{
		track:  track,
		kind:   k,
		ch:     uint8(m.Args[0].Int),
		offset: max(m.Args[1].Int, 0),
	}
	if k == kindProgram {
		te.d1 = uint8(m.Args[2].Int)
	} else {
		te.d1, te.d2 = cc, uint8(m.Args[2].Int)
	}
	e.b.events = append(e.b.events, te)
}

func (e *Engine) check(m *osc.Message, tags string) bool {
	if string(m.Tags) == tags {
		return true
	}
	e.log.Warn("invalid arguments", "address", string(m.Address),
		"got", string(m.Tags), "want", tags)
	return false
}

// midi reports whether the argument at channel is a MIDI channel and the
// arguments at data are 7-bit MIDI data values.
func (e *Engine) midi(m *osc.Message, channel int, data ...int) bool {
	ok := m.Args[channel].Int >= 0 && m.Args[channel].Int < 16
	for _, i := range data {
		ok = ok && m.Args[i].Int >= 0 && m.Args[i].Int < 128
	}
	if !ok {
		e.log.Warn("MIDI value out of range", "address", string(m.Address), "args", formatArgs(m))
	}
	return ok
}

func parseTrackNumber(b []byte) (int32, bool) {
	if len(b) == 0 || len(b) > 9 {
		return 0, false
	}
	var n int32
	for _, c := range b {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int32(c-'0')
	}
	return n, true
}

func formatArgs(m *osc.Message) []any {
	args := make([]any, len(m.Args))
	for i, a := range m.Args {
		switch a.Type {
		case 'f', 'd':
			args[i] = a.Float
		case 's', 'S':
			args[i] = string(a.Bytes)
		default:
			args[i] = a.Int
		}
	}
	return args
}
