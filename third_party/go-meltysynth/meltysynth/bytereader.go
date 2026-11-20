package meltysynth

import (
	"encoding/binary"
	"io"
	"unsafe"
)

var littleEndian = binary.NativeEndian.Uint16([]byte{1, 0}) == 1

// byteReader reads from memory and can hand out sample data without copying.
type byteReader struct {
	data []byte
	pos  int
}

func (r *byteReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

// int16s consumes size bytes of little-endian samples. The result aliases
// the data when the host byte order and alignment allow it.
func (r *byteReader) int16s(size int) ([]int16, error) {
	if size < 0 || size > len(r.data)-r.pos {
		return nil, io.ErrUnexpectedEOF
	}
	b := r.data[r.pos : r.pos+size]
	r.pos += size
	n := size / 2
	if n == 0 {
		return []int16{}, nil
	}
	if p := unsafe.Pointer(unsafe.SliceData(b)); littleEndian && uintptr(p)%2 == 0 {
		return unsafe.Slice((*int16)(p), n), nil
	}
	s := make([]int16, n)
	for i := range s {
		s[i] = int16(binary.LittleEndian.Uint16(b[2*i:]))
	}
	return s, nil
}
