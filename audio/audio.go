// Package audio plays an engine through the system audio device, using a
// SoundFont synthesizer.
package audio

import (
	"cmp"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/ebitengine/oto/v3"
	"github.com/sinshu/go-meltysynth/meltysynth"

	"alda-player/engine"
)

// SampleRate is the engine clock and output rate in frames per second.
const SampleRate = 48000

// Run plays eng with the SoundFont at path until ctx is done. If audio
// cannot be set up, it logs why and drives the engine's clock in real time
// without sound, so that scheduled events such as shutdown still happen.
func Run(ctx context.Context, eng *engine.Engine, path string, log *slog.Logger) {
	octx, p, err := open(ctx, eng, path, log)
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		log.Error("audio unavailable, playing silently", "err", err)
		runClock(ctx, eng)
		return
	}
	defer func() {
		if err := p.Close(); err != nil {
			log.Warn("closing audio output", "err", err)
		}
	}()
	// oto reports device failures asynchronously.
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := cmp.Or(octx.Err(), p.Err()); err != nil {
				log.Error("audio output failed, playing silently", "err", err)
				runClock(ctx, eng)
				return
			}
		}
	}
}

func open(ctx context.Context, eng *engine.Engine, path string, log *slog.Logger) (*oto.Context, *oto.Player, error) {
	if path == "" {
		return nil, nil, errors.New("no SoundFont found")
	}
	start := time.Now()
	synth, err := loadSynth(path)
	if err != nil {
		return nil, nil, err
	}
	log.Info("loaded SoundFont", "path", path, "took", time.Since(start).Round(time.Millisecond))

	octx, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate:      SampleRate,
		ChannelCount:    2,
		Format:          oto.FormatFloat32LE,
		ApplicationName: "alda-player",
	})
	if err != nil {
		return nil, nil, fmt.Errorf("opening audio device: %w", err)
	}
	select {
	case <-ready:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	if err := octx.Err(); err != nil {
		return nil, nil, err
	}
	p := octx.NewPlayer(&stream{eng: eng, synth: synth})
	p.Play()
	log.Info("audio output started")
	return octx, p, nil
}

func loadSynth(path string) (*meltysynth.Synthesizer, error) {
	data, err := mapFile(path)
	if err != nil {
		return nil, fmt.Errorf("loading SoundFont: %w", err)
	}
	sf, err := meltysynth.NewSoundFontFromBytes(data)
	if err != nil {
		return nil, fmt.Errorf("loading SoundFont %s: %w", path, err)
	}
	s, err := meltysynth.NewSynthesizer(sf, meltysynth.NewSynthesizerSettings(SampleRate))
	if err != nil {
		return nil, fmt.Errorf("creating synthesizer: %w", err)
	}
	return s, nil
}

// stream renders the engine as interleaved little-endian float32 stereo.
type stream struct {
	eng         *engine.Engine
	synth       engine.Synth
	left, right []float32
}

func (s *stream) Read(p []byte) (int, error) {
	n := len(p) / 8
	if cap(s.left) < n {
		s.left = make([]float32, n)
		s.right = make([]float32, n)
	}
	l, r := s.left[:n], s.right[:n]
	s.eng.Render(s.synth, l, r)
	for i := range n {
		binary.LittleEndian.PutUint32(p[8*i:], math.Float32bits(l[i]))
		binary.LittleEndian.PutUint32(p[8*i+4:], math.Float32bits(r[i]))
	}
	return n * 8, nil
}

// runClock advances the engine in real time until ctx is done.
func runClock(ctx context.Context, eng *engine.Engine) {
	t := time.NewTicker(10 * time.Millisecond)
	defer t.Stop()
	start := time.Now()
	var done int64
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			target := int64(now.Sub(start)) * SampleRate / int64(time.Second)
			eng.Advance(int(target - done))
			done = target
		}
	}
}
