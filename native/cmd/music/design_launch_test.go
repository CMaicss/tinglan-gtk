package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDesignLauncherHasAllRuntimeAssets(t *testing.T) {
	dir := t.TempDir()
	if err := unpackDesignUI(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"app.py", "live.py", "live_art.py", "live_collections.py", "live_settings.py", "gtk_collection.css", "icon.png"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || len(data) == 0 {
			t.Fatalf("missing runtime asset %s: %v", name, err)
		}
	}
	assets, err := os.ReadDir(filepath.Join(dir, "icons"))
	if err != nil || len(assets) == 0 {
		t.Fatalf("missing icon assets: %v", err)
	}
}

func TestDesignEntrypointKeepsBackendAndLegacyTestsSeparate(t *testing.T) {
	for _, tt := range []struct {
		args           []string
		headless, want bool
	}{
		{nil, false, true}, {[]string{"--smoke"}, false, true}, {[]string{"--width", "400", "--dark"}, false, true},
		{[]string{"--design-stdio"}, false, false}, {[]string{"--demo", "2"}, false, false}, {nil, true, false},
	} {
		if got := useDesignUI(tt.args, tt.headless); got != tt.want {
			t.Errorf("%v: got %v, want %v", tt.args, got, tt.want)
		}
	}
}
