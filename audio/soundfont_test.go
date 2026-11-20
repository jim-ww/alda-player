package audio

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindSoundFont(t *testing.T) {
	home, data1, data2 := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("XDG_DATA_HOME", home)
	t.Setenv("XDG_DATA_DIRS", data1+":relative:"+data2)
	touch := func(path string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	b := touch(filepath.Join(data2, "sounds/sf2/b.sf2"))
	if got := FindSoundFont(); got != b {
		t.Fatalf("got %q, want %q", got, b)
	}
	a := touch(filepath.Join(data1, "soundfonts/a.sf2"))
	touch(filepath.Join(data1, "soundfonts/z.sf2"))
	if got := FindSoundFont(); got != a {
		t.Fatalf("got %q, want first in name order %q", got, a)
	}
	def := touch(filepath.Join(data1, "soundfonts/default.sf2"))
	if got := FindSoundFont(); got != def {
		t.Fatalf("got %q, want %q", got, def)
	}
	user := touch(filepath.Join(home, "soundfonts/mine.sf2"))
	if got := FindSoundFont(); got != user {
		t.Fatalf("got %q, want XDG_DATA_HOME first %q", got, user)
	}
}
