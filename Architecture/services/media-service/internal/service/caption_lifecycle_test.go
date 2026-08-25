package service

import (
	"context"
	"testing"

	"github.com/atpost/media-service/internal/captions"
)

// Module 1 fixes-v1 / Codex P0-2 + P0-3.

// The schema CHECK on media_subtitles.source is
// (auto_generated | manual | translated). Writing 'auto' made every
// configured transcription fail at the database. This pins the mapping so
// a future edit cannot reintroduce an out-of-enum value.
func TestNormalizeSubtitleSource(t *testing.T) {
	cases := map[string]string{
		"auto":           "auto_generated", // the bug: legacy spelling
		"auto_generated": "auto_generated",
		"manual":         "manual",
		"translated":     "translated",
		"":               "auto_generated",
		"whisper":        "manual", // unknown → a value the CHECK accepts
	}
	for in, want := range cases {
		if got := normalizeSubtitleSource(in); got != want {
			t.Errorf("normalizeSubtitleSource(%q) = %q, want %q", in, got, want)
		}
	}
	// Every output must be inside the schema's allowed set.
	allowed := map[string]bool{"auto_generated": true, "manual": true, "translated": true}
	for in := range cases {
		if !allowed[normalizeSubtitleSource(in)] {
			t.Errorf("normalizeSubtitleSource(%q) produced a value the CHECK rejects", in)
		}
	}
}

// GetCaptionStatus must never report a provider-less deployment as
// anything other than "unavailable" — a placeholder is not a caption.
func TestCaptionsBackendConfigured_StubIsNotConfigured(t *testing.T) {
	s := &Service{}
	if s.CaptionsBackendConfigured() {
		t.Fatal("a nil backend must not count as configured")
	}
	s.captions = stubBackendForTest{}
	if s.CaptionsBackendConfigured() {
		t.Fatal("the stub backend must not count as configured")
	}
	s.captions = realBackendForTest{}
	if !s.CaptionsBackendConfigured() {
		t.Fatal("a real backend must count as configured")
	}
}

type stubBackendForTest struct{}

func (stubBackendForTest) Name() string { return "stub" }
func (stubBackendForTest) Transcribe(context.Context, string, string) (*captions.Result, error) {
	return &captions.Result{IsPlaceholder: true}, nil
}

type realBackendForTest struct{}

func (realBackendForTest) Name() string { return "whisper" }
func (realBackendForTest) Transcribe(context.Context, string, string) (*captions.Result, error) {
	return &captions.Result{Text: "hello", Language: "en", Format: "vtt"}, nil
}
