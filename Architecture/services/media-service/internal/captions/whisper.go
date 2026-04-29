package captions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"
)

// WhisperBackend talks to OpenAI's Whisper transcription API at
// /v1/audio/transcriptions. Stream:
//
//  1. Fetch the audio file from audioURL (signed CDN URL or
//     pre-signed S3) — bounded read, capped to whisperMaxAudioBytes.
//  2. POST as multipart/form-data with model=whisper-1 plus
//     response_format=verbose_json so we get word timings.
//  3. Decode into our Result shape.
//
// Whisper has a 25 MB upload cap; longer audio needs to be chunked
// before this backend gets called (out of scope here — the worker
// queue would do the split).
type WhisperBackend struct {
	apiKey string
	model  string
	c      *http.Client
}

const (
	whisperEndpoint        = "https://api.openai.com/v1/audio/transcriptions"
	whisperDefaultModel    = "whisper-1"
	whisperMaxAudioBytes   = 25 * 1024 * 1024 // OpenAI's hard limit
	whisperRequestTimeout  = 90 * time.Second
)

// NewWhisperBackend constructs a Whisper backend, pulling the API
// key from the OPENAI_API_KEY env var. Returns ErrNotConfigured if
// the key isn't set so main.go can pick StubBackend instead.
func NewWhisperBackend() (*WhisperBackend, error) {
	key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if key == "" {
		return nil, ErrNotConfigured
	}
	model := strings.TrimSpace(os.Getenv("OPENAI_WHISPER_MODEL"))
	if model == "" {
		model = whisperDefaultModel
	}
	return &WhisperBackend{
		apiKey: key,
		model:  model,
		c:      &http.Client{Timeout: whisperRequestTimeout},
	}, nil
}

func (w *WhisperBackend) Name() string { return "whisper" }

// Transcribe downloads audioURL, ships it to OpenAI, and parses the
// verbose_json response back into a Result with word-level timings.
func (w *WhisperBackend) Transcribe(ctx context.Context, audioURL, language string) (*Result, error) {
	audio, filename, err := w.fetchAudio(ctx, audioURL)
	if err != nil {
		return nil, fmt.Errorf("fetch audio: %w", err)
	}

	body, contentType, err := buildWhisperMultipart(audio, filename, w.model, language)
	if err != nil {
		return nil, fmt.Errorf("build multipart: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, whisperEndpoint, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+w.apiKey)
	req.Header.Set("Content-Type", contentType)

	resp, err := w.c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("whisper http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return nil, fmt.Errorf("whisper status %d: %s", resp.StatusCode, string(raw))
	}

	var apiResp whisperResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode whisper response: %w", err)
	}

	return apiResp.toResult(language), nil
}

// fetchAudio streams the audio URL into memory, capped at
// whisperMaxAudioBytes. Returns bytes + a sane filename for the
// multipart upload.
func (w *WhisperBackend) fetchAudio(ctx context.Context, audioURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, audioURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := w.c.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("audio fetch status %d", resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, whisperMaxAudioBytes+1)
	buf, err := io.ReadAll(limited)
	if err != nil {
		return nil, "", err
	}
	if int64(len(buf)) > whisperMaxAudioBytes {
		return nil, "", fmt.Errorf("audio exceeds %d bytes (whisper cap); chunk before transcribing", whisperMaxAudioBytes)
	}
	return buf, filenameForURL(audioURL), nil
}

func filenameForURL(audioURL string) string {
	// Strip query string + path, keep only the last path component.
	u := audioURL
	if i := strings.Index(u, "?"); i >= 0 {
		u = u[:i]
	}
	if i := strings.LastIndex(u, "/"); i >= 0 {
		u = u[i+1:]
	}
	if u == "" {
		u = "audio.mp3"
	}
	if !strings.Contains(u, ".") {
		u += ".mp3"
	}
	return u
}

func buildWhisperMultipart(audio []byte, filename, model, language string) (io.Reader, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	if err := w.WriteField("model", model); err != nil {
		return nil, "", err
	}
	if err := w.WriteField("response_format", "verbose_json"); err != nil {
		return nil, "", err
	}
	if err := w.WriteField("timestamp_granularities[]", "word"); err != nil {
		return nil, "", err
	}
	if language != "" {
		if err := w.WriteField("language", language); err != nil {
			return nil, "", err
		}
	}
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(audio); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return &buf, w.FormDataContentType(), nil
}

// ---------------------------------------------------------------------------
// Whisper API response shape (verbose_json)
// ---------------------------------------------------------------------------

type whisperResponse struct {
	Language string          `json:"language"`
	Text     string          `json:"text"`
	Words    []whisperWord   `json:"words,omitempty"`
	// segments []whisperSegment — not needed for our shape, ignored.
}

type whisperWord struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"` // seconds
	End   float64 `json:"end"`
}

func (r whisperResponse) toResult(requestedLang string) *Result {
	words := make([]Word, 0, len(r.Words))
	for _, w := range r.Words {
		words = append(words, Word{
			Text:    w.Word,
			StartMs: int64(w.Start * 1000),
			EndMs:   int64(w.End * 1000),
		})
	}
	lang := r.Language
	if lang == "" {
		lang = requestedLang
	}
	if lang == "" {
		lang = "en"
	}
	return &Result{
		Language:    lang,
		Format:      "vtt",
		Text:        strings.TrimSpace(r.Text),
		Words:       words,
		BackendName: "whisper",
	}
}

// SelectBackend returns the "best" backend available right now:
// Whisper if OPENAI_API_KEY is set, Stub otherwise. main.go calls
// this once at boot so the rest of the service can stay generic.
func SelectBackend() Backend {
	if w, err := NewWhisperBackend(); err == nil {
		return w
	}
	return StubBackend{}
}
