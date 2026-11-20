package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// clientState mirrors the client's PlayerState (client/system/process_management.go).
type clientState struct {
	State  string `json:"state"`
	Port   int    `json:"port"`
	Expiry int64  `json:"expiry"`
	PID    int    `json:"pid"`
}

func readState(t *testing.T, dir string, f *File) clientState {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "players", "1.2.3", f.ID()+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var s clientState
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLifecycle(t *testing.T) {
	dir := t.TempDir()
	start := time.Now()
	f, err := Create(dir, "1.2.3", 4242, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.ID()) != 3 || strings.Trim(f.ID(), "abcdefghijklmnopqrstuvwxyz") != "" {
		t.Errorf("ID = %q, want 3 lowercase letters", f.ID())
	}

	s := readState(t, dir, f)
	if s.State != Starting || s.Port != 4242 || s.PID != os.Getpid() {
		t.Errorf("state = %+v", s)
	}
	if s.Expiry < start.Add(time.Minute).UnixMilli() {
		t.Errorf("expiry %d is too early", s.Expiry)
	}

	if err := f.Set(Ready); err != nil {
		t.Fatal(err)
	}
	if got := readState(t, dir, f).State; got != Ready {
		t.Errorf("state = %q, want %q", got, Ready)
	}

	later := start.Add(time.Hour)
	if err := f.Extend(later); err != nil {
		t.Fatal(err)
	}
	if got, want := readState(t, dir, f).Expiry, later.Add(time.Minute).UnixMilli(); got != want {
		t.Errorf("expiry = %d, want %d", got, want)
	}
	if err := f.Extend(start); err != nil { // never shortens
		t.Fatal(err)
	}
	if f.Expired(later) || !f.Expired(later.Add(2*time.Minute)) {
		t.Error("Expired is wrong")
	}

	entries, _ := os.ReadDir(filepath.Join(dir, "players", "1.2.3"))
	if len(entries) != 1 {
		t.Errorf("version dir has %d entries, want only the state file", len(entries))
	}

	if err := f.Remove(); err != nil {
		t.Fatal(err)
	}
}

func TestCleanStale(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-time.Hour)
	write := func(path string, mtime time.Time) {
		t.Helper()
		path = filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	write("players/0.1/abc.json", old)
	write("players/0.2/abd.json", old)
	write("players/0.2/abe.json", time.Now())

	if err := CleanStale(filepath.Join(dir, "players"), 2*time.Minute); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{
		"players/0.1":          false,
		"players/0.2/abd.json": false,
		"players/0.2/abe.json": true,
	} {
		_, err := os.Stat(filepath.Join(dir, path))
		if got := err == nil; got != want {
			t.Errorf("%s exists = %v, want %v", path, got, want)
		}
	}
	if err := CleanStale(filepath.Join(dir, "missing"), time.Minute); err != nil {
		t.Errorf("missing dir: %v", err)
	}
}
