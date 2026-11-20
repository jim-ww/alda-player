//go:build unix

package audio

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// mapFile maps path read-only for the life of the process. The pages live in
// the page cache, so all players share one copy and nothing is read up
// front; the kernel is asked to read the file ahead so that rendering
// doesn't stall on page faults.
func mapFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := fi.Size()
	if size <= 0 || int64(int(size)) != size {
		return nil, fmt.Errorf("%s: unsupported size %d", path, size)
	}
	b, err := unix.Mmap(int(f.Fd()), 0, int(size), unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("mapping %s: %w", path, err)
	}
	_ = unix.Madvise(b, unix.MADV_WILLNEED)
	return b, nil
}
