package audio

import (
	"bufio"
	"cmp"
	"os"
	"slices"
	"testing"

	"github.com/sinshu/go-meltysynth/meltysynth"
)

func testSoundFont(tb testing.TB) string {
	tb.Helper()
	path := cmp.Or(os.Getenv("ALDA_SOUNDFONT"), FindSoundFont())
	if path == "" {
		tb.Skip("no SoundFont available; set ALDA_SOUNDFONT")
	}
	return path
}

func TestMappedSoundFontMatchesReader(t *testing.T) {
	path := testSoundFont(t)
	data, err := mapFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mapped, err := meltysynth.NewSoundFontFromBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	read, err := meltysynth.NewSoundFont(bufio.NewReader(f))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(mapped.WaveData, read.WaveData) {
		t.Error("wave data differs")
	}
	if len(mapped.Presets) != len(read.Presets) || len(mapped.SampleHeaders) != len(read.SampleHeaders) {
		t.Errorf("presets %d/%d, samples %d/%d", len(mapped.Presets), len(read.Presets),
			len(mapped.SampleHeaders), len(read.SampleHeaders))
	}
}

func BenchmarkLoadSynth(b *testing.B) {
	path := testSoundFont(b)
	for b.Loop() {
		if _, err := loadSynth(path); err != nil {
			b.Fatal(err)
		}
	}
}
