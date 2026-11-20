# alda-player

A drop-in replacement for [Alda](https://alda.io)'s JVM player process,
written in pure Go. Use the upstream `alda` client without a JVM.

- Speaks the client's OSC-over-TCP protocol, state files and CLI
  (`alda-player info`, `alda-player run`); passes `alda doctor`.
- Synthesizes audio in-process from a SoundFont
  ([meltysynth](https://github.com/sinshu/go-meltysynth)) and plays it through
  PulseAudio/PipeWire or ALSA ([oto](https://github.com/ebitengine/oto)).
  No cgo, no FluidSynth.
- Exports MIDI files for `alda export`.
- Idle players use ~7 MB; the SoundFont is memory-mapped and shared.

Requires alda 2.3.0 or later.

## Install

### Nix

The overlay replaces the player in nixpkgs' `alda`. The client still comes
from the binary cache; only this player is built.

```nix
{
  inputs.alda-player.url = "github:jim-ww/alda-player";
  inputs.alda-player.inputs.nixpkgs.follows = "nixpkgs";

  # in your NixOS or home-manager configuration:
  nixpkgs.overlays = [ inputs.alda-player.overlays.default ];
  environment.systemPackages = [ pkgs.alda ];
}
```

Or try it: `nix run github:jim-ww/alda-player#alda -- play -c "piano: c8 d e f g2"`

### Go

```sh
v=$(ALDA_DISABLE_SPAWNING=yes alda version | tail -1 | cut -d' ' -f2)
go install -ldflags "-X main.version=$v" github.com/jim-ww/alda-player@latest
```

Put it on `PATH` ahead of any other `alda-player`.

## Configuration

| Variable | Meaning |
|---|---|
| `ALDA_SOUNDFONT` | SoundFont (`.sf2`) to play with. Also `alda-player run --soundfont`. Default: `default.sf2` or the first `*.sf2` in `$XDG_DATA_HOME` / `$XDG_DATA_DIRS` under `soundfonts/` or `sounds/sf2/`. The Nix package defaults to FluidR3. |
| `ALDA_PLAYER_VERSION` | Version to report. It must equal the client's version, or the client won't find the player. Normally set at build time. |

Patterns and loops from the OSC API are not supported; the alda client
doesn't use them.

## Support the project

**Monero (XMR)**

`83YGRqP8uHed6NeegZQeX9ccCxbzoRHHEEi7pTwk4aqdJZEVXXA6NWtetnsEM2v33zFBBt3Rp6DNhU9qhJEGPspU14yN8t7`

## License
GPL-3.0. Free to use, study, share, and modify — provided you keep the same freedoms for others.
