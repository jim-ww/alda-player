// Package state manages the state files through which the Alda client
// discovers player processes.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// Player states, as understood by the client.
const (
	Starting = "starting"
	Ready    = "ready"    // available to be claimed by a client
	Active   = "active"   // claimed
	Finished = "finished" // done playing a one-off score
)

// File is a player's state file at <dir>/players/<version>/<id>.json.
// It is safe for concurrent use.
type File struct {
	id      string
	path    string
	tmp     string
	timeout time.Duration

	mu sync.Mutex
	st fileState
}

type fileState struct {
	Expiry int64  `json:"expiry"` // Unix milliseconds
	PID    int    `json:"pid"`
	Port   int    `json:"port"`
	State  string `json:"state"`
}

// Create creates the state file of a starting player listening on port,
// under a new random ID. The player expires after timeout without activity.
func Create(dir, version string, port int, timeout time.Duration) (*File, error) {
	players := filepath.Join(dir, "players")
	versionDir := filepath.Join(players, version)
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating state directory: %w", err)
	}
	for range 100 {
		id := newID()
		path := filepath.Join(versionDir, id+".json")
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if errors.Is(err, fs.ErrNotExist) {
			// Another player's CleanStale removed the empty directory.
			if err := os.MkdirAll(versionDir, 0o755); err != nil {
				return nil, fmt.Errorf("creating state directory: %w", err)
			}
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("creating state file: %w", err)
		}
		f.Close()
		sf := &File{
			id:      id,
			path:    path,
			tmp:     filepath.Join(players, "."+version+"-"+id+".tmp"),
			timeout: timeout,
			st: fileState{
				Expiry: time.Now().Add(timeout).UnixMilli(),
				PID:    os.Getpid(),
				Port:   port,
				State:  Starting,
			},
		}
		if err := sf.write(); err != nil {
			os.Remove(path)
			return nil, err
		}
		return sf, nil
	}
	return nil, errors.New("creating state file: no free player ID")
}

// newID returns three random lowercase letters, like the original player.
func newID() string {
	b := make([]byte, 3)
	for i := range b {
		b[i] = 'a' + byte(rand.IntN(26))
	}
	return string(b)
}

// ID returns the player ID.
func (f *File) ID() string { return f.id }

// Set changes the player state.
func (f *File) Set(state string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.st.State == state {
		return nil
	}
	f.st.State = state
	return f.write()
}

// State returns the player state.
func (f *File) State() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st.State
}

// Extend makes the player expire no earlier than the timeout after t.
func (f *File) Extend(t time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	expiry := t.Add(f.timeout).UnixMilli()
	if expiry <= f.st.Expiry {
		return nil
	}
	f.st.Expiry = expiry
	return f.write()
}

// Expired reports whether the player has been inactive for too long.
func (f *File) Expired(now time.Time) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return now.UnixMilli() > f.st.Expiry
}

// Touch updates the file's modification time, which marks it as not stale.
func (f *File) Touch() error {
	now := time.Now()
	return os.Chtimes(f.path, now, now)
}

// Remove deletes the state file.
func (f *File) Remove() error {
	return os.Remove(f.path)
}

// write replaces the file atomically. The temporary file lives outside the
// version directory, so the client never lists it.
func (f *File) write() error {
	b, err := json.Marshal(f.st)
	if err != nil {
		return err
	}
	if err := os.WriteFile(f.tmp, b, 0o644); err != nil {
		return fmt.Errorf("writing state file: %w", err)
	}
	if err := os.Rename(f.tmp, f.path); err != nil {
		return fmt.Errorf("writing state file: %w", err)
	}
	return nil
}

// CleanStale removes files under dir not modified within maxAge, and then
// empty directories. They are left behind by players that died without
// cleaning up. A missing dir is not an error.
func CleanStale(dir string, maxAge time.Duration) error {
	cutoff := time.Now().Add(-maxAge)
	var dirs []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != dir {
				dirs = append(dirs, path)
			}
			return nil
		}
		info, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
		return nil
	})
	// Deepest first; removing a non-empty directory fails harmlessly.
	for _, d := range slices.Backward(dirs) {
		os.Remove(d)
	}
	return err
}
