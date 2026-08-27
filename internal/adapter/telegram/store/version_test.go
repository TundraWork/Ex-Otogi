package store

import (
	"runtime/debug"
	"testing"

	"github.com/gotd/td/tg"
)

func TestPinnedTelegramLayerAndModules(t *testing.T) {
	if tg.Layer != 228 {
		t.Fatalf("tg.Layer = %d, want 228", tg.Layer)
	}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		t.Fatal("build information unavailable")
	}
	want := map[string]string{
		"github.com/gotd/td": "v0.161.0",
		"modernc.org/sqlite": "v1.57.0",
	}
	for _, dependency := range info.Deps {
		if version, exists := want[dependency.Path]; exists {
			if dependency.Version != version {
				t.Fatalf("%s version = %q, want %q", dependency.Path, dependency.Version, version)
			}
			delete(want, dependency.Path)
		}
	}
	for path := range want {
		t.Errorf("missing module %s", path)
	}
}
