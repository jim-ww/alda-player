module github.com/jim-ww/alda-player

go 1.26.0

require (
	github.com/daveyarwood/go-osc v0.0.0-20200229013406-0675d0af5e0b
	github.com/ebitengine/oto/v3 v3.5.1
	github.com/google/go-cmp v0.7.0
	github.com/sinshu/go-meltysynth v0.0.0-20230205031334-05d311382fc4
	gitlab.com/gomidi/midi/v2 v2.3.24
	golang.org/x/sys v0.47.0
)

require (
	github.com/ebitengine/purego v0.11.0 // indirect
	github.com/jfreymuth/pulse v0.1.3 // indirect
)

replace github.com/sinshu/go-meltysynth => ./third_party/go-meltysynth
