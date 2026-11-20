package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"slices"
	"time"
)

const (
	maxPacketSize = 64 << 20
	readTimeout   = 5 * time.Second
)

// serveOSC receives OSC packets the way the Alda client sends them: one
// packet per TCP connection, ending when the client closes it. Packets are
// read and handled one at a time, so they are applied in arrival order.
// It returns when ctx is done.
func serveOSC(ctx context.Context, ln net.Listener, handle func([]byte), log *slog.Logger) error {
	stop := context.AfterFunc(ctx, func() { ln.Close() })
	defer stop()

	buf := make([]byte, 0, 64<<10)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			log.Warn("accepting connection", "err", err)
			time.Sleep(10 * time.Millisecond)
			continue
		}
		buf, err = readPacket(conn, buf[:0])
		conn.Close()
		if err != nil {
			log.Warn("reading packet", "err", err)
			continue
		}
		if len(buf) > 0 {
			handle(buf)
		}
	}
}

// readPacket appends everything conn sends until EOF to buf.
func readPacket(conn net.Conn, buf []byte) ([]byte, error) {
	if err := conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
		return buf, err
	}
	for {
		if len(buf) == cap(buf) {
			if len(buf) >= maxPacketSize {
				return buf, fmt.Errorf("packet exceeds %d bytes", maxPacketSize)
			}
			buf = slices.Grow(buf, len(buf))
		}
		n, err := conn.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if errors.Is(err, io.EOF) {
			return buf, nil
		}
		if err != nil {
			return buf, err
		}
	}
}
