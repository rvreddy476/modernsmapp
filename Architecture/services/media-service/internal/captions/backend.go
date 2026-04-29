// Package captions wraps the speech-to-text backend used to
// auto-generate subtitles for uploaded videos. The interface lets
// us swap providers (OpenAI Whisper today, Deepgram / AssemblyAI /
// self-hosted whisper.cpp tomorrow) without touching the media-
// service business logic — same pattern the payments-service uses
// for Razorpay/Stripe.
//
// Production deployments wire WhisperBackend (OpenAI) when
// OPENAI_API_KEY is set; otherwise StubBackend kicks in and
// returns "captions pending" placeholders so the rest of the
// pipeline can still finish.
package captions

import (
	"context"
	"errors"
)

// Word is one timed token in the transcript. Used for word-level
// karaoke captions on Flicks/Reels.
type Word struct {
	Text       string  `json:"text"`
	StartMs    int64   `json:"start_ms"`
	EndMs      int64   `json:"end_ms"`
	Confidence float32 `json:"confidence,omitempty"`
}

// Result is what a backend hands back to the service layer. The
// service then turns this into a media_subtitles row.
type Result struct {
	Language     string  `json:"language"`
	Format       string  `json:"format"`        // "srt" | "vtt" | "json"
	Text         string  `json:"text"`          // full transcript
	Words        []Word  `json:"words,omitempty"`
	Confidence   float32 `json:"confidence,omitempty"`
	BackendName  string  `json:"backend"`       // "whisper" | "stub" | etc.
	IsPlaceholder bool   `json:"is_placeholder"` // true when no real transcription happened
}

// Backend is the contract every speech-to-text provider implements.
// Audio is given as a fetchable URL so the backend can stream it
// directly (most providers prefer multipart upload anyway, the
// implementation handles the fetch internally).
//
// Language can be "" to ask the backend to auto-detect.
type Backend interface {
	// Name reports which backend is wired (logged + stamped on the
	// resulting Result so observability tells provider apart).
	Name() string

	// Transcribe fetches the audio at audioURL and returns a
	// transcript. Language hint is optional ("" means auto-detect).
	Transcribe(ctx context.Context, audioURL, language string) (*Result, error)
}

// ErrNotConfigured is returned by NewWhisperBackend when no
// OPENAI_API_KEY is set. Callers fall back to StubBackend so the
// pipeline keeps working in dev.
var ErrNotConfigured = errors.New("captions: OPENAI_API_KEY not set")
