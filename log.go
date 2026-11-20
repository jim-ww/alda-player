package main

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"alda-player/engine"
)

// logHandler formats records like the original player's log4j layout:
//
//	abc INFO  2006-01-02 15:04:05 - message key=value
//
// so that logs of both players read alike. Every record is written with a
// single Write call, which keeps lines intact when several player
// processes append to the same file.
type logHandler struct {
	mu     *sync.Mutex
	w      io.Writer
	level  slog.Leveler
	id     string
	group  string
	preset []byte // attributes added with WithAttrs, already formatted
}

func newLogHandler(w io.Writer, id string, level slog.Leveler) *logHandler {
	return &logHandler{mu: new(sync.Mutex), w: w, level: level, id: id}
}

func (h *logHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level.Level()
}

func (h *logHandler) Handle(_ context.Context, r slog.Record) error {
	b := make([]byte, 0, 256)
	b = append(b, h.id...)
	b = append(b, ' ')
	b = appendLevel(b, r.Level)
	b = append(b, ' ')
	b = r.Time.AppendFormat(b, time.DateTime)
	b = append(b, " - "...)
	b = append(b, r.Message...)
	b = append(b, h.preset...)
	r.Attrs(func(a slog.Attr) bool {
		b = appendAttr(b, h.group, a)
		return true
	})
	b = append(b, '\n')
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write(b)
	return err
}

func (h *logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := *h
	c.preset = append([]byte(nil), h.preset...)
	for _, a := range attrs {
		c.preset = appendAttr(c.preset, h.group, a)
	}
	return &c
}

func (h *logHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	c := *h
	c.group = h.group + name + "."
	return &c
}

func appendLevel(b []byte, l slog.Level) []byte {
	var s string
	switch {
	case l < slog.LevelDebug:
		s = "TRACE"
	case l < slog.LevelInfo:
		s = "DEBUG"
	case l < slog.LevelWarn:
		s = "INFO "
	case l < slog.LevelError:
		s = "WARN "
	default:
		s = "ERROR"
	}
	return append(b, s...)
}

func appendAttr(b []byte, group string, a slog.Attr) []byte {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return b
	}
	if a.Value.Kind() == slog.KindGroup {
		for _, ga := range a.Value.Group() {
			b = appendAttr(b, group+a.Key+".", ga)
		}
		return b
	}
	b = append(b, ' ')
	b = append(b, group...)
	b = append(b, a.Key...)
	b = append(b, '=')
	s := a.Value.String()
	if s == "" || strings.ContainsAny(s, " =\"\n\t") {
		return strconv.AppendQuote(b, s)
	}
	return append(b, s...)
}

// parseLevel maps the verbosity flags to a level.
func parseLevel(verbose, veryVerbose bool) slog.Level {
	switch {
	case veryVerbose:
		return engine.LevelTrace
	case verbose:
		return slog.LevelDebug
	default:
		return slog.LevelInfo
	}
}

// openLog opens dir/alda-player.log for appending. A log last written on an
// earlier day is first renamed to alda-player-<date>.log.
func openLog(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "alda-player.log")
	if fi, err := os.Stat(path); err == nil {
		mod := fi.ModTime()
		if y, m, d := mod.Date(); time.Date(y, m, d, 0, 0, 0, 0, time.Local).Before(today()) {
			dated := filepath.Join(dir, "alda-player-"+mod.Format(time.DateOnly)+".log")
			if _, err := os.Stat(dated); errors.Is(err, fs.ErrNotExist) {
				os.Rename(path, dated)
			}
		}
	}
	return os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
}

func today() time.Time {
	y, m, d := time.Now().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}
