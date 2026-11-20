// Package osc decodes OSC 1.0 packets without copying: decoded messages
// reference the packet buffer.
package osc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// ErrMalformed is wrapped by every decoding error.
var ErrMalformed = errors.New("malformed OSC packet")

var bundleTag = []byte("#bundle\x00")

// Arg is a decoded argument. Which field is set depends on Type.
type Arg struct {
	Type  byte    // OSC type tag, e.g. 'i', 'f', 's'
	Int   int64   // i, h, c, r, m, t
	Float float64 // f, d
	Bytes []byte  // s, S, b; aliases the packet
}

// Message is a decoded OSC message. Its slices alias the packet and its
// Args are reused by the Decoder, so it is only valid during the callback.
type Message struct {
	Address []byte
	Tags    []byte // type tags without the leading ','
	Args    []Arg
}

// Decoder decodes packets, reusing its argument buffer between messages.
// The zero value is ready to use. A Decoder is not safe for concurrent use.
type Decoder struct {
	msg Message
}

// Decode calls fn for every message in packet, descending into bundles.
// Bundle time tags are ignored. Decoding stops at the first error from fn.
func (d *Decoder) Decode(packet []byte, fn func(*Message) error) error {
	if bytes.HasPrefix(packet, bundleTag) {
		return d.decodeBundle(packet, fn)
	}
	return d.decodeMessage(packet, fn)
}

func (d *Decoder) decodeBundle(b []byte, fn func(*Message) error) error {
	if len(b) < 16 {
		return fmt.Errorf("%w: bundle header truncated", ErrMalformed)
	}
	b = b[16:] // tag and time tag
	for len(b) > 0 {
		if len(b) < 4 {
			return fmt.Errorf("%w: bundle element size truncated", ErrMalformed)
		}
		size := binary.BigEndian.Uint32(b)
		b = b[4:]
		if size > uint32(len(b)) {
			return fmt.Errorf("%w: bundle element size %d exceeds packet", ErrMalformed, size)
		}
		if err := d.Decode(b[:size], fn); err != nil {
			return err
		}
		b = b[size:]
	}
	return nil
}

func (d *Decoder) decodeMessage(b []byte, fn func(*Message) error) error {
	addr, b, err := readString(b)
	if err != nil {
		return fmt.Errorf("address: %w", err)
	}
	if len(addr) == 0 || addr[0] != '/' {
		return fmt.Errorf("%w: invalid address %q", ErrMalformed, addr)
	}
	msg := &d.msg
	*msg = Message{Address: addr, Args: msg.Args[:0]}
	if len(b) > 0 {
		tags, rest, err := readString(b)
		if err != nil {
			return fmt.Errorf("type tags: %w", err)
		}
		if len(tags) == 0 || tags[0] != ',' {
			return fmt.Errorf("%w: type tags must start with ','", ErrMalformed)
		}
		msg.Tags = tags[1:]
		if msg.Args, err = readArgs(msg.Args, msg.Tags, rest); err != nil {
			return fmt.Errorf("%s: %w", addr, err)
		}
	}
	return fn(msg)
}

func readArgs(args []Arg, tags, b []byte) ([]Arg, error) {
	for _, t := range tags {
		a := Arg{Type: t}
		switch t {
		case 'i', 'c', 'r', 'm':
			if len(b) < 4 {
				return args, fmt.Errorf("%w: truncated %c argument", ErrMalformed, t)
			}
			a.Int = int64(int32(binary.BigEndian.Uint32(b)))
			b = b[4:]
		case 'f':
			if len(b) < 4 {
				return args, fmt.Errorf("%w: truncated f argument", ErrMalformed)
			}
			a.Float = float64(math.Float32frombits(binary.BigEndian.Uint32(b)))
			b = b[4:]
		case 'h', 't':
			if len(b) < 8 {
				return args, fmt.Errorf("%w: truncated %c argument", ErrMalformed, t)
			}
			a.Int = int64(binary.BigEndian.Uint64(b))
			b = b[8:]
		case 'd':
			if len(b) < 8 {
				return args, fmt.Errorf("%w: truncated d argument", ErrMalformed)
			}
			a.Float = math.Float64frombits(binary.BigEndian.Uint64(b))
			b = b[8:]
		case 's', 'S':
			var err error
			if a.Bytes, b, err = readString(b); err != nil {
				return args, err
			}
		case 'b':
			if len(b) < 4 {
				return args, fmt.Errorf("%w: truncated blob size", ErrMalformed)
			}
			n := int(binary.BigEndian.Uint32(b))
			b = b[4:]
			if n > len(b) {
				return args, fmt.Errorf("%w: blob size %d exceeds packet", ErrMalformed, n)
			}
			a.Bytes = b[:n:n]
			b = b[min(pad(n), len(b)):]
		case 'T', 'F', 'N', 'I':
		default:
			return args, fmt.Errorf("%w: unsupported type tag %q", ErrMalformed, t)
		}
		args = append(args, a)
	}
	return args, nil
}

// readString reads a NUL-terminated string padded to a multiple of 4 bytes.
func readString(b []byte) (s, rest []byte, err error) {
	n := bytes.IndexByte(b, 0)
	if n < 0 {
		return nil, nil, fmt.Errorf("%w: unterminated string", ErrMalformed)
	}
	end := pad(n + 1)
	if end > len(b) {
		return nil, nil, fmt.Errorf("%w: string padding truncated", ErrMalformed)
	}
	return b[:n:n], b[end:], nil
}

func pad(n int) int { return (n + 3) &^ 3 }
