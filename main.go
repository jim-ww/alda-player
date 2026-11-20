// Command alda-player is a drop-in replacement for the JVM player process
// of Alda (https://alda.io). The alda client spawns it and sends it scores
// as OSC messages over TCP; it plays them with a SoundFont synthesizer and
// exports them as MIDI files.
package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"alda-player/audio"
	"alda-player/engine"
	"alda-player/state"
)

// version must equal the alda client's version: the client only discovers
// players in the state directory of its own version. Set it at build time
// with -ldflags "-X main.version=2.3.2" or at run time with
// ALDA_PLAYER_VERSION.
var version = "dev"

const usage = `Usage: alda-player [-v|-vv] <command> [options]

A background process that plays Alda scores.

Commands:
  info    Print the version and log path
  run     Run the player process

Options:
  -v, --verbose        verbose output
  -vv, --very-verbose  very verbose output
`

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "alda-player:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	root := flag.NewFlagSet("alda-player", flag.ContinueOnError)
	root.SetOutput(stderr)
	root.Usage = func() { fmt.Fprint(stderr, usage) }
	var verbose, veryVerbose bool
	root.BoolVar(&verbose, "v", false, "")
	root.BoolVar(&verbose, "verbose", false, "")
	root.BoolVar(&veryVerbose, "vv", false, "")
	root.BoolVar(&veryVerbose, "very-verbose", false, "")
	if err := root.Parse(args); err != nil {
		return ignoreHelp(err)
	}

	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	cfg := config{
		version:  cmp.Or(os.Getenv("ALDA_PLAYER_VERSION"), version),
		cacheDir: filepath.Join(cache, "alda"),
		level:    parseLevel(verbose, veryVerbose),
	}

	switch cmd, cmdArgs := root.Arg(0), root.Args()[min(1, root.NArg()):]; cmd {
	case "info":
		fmt.Fprintf(stdout, "alda-player %s\nlog path: %s\n", cfg.version, filepath.Join(cfg.cacheDir, "logs"))
		return nil
	case "run":
		fs := flag.NewFlagSet("alda-player run", flag.ContinueOnError)
		fs.SetOutput(stderr)
		fs.IntVar(&cfg.port, "p", 0, "")
		fs.IntVar(&cfg.port, "port", 0, "the port to listen on (default: random available port)")
		fs.BoolVar(&cfg.lazyAudio, "lazy-audio", false, "stay in the starting state until first use")
		fs.StringVar(&cfg.soundfont, "soundfont", "", "SoundFont (.sf2) to play with (default: $ALDA_SOUNDFONT, or searched in $XDG_DATA_DIRS/soundfonts)")
		if err := fs.Parse(cmdArgs); err != nil {
			return ignoreHelp(err)
		}
		return runPlayer(ctx, cfg, stdout)
	case "":
		root.Usage()
		return errors.New("missing command")
	default:
		root.Usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func ignoreHelp(err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}

type config struct {
	version   string
	cacheDir  string
	level     slog.Level
	port      int
	lazyAudio bool
	soundfont string
}

func runPlayer(ctx context.Context, cfg config, stdout io.Writer) error {
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ctx, shutdown := context.WithCancel(ctx)
	defer shutdown()

	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.port)))
	if err != nil {
		return err
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	// Like the original, expire after a random 5-10 minutes of inactivity so
	// that idle players don't all exit at once.
	timeout := 5*time.Minute + rand.N(5*time.Minute)
	stateDir := filepath.Join(cfg.cacheDir, "state")
	sf, err := state.Create(stateDir, cfg.version, port, timeout)
	if err != nil {
		return err
	}
	defer sf.Remove()

	var out io.Writer = stdout
	logFile, logErr := openLog(filepath.Join(cfg.cacheDir, "logs"))
	if logErr == nil {
		defer logFile.Close()
		out = io.MultiWriter(stdout, logFile)
	}
	log := slog.New(newLogHandler(out, sf.ID(), cfg.level))
	if logErr != nil {
		log.Warn("logging to stdout only", "err", logErr)
	}
	log.Info("starting receiver", "port", port, "version", cfg.version)

	for _, dir := range []string{"players", "repl-servers"} {
		if err := state.CleanStale(filepath.Join(stateDir, dir), 2*time.Minute); err != nil {
			log.Warn("cleaning up stale state files", "err", err)
		}
	}

	soundfont := cmp.Or(cfg.soundfont, os.Getenv("ALDA_SOUNDFONT"))
	if soundfont == "" {
		soundfont = audio.FindSoundFont()
	}
	if soundfont == "" {
		log.Warn("no SoundFont found; playback will be silent. Set ALDA_SOUNDFONT or pass --soundfont")
	}

	eng := engine.New(audio.SampleRate, log)

	// Audio is only set up by a player that is asked to play, so the idle
	// players in the client's pool don't hold the audio device.
	var wg sync.WaitGroup
	defer func() {
		shutdown()
		wg.Wait()
	}()
	startAudio := sync.OnceFunc(func() {
		wg.Go(func() { audio.Run(ctx, eng, soundfont, log) })
	})

	setState := func(s string) {
		if err := sf.Set(s); err != nil {
			log.Warn("updating state file", "err", err)
		}
	}
	extend := func(t time.Time) {
		if err := sf.Extend(t); err != nil {
			log.Warn("updating state file", "err", err)
		}
	}

	handle := func(packet []byte) {
		now := time.Now()
		res, err := eng.Handle(packet)
		if err != nil {
			log.Error("handling packet", "err", err)
		}
		extend(now)
		if res.Ping || res.Scheduled {
			setState(state.Active)
		}
		if res.Scheduled {
			extend(now.Add(res.Until))
		}
		if res.Play {
			startAudio()
		}
		if res.Finished {
			setState(state.Finished)
		}
		if res.Shutdown {
			log.Info("shutting down")
			shutdown()
		}
	}

	serveErr := make(chan error, 1)
	wg.Go(func() { serveErr <- serveOSC(ctx, ln, handle, log) })

	if !cfg.lazyAudio {
		setState(state.Ready)
	}

	touch := time.NewTicker(10 * time.Second)
	defer touch.Stop()
	expiry := time.NewTicker(time.Second)
	defer expiry.Stop()
	for {
		select {
		case <-ctx.Done():
			if context.Cause(ctx) != context.Canceled {
				log.Info("stopping", "cause", context.Cause(ctx))
			}
			return nil
		case err := <-serveErr:
			return err
		case <-eng.Shutdown():
			log.Info("shutting down after playback")
			return nil
		case <-eng.Finished():
			log.Debug("playback finished")
			setState(state.Finished)
		case <-touch.C:
			if err := sf.Touch(); err != nil {
				log.Warn("touching state file", "err", err)
			}
		case now := <-expiry.C:
			if sf.Expired(now) {
				log.Info("shutting down due to inactivity")
				return nil
			}
		}
	}
}
