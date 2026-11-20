package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/daveyarwood/go-osc/osc"
	"gitlab.com/gomidi/midi/v2/smf"
)

func TestServeOSCWithClientTransport(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	got := make(chan []byte, 100)
	done := make(chan error)
	go func() {
		done <- serveOSC(ctx, ln, func(p []byte) { got <- bytes.Clone(p) }, slog.New(slog.DiscardHandler))
	}()

	client := osc.NewClient("127.0.0.1", ln.Addr().(*net.TCPAddr).Port, osc.ClientProtocol(osc.TCP))
	var want [][]byte
	for i := range 50 {
		m := osc.NewMessage(fmt.Sprintf("/track/%d/midi/note", i))
		m.Append(int32(i))
		m.Append(strings.Repeat("x", i*100))
		b, _ := m.MarshalBinary()
		want = append(want, b)
		if err := client.Send(m); err != nil {
			t.Fatal(err)
		}
	}
	for i, w := range want {
		if p := <-got; !bytes.Equal(p, w) {
			t.Fatalf("packet %d differs or arrived out of order", i)
		}
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("serveOSC: %v", err)
	}
}

func TestInfo(t *testing.T) {
	t.Setenv("ALDA_PLAYER_VERSION", "9.9.9")
	t.Setenv("XDG_CACHE_HOME", "/cache")
	var out bytes.Buffer
	if err := run(t.Context(), []string{"info"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	// The client parses the version with `alda-player ([^\r\n]+)`.
	if want := "alda-player 9.9.9\nlog path: /cache/alda/logs\n"; out.String() != want {
		t.Errorf("info printed %q, want %q", out.String(), want)
	}
}

// TestEndToEnd drives this player with the real alda client.
func TestEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	if _, err := exec.LookPath("alda"); err != nil {
		t.Skip("alda not in PATH")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if out, err := exec.Command("go", "build", "-o", filepath.Join(bin, "alda-player"), ".").CombinedOutput(); err != nil {
		t.Fatalf("building: %v\n%s", err, out)
	}
	env := append(os.Environ(),
		"PATH="+bin+string(filepath.ListSeparator)+os.Getenv("PATH"),
		"XDG_CACHE_HOME="+filepath.Join(dir, "cache"),
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
		"ALDA_DISABLE_TELEMETRY=yes",
		"ALDA_SOUNDFONT=", // no audio needed
	)
	alda := func(t *testing.T, env []string, args ...string) string {
		t.Helper()
		cmd := exec.Command("alda", args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("alda %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}

	out := alda(t, append(env, "ALDA_DISABLE_SPAWNING=yes"), "version")
	m := regexp.MustCompile(`alda (\d+)\.(\d+)\.\S+`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("unexpected alda version output: %q", out)
	}
	if m[1] != "2" || m[2] < "3" {
		t.Skipf("alda %s is older than 2.3.0", m[0])
	}
	env = append(env, "ALDA_PLAYER_VERSION="+strings.TrimPrefix(m[0], "alda "))
	t.Cleanup(func() { alda(t, env, "shutdown") })

	t.Run("doctor", func(t *testing.T) {
		out := alda(t, env, "doctor")
		for line := range strings.Lines(out) {
			if strings.HasPrefix(line, "ERR") {
				t.Errorf("doctor: %s", line)
			}
		}
	})

	t.Run("export", func(t *testing.T) {
		path := filepath.Join(dir, "out.mid")
		alda(t, env, "export", "-c", "piano: c8 d e | percussion: o2 c4", "-o", path)
		f, err := smf.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var keys []uint8
		for _, ev := range f.Tracks[0] {
			var ch, key, vel uint8
			if ev.Message.GetNoteStart(&ch, &key, &vel) {
				keys = append(keys, key)
			}
		}
		slices.Sort(keys)
		if want := []uint8{36, 60, 62, 64}; !slices.Equal(keys, want) {
			t.Errorf("note keys = %v, want %v", keys, want)
		}
	})
}
