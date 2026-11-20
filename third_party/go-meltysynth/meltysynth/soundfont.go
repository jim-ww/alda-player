package meltysynth

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

type SoundFont struct {
	Info          *SoundFontInfo
	BitsPerSample int32
	WaveData      []int16
	SampleHeaders []*SampleHeader
	Presets       []*Preset
	Instruments   []*Instrument
}

func NewSoundFont(r io.Reader) (*SoundFont, error) {
	return newSoundFont(r)
}

// NewSoundFontFromBytes parses a SoundFont held in memory. On little-endian
// machines WaveData aliases data instead of copying it, so data must stay
// valid and unmodified while the SoundFont is in use. Loading a
// memory-mapped file this way takes milliseconds and shares the sample data
// between processes through the page cache.
func NewSoundFontFromBytes(data []byte) (*SoundFont, error) {
	return newSoundFont(&byteReader{data: data})
}

func newSoundFont(r io.Reader) (*SoundFont, error) {
	chunkId, err := readFourCC(r)
	if err != nil {
		return nil, err
	}
	if chunkId != "RIFF" {
		return nil, errors.New("the riff chunk was not found")
	}

	var size int32
	err = binary.Read(r, binary.LittleEndian, &size)
	if err != nil {
		return nil, err
	}

	var formType string
	formType, err = readFourCC(r)
	if err != nil {
		return nil, err
	}
	if formType != "sfbk" {
		return nil, fmt.Errorf(`the type of the riff chunk must be "sfbk", but was %q`, formType)
	}

	result := &SoundFont{}

	result.Info, err = newSoundFontInfo(r)
	if err != nil {
		return nil, err
	}

	var sampleData *soundFontSampleData
	sampleData, err = newSoundFontSampleData(r)
	if err != nil {
		return nil, err
	}
	result.BitsPerSample = sampleData.bitsPerSample
	result.WaveData = sampleData.samples

	var parameters *soundFontParameters
	parameters, err = newSoundFontParameters(r)
	if err != nil {
		return nil, err
	}

	result.SampleHeaders = parameters.sampleHeaders
	result.Presets = parameters.presets
	result.Instruments = parameters.instruments

	return result, nil
}
