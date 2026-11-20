//go:build !unix

package audio

import "os"

func mapFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
