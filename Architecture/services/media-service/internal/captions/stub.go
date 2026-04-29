package captions

import "context"

// StubBackend is the default backend when no real transcription
// provider is wired. Returns a placeholder Result so the media-
// service pipeline (subtitle row insert, post update, etc.) can
// run end-to-end in dev without an API key.
//
// The placeholder text makes it obvious in the studio that
// captions need a real backend before they're useful — much better
// than silently inserting empty subtitle rows that look "done".
type StubBackend struct{}

func (StubBackend) Name() string { return "stub" }

func (StubBackend) Transcribe(_ context.Context, audioURL, language string) (*Result, error) {
	if language == "" {
		language = "en"
	}
	return &Result{
		Language:      language,
		Format:        "vtt",
		Text:          "[auto-captions pending — set OPENAI_API_KEY (or wire another backend) to enable]",
		BackendName:   "stub",
		IsPlaceholder: true,
	}, nil
}
