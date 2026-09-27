// Package dubbing produces a translated, spoken audio track for a video —
// the "generate" half of alternate audio tracks (2026-09-27).
//
// A Dubber takes the video's own audio and returns a file the same length as
// the video with the speech re-voiced in the target language. media-service
// then muxes that file into the video's variants exactly as it does an
// uploaded track, so the dubber never touches storage or the database.
//
// Selection is by MEDIA_DUBBING_BACKEND:
//
//	unset / "off" / "none" → no dubber; the generate route answers 503
//	"openai"               → OpenAIDubber (needs OPENAI_API_KEY)
//	"stub"                 → StubDubber, non-production only: copies the
//	                          source audio so the pipeline can be exercised
//	                          on dev without a key
package dubbing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DubInput is everything a Dubber needs. Paths are local files in a scratch
// directory the caller owns and removes.
type DubInput struct {
	// VideoPath is the video the track is for (the original or the 720p rung).
	VideoPath string
	// SourceAudioPath is the video's audio, already extracted with ffmpeg.
	SourceAudioPath string
	// SourceLanguage is the spoken language of the source, "" to auto-detect.
	SourceLanguage string
	// TargetLanguage is the language to produce.
	TargetLanguage string
	// DurationMs is the video's duration; the output is padded or trimmed to it.
	DurationMs int
	// WorkDir is where the dubber may write intermediate and output files.
	// Empty means a fresh temp dir that the CALLER must remove (it is the
	// parent of the returned path).
	WorkDir string
}

// Dubber produces the dubbed audio file and returns its path.
type Dubber interface {
	Name() string
	Dub(ctx context.Context, in DubInput) (audioPath string, err error)
}

// ErrNotConfigured: the chosen backend is missing its credentials.
var ErrNotConfigured = errors.New("dubbing: backend not configured")

// ErrStubInProduction: MEDIA_DUBBING_BACKEND=stub is refused in production,
// the same way the mock scanner and the mock face comparer are.
var ErrStubInProduction = errors.New("dubbing: MEDIA_DUBBING_BACKEND=stub is refused in production")

// Select picks the dubber from the environment. (nil, nil) means none is
// configured, which the service reports as DUBBING_UNAVAILABLE. An error
// means main must refuse to start.
func Select(getenv func(string) string) (Dubber, error) {
	switch strings.ToLower(strings.TrimSpace(getenv("MEDIA_DUBBING_BACKEND"))) {
	case "", "off", "none", "false", "0":
		return nil, nil
	case "openai":
		return NewOpenAIDubber(getenv)
	case "stub":
		if isProductionEnv(getenv) {
			return nil, ErrStubInProduction
		}
		return StubDubber{}, nil
	default:
		return nil, fmt.Errorf("dubbing: unknown MEDIA_DUBBING_BACKEND %q (openai, stub, or unset)", getenv("MEDIA_DUBBING_BACKEND"))
	}
}

// isProductionEnv mirrors the detection cmd/server and processing use.
func isProductionEnv(getenv func(string) string) bool {
	for _, k := range []string{"DEPLOY_ENV", "APP_ENV", "ENVIRONMENT", "ENV"} {
		switch strings.ToLower(strings.TrimSpace(getenv(k))) {
		case "production", "prod":
			return true
		}
	}
	return false
}

// StubDubber "dubs" by copying the source audio. It exists so the whole
// upload → mux → serve path can be exercised on dev without an AI key; the
// resulting track is the original audio under a different language label,
// which is why Select refuses it in production.
type StubDubber struct{}

func (StubDubber) Name() string { return "stub" }

func (StubDubber) Dub(_ context.Context, in DubInput) (string, error) {
	if in.SourceAudioPath == "" {
		return "", errors.New("dubbing stub: no source audio")
	}
	dir, err := workDir(in.WorkDir, "dub-stub-")
	if err != nil {
		return "", err
	}
	out := filepath.Join(dir, "dubbed"+filepath.Ext(in.SourceAudioPath))
	if err := copyFile(in.SourceAudioPath, out); err != nil {
		return "", err
	}
	return out, nil
}

func workDir(dir, prefix string) (string, error) {
	if dir != "" {
		return dir, os.MkdirAll(dir, 0o700)
	}
	return os.MkdirTemp("", prefix)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
