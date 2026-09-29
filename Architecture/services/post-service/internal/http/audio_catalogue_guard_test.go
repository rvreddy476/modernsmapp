package http

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// post-service serves no sound catalogue (2026-09-29, service/audio.go).
//
// It used to register /v1/audio/tracks: create, trending, search, read. The
// gateway sends the whole /v1/audio prefix to media-service, so no client
// could reach them; and the create took no caller and no proof of ownership,
// so they must not come back as they were. media-service owns sounds. Read
// from the source, so a re-registration fails here before it ships.
func TestNoSoundCatalogueIsRegisteredHere(t *testing.T) {
	for _, dir := range []string{".", filepath.Join("..", "..", "cmd", "server")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			src, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			for n, line := range strings.Split(string(src), "\n") {
				code := line
				if i := strings.Index(code, "//"); i >= 0 {
					code = code[:i]
				}
				if strings.Contains(code, `"/v1/audio`) || strings.Contains(code, "RegisterAudioRoutes") {
					t.Errorf("%s:%d registers a sound route; media-service owns /v1/audio: %s", filepath.Join(dir, name), n+1, strings.TrimSpace(line))
				}
			}
		}
	}
}
