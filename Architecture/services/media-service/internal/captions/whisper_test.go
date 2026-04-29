package captions

import (
	"context"
	"strings"
	"testing"
)

// TestStubBackend — sanity that the placeholder is obviously a
// placeholder and tagged so the studio UI can branch on it.
func TestStubBackend(t *testing.T) {
	b := StubBackend{}
	if b.Name() != "stub" {
		t.Errorf("Name: got %q, want stub", b.Name())
	}
	res, err := b.Transcribe(context.Background(), "https://example.com/x.mp3", "")
	if err != nil {
		t.Fatalf("stub Transcribe: unexpected error %v", err)
	}
	if !res.IsPlaceholder {
		t.Errorf("stub result must set IsPlaceholder=true so studio can show 'pending'")
	}
	if res.BackendName != "stub" {
		t.Errorf("backend stamp: got %q, want stub", res.BackendName)
	}
	if !strings.Contains(res.Text, "pending") {
		t.Errorf("stub text should make the placeholder obvious, got %q", res.Text)
	}
}

// TestSelectBackendDefault — without OPENAI_API_KEY we fall back
// to Stub. The deploy environment is what flips this; tests can't
// reliably depend on the env so we just assert the dev default.
func TestSelectBackendDefault(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	b := SelectBackend()
	if b.Name() != "stub" {
		t.Errorf("expected stub when OPENAI_API_KEY unset, got %s", b.Name())
	}
}

// TestSelectBackendWhisper — with a key, we get the whisper
// backend. We don't make any HTTP calls here.
func TestSelectBackendWhisper(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test-not-real")
	b := SelectBackend()
	if b.Name() != "whisper" {
		t.Errorf("expected whisper when OPENAI_API_KEY set, got %s", b.Name())
	}
}

// TestFilenameForURL — Whisper requires the multipart filename to
// have an extension so the API can pick the codec. The helper must
// strip query strings and add a default extension when missing.
func TestFilenameForURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://cdn/foo.mp3", "foo.mp3"},
		{"https://cdn/foo.mp3?token=abc&exp=42", "foo.mp3"},
		{"https://cdn/path/with/slashes/clip.m4a", "clip.m4a"},
		{"https://cdn/no-extension", "no-extension.mp3"},
		{"", "audio.mp3"},
	}
	for _, tc := range cases {
		if got := filenameForURL(tc.in); got != tc.want {
			t.Errorf("filenameForURL(%q): got %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestBuildWhisperMultipart — confirms the form fields the OpenAI
// API expects are present and the file part carries the bytes.
// Verifies wire shape without making a network call.
func TestBuildWhisperMultipart(t *testing.T) {
	body, contentType, err := buildWhisperMultipart([]byte("FAKE_AUDIO_BYTES"), "x.mp3", "whisper-1", "en")
	if err != nil {
		t.Fatalf("buildWhisperMultipart: %v", err)
	}
	if !strings.HasPrefix(contentType, "multipart/form-data") {
		t.Errorf("content-type: got %q, want multipart/form-data...", contentType)
	}
	buf := make([]byte, 4096)
	n, _ := body.Read(buf)
	got := string(buf[:n])
	required := []string{
		`name="model"`, "whisper-1",
		`name="response_format"`, "verbose_json",
		`name="timestamp_granularities[]"`, "word",
		`name="language"`, "en",
		`name="file"`, "filename=\"x.mp3\"",
		"FAKE_AUDIO_BYTES",
	}
	for _, want := range required {
		if !strings.Contains(got, want) {
			t.Errorf("multipart body missing %q", want)
		}
	}
}
