package processing

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// The real-ffmpeg test gate (reels audit reels-12, 2026-11-17).
//
// Every test that runs the real ffmpeg used to SKIP when it was not on PATH,
// and `go test` without -v does not print skips, so a CI job without ffmpeg
// reported them green without running one. REQUIRE_FFMPEG=1 (set by the CI
// lane that installs ffmpeg) turns "not installed" into a FAILURE, and also
// runs the -short-skipped pipeline tests.

// requireFFmpegEnv makes a missing ffmpeg/ffprobe fail the real-ffmpeg tests.
const requireFFmpegEnv = "REQUIRE_FFMPEG"

type ffmpegGateAction int

const (
	ffmpegGateRun ffmpegGateAction = iota
	ffmpegGateSkip
	ffmpegGateFail
)

// ffmpegRequired reads REQUIRE_FFMPEG: 1/true/yes.
func ffmpegRequired(getenv func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(getenv(requireFFmpegEnv))) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// ffmpegGate decides what a real-ffmpeg test does. skipShort says the test
// is skipped under -short; missing is the RequireFFmpeg error (nil when both
// binaries are on PATH).
func ffmpegGate(getenv func(string) string, short, skipShort bool, missing error) (ffmpegGateAction, string) {
	required := ffmpegRequired(getenv)
	if missing != nil {
		if required {
			return ffmpegGateFail, "ffmpeg is required (" + requireFFmpegEnv + "=1) but not installed: " + missing.Error()
		}
		return ffmpegGateSkip, "ffmpeg not installed: " + missing.Error()
	}
	if short && skipShort && !required {
		return ffmpegGateSkip, "real ffmpeg pipeline skipped in -short"
	}
	return ffmpegGateRun, ""
}

func applyFFmpegGate(t *testing.T, skipShort bool) {
	t.Helper()
	switch action, msg := ffmpegGate(os.Getenv, testing.Short(), skipShort, RequireFFmpeg()); action {
	case ffmpegGateFail:
		t.Fatal(msg)
	case ffmpegGateSkip:
		t.Skip(msg)
	}
}

// requireFFmpegOrSkip gates the pipeline tests: skipped in -short and
// without ffmpeg, unless REQUIRE_FFMPEG=1, which runs them or fails.
func requireFFmpegOrSkip(t *testing.T) {
	t.Helper()
	applyFFmpegGate(t, true)
}

// requireFFmpegTools gates the small real-ffmpeg tests that also run under
// -short: skipped without ffmpeg unless REQUIRE_FFMPEG=1.
func requireFFmpegTools(t *testing.T) {
	t.Helper()
	applyFFmpegGate(t, false)
}

// skipOrFailFixture is for a test that could not build its ffmpeg fixture:
// a skip normally, a failure when ffmpeg is required.
func skipOrFailFixture(t *testing.T, format string, args ...any) {
	t.Helper()
	if ffmpegRequired(os.Getenv) {
		t.Fatalf(format, args...)
	}
	t.Skipf(format, args...)
}

func TestFFmpegGate(t *testing.T) {
	missing := errors.New("exec: \"ffmpeg\": executable file not found in $PATH")
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == requireFFmpegEnv {
				return v
			}
			return ""
		}
	}
	cases := []struct {
		name             string
		req              string
		short, skipShort bool
		missing          error
		want             ffmpegGateAction
	}{
		{"missing, not required: skip", "", false, true, missing, ffmpegGateSkip},
		{"missing, required: FAIL", "1", false, true, missing, ffmpegGateFail},
		{"missing, required (true): FAIL", "true", false, false, missing, ffmpegGateFail},
		{"missing, required under -short: FAIL", "1", true, true, missing, ffmpegGateFail},
		{"present: run", "", false, true, nil, ffmpegGateRun},
		{"present, -short, pipeline test: skip", "", true, true, nil, ffmpegGateSkip},
		{"present, -short, small test: run", "", true, false, nil, ffmpegGateRun},
		{"present, -short, required: run", "1", true, true, nil, ffmpegGateRun},
		{"missing, junk value: skip", "maybe", false, true, missing, ffmpegGateSkip},
	}
	for _, c := range cases {
		if got, msg := ffmpegGate(env(c.req), c.short, c.skipShort, c.missing); got != c.want {
			t.Errorf("%s: action %d (%s), want %d", c.name, got, msg, c.want)
		}
	}
}
