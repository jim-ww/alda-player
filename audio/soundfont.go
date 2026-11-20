package audio

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
)

// FindSoundFont returns the first SoundFont found in the standard data
// directories, or "" if there is none. In each directory's soundfonts and
// sounds/sf2 subdirectories, default.sf2 is preferred, then the first *.sf2
// in name order.
func FindSoundFont() string {
	for _, dir := range searchDirs() {
		if path := filepath.Join(dir, "default.sf2"); isFile(path) {
			return path
		}
		matches, _ := filepath.Glob(filepath.Join(dir, "*.sf2"))
		slices.Sort(matches)
		for _, m := range matches {
			if isFile(m) {
				return m
			}
		}
	}
	return ""
}

// searchDirs lists $XDG_DATA_HOME and $XDG_DATA_DIRS, plus the NixOS system
// profile in case XDG_DATA_DIRS isn't set.
func searchDirs() []string {
	var data []string
	if home, err := os.UserHomeDir(); err == nil {
		data = append(data, cmp.Or(os.Getenv("XDG_DATA_HOME"), filepath.Join(home, ".local/share")))
	}
	data = append(data, filepath.SplitList(cmp.Or(os.Getenv("XDG_DATA_DIRS"), "/usr/local/share:/usr/share"))...)
	data = append(data, "/run/current-system/sw/share")

	var dirs []string
	for _, d := range data {
		if !filepath.IsAbs(d) {
			continue
		}
		for _, sub := range []string{"soundfonts", "sounds/sf2"} {
			if p := filepath.Join(d, sub); !slices.Contains(dirs, p) {
				dirs = append(dirs, p)
			}
		}
	}
	return dirs
}

func isFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}
