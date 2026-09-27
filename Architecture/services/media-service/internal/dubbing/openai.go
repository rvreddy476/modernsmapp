package dubbing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// OpenAIDubber re-voices a video with three OpenAI calls and one ffmpeg run:
//
//  1. POST /audio/transcriptions (verbose_json) — the source speech as timed
//     segments
//  2. POST /chat/completions — every segment translated in one call, as a
//     JSON array of the same length
//  3. POST /audio/speech per segment — the translated line as an mp3 clip
//  4. ffmpeg assembles the clips at their start times (AssemblyFilter)
//
// Enabled with MEDIA_DUBBING_BACKEND=openai and OPENAI_API_KEY. Models and
// voice come from OPENAI_WHISPER_MODEL (whisper-1), OPENAI_DUB_TEXT_MODEL
// (gpt-4o-mini), OPENAI_DUB_TTS_MODEL (tts-1), OPENAI_DUB_VOICE (alloy).
// OPENAI_BASE_URL overrides the API root, which is how the tests stand in for
// the service.
type OpenAIDubber struct {
	apiKey       string
	baseURL      string
	whisperModel string
	textModel    string
	ttsModel     string
	voice        string
	c            *http.Client

	// probe and assemble are the ffmpeg seams; tests replace them.
	probe    func(ctx context.Context, path string) (int, error)
	assemble func(ctx context.Context, segs []Segment, durationMs int, outPath string) error
}

const (
	openAIDefaultBaseURL   = "https://api.openai.com/v1"
	openAIDefaultWhisper   = "whisper-1"
	openAIDefaultTextModel = "gpt-4o-mini"
	openAIDefaultTTSModel  = "tts-1"
	openAIDefaultVoice     = "alloy"
	openAIRequestTimeout   = 120 * time.Second
	// openAIMaxSourceBytes is the transcription upload cap (OpenAI's limit).
	openAIMaxSourceBytes = 25 * 1024 * 1024
	// openAIMaxSegments bounds the TTS fan-out for one track.
	openAIMaxSegments = 400
	// openAIMaxTTSChars is the speech endpoint's input cap.
	openAIMaxTTSChars = 4096
)

// NewOpenAIDubber reads its configuration from getenv.
func NewOpenAIDubber(getenv func(string) string) (*OpenAIDubber, error) {
	key := strings.TrimSpace(getenv("OPENAI_API_KEY"))
	if key == "" {
		return nil, fmt.Errorf("%w: MEDIA_DUBBING_BACKEND=openai needs OPENAI_API_KEY", ErrNotConfigured)
	}
	pick := func(name, fallback string) string {
		if v := strings.TrimSpace(getenv(name)); v != "" {
			return v
		}
		return fallback
	}
	return &OpenAIDubber{
		apiKey:       key,
		baseURL:      strings.TrimRight(pick("OPENAI_BASE_URL", openAIDefaultBaseURL), "/"),
		whisperModel: pick("OPENAI_WHISPER_MODEL", openAIDefaultWhisper),
		textModel:    pick("OPENAI_DUB_TEXT_MODEL", openAIDefaultTextModel),
		ttsModel:     pick("OPENAI_DUB_TTS_MODEL", openAIDefaultTTSModel),
		voice:        pick("OPENAI_DUB_VOICE", openAIDefaultVoice),
		c:            &http.Client{Timeout: openAIRequestTimeout},
		probe:        ProbeDurationMs,
		assemble:     AssembleWithFFmpeg,
	}, nil
}

func (d *OpenAIDubber) Name() string { return "openai" }

// Dub runs the four steps. Intermediate clips live in in.WorkDir (or a temp
// dir that becomes the parent of the returned file).
func (d *OpenAIDubber) Dub(ctx context.Context, in DubInput) (string, error) {
	if in.SourceAudioPath == "" {
		return "", errors.New("dubbing: no source audio")
	}
	if in.DurationMs <= 0 {
		return "", errors.New("dubbing: video duration unknown")
	}
	if strings.TrimSpace(in.TargetLanguage) == "" {
		return "", errors.New("dubbing: target language required")
	}
	dir, err := workDir(in.WorkDir, "dub-openai-")
	if err != nil {
		return "", err
	}

	transcript, err := d.transcribe(ctx, in.SourceAudioPath, in.SourceLanguage)
	if err != nil {
		return "", fmt.Errorf("transcribe: %w", err)
	}
	transcript = compactSegments(transcript)
	if len(transcript) == 0 {
		return "", errors.New("dubbing: no speech found in the source audio")
	}
	if len(transcript) > openAIMaxSegments {
		return "", fmt.Errorf("dubbing: %d segments exceeds the %d cap", len(transcript), openAIMaxSegments)
	}

	texts := make([]string, len(transcript))
	for i, s := range transcript {
		texts[i] = s.Text
	}
	sourceLang := in.SourceLanguage
	if sourceLang == "" && len(transcript) > 0 {
		sourceLang = transcript[0].Language
	}
	translated, err := d.translate(ctx, texts, sourceLang, in.TargetLanguage)
	if err != nil {
		return "", fmt.Errorf("translate: %w", err)
	}

	segments := make([]Segment, 0, len(transcript))
	for i, s := range transcript {
		line := strings.TrimSpace(translated[i])
		if line == "" {
			continue
		}
		if len(line) > openAIMaxTTSChars {
			line = line[:openAIMaxTTSChars]
		}
		clip := filepath.Join(dir, fmt.Sprintf("seg-%03d.mp3", i))
		if err := d.speech(ctx, line, clip); err != nil {
			return "", fmt.Errorf("speech segment %d: %w", i, err)
		}
		audioMs, err := d.probe(ctx, clip)
		if err != nil {
			return "", fmt.Errorf("probe segment %d: %w", i, err)
		}
		segments = append(segments, Segment{
			StartMs: int(s.Start*1000 + 0.5),
			EndMs:   int(s.End*1000 + 0.5),
			AudioMs: audioMs,
			Path:    clip,
		})
	}
	if len(segments) == 0 {
		return "", errors.New("dubbing: translation produced no speakable lines")
	}

	out := filepath.Join(dir, "dubbed.m4a")
	if err := d.assemble(ctx, segments, in.DurationMs, out); err != nil {
		return "", fmt.Errorf("assemble: %w", err)
	}
	return out, nil
}

// ── step 1: transcription ────────────────────────────────────────────────

type transcriptSegment struct {
	Start    float64
	End      float64
	Text     string
	Language string
}

type verboseTranscription struct {
	Language string `json:"language"`
	Text     string `json:"text"`
	Segments []struct {
		Start float64 `json:"start"`
		End   float64 `json:"end"`
		Text  string  `json:"text"`
	} `json:"segments"`
}

func (d *OpenAIDubber) transcribe(ctx context.Context, audioPath, language string) ([]transcriptSegment, error) {
	audio, err := os.ReadFile(audioPath)
	if err != nil {
		return nil, err
	}
	if len(audio) > openAIMaxSourceBytes {
		return nil, fmt.Errorf("source audio %d bytes exceeds the %d transcription cap", len(audio), openAIMaxSourceBytes)
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fields := [][2]string{
		{"model", d.whisperModel},
		{"response_format", "verbose_json"},
		{"timestamp_granularities[]", "segment"},
	}
	if language != "" {
		fields = append(fields, [2]string{"language", language})
	}
	for _, f := range fields {
		if err := w.WriteField(f[0], f[1]); err != nil {
			return nil, err
		}
	}
	name := filepath.Base(audioPath)
	if !strings.Contains(name, ".") {
		name += ".m4a"
	}
	part, err := w.CreateFormFile("file", name)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(audio); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}

	var resp verboseTranscription
	if err := d.call(ctx, "/audio/transcriptions", w.FormDataContentType(), &buf, &resp); err != nil {
		return nil, err
	}
	out := make([]transcriptSegment, 0, len(resp.Segments))
	for _, s := range resp.Segments {
		out = append(out, transcriptSegment{Start: s.Start, End: s.End, Text: strings.TrimSpace(s.Text), Language: resp.Language})
	}
	if len(out) == 0 && strings.TrimSpace(resp.Text) != "" {
		// A backend that returned no segment timing: one utterance from 0.
		out = append(out, transcriptSegment{Start: 0, End: 0, Text: strings.TrimSpace(resp.Text), Language: resp.Language})
	}
	return out, nil
}

// compactSegments drops empty lines and keeps time monotonic.
func compactSegments(in []transcriptSegment) []transcriptSegment {
	out := in[:0]
	last := 0.0
	for _, s := range in {
		if s.Text == "" {
			continue
		}
		if s.Start < last {
			s.Start = last
		}
		if s.End < s.Start {
			s.End = s.Start
		}
		last = s.Start
		out = append(out, s)
	}
	return out
}

// ── step 2: translation ──────────────────────────────────────────────────

type chatRequest struct {
	Model          string        `json:"model"`
	Messages       []chatMessage `json:"messages"`
	Temperature    float64       `json:"temperature"`
	ResponseFormat struct {
		Type string `json:"type"`
	} `json:"response_format"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

func (d *OpenAIDubber) translate(ctx context.Context, texts []string, sourceLang, targetLang string) ([]string, error) {
	payload, err := json.Marshal(map[string]any{"segments": texts})
	if err != nil {
		return nil, err
	}
	from := "the source language"
	if sourceLang != "" {
		from = "language tag " + sourceLang
	}
	req := chatRequest{
		Model:       d.textModel,
		Temperature: 0.2,
		Messages: []chatMessage{
			{Role: "system", Content: "You translate subtitle segments for a dubbed voice-over. " +
				"Translate every segment from " + from + " into language tag " + targetLang + ". " +
				"Keep each translation natural to speak aloud and about the same length as the original. " +
				"Reply with a JSON object {\"translations\": [...]} containing exactly one string per input " +
				"segment, in the same order, and nothing else."},
			{Role: "user", Content: string(payload)},
		},
	}
	req.ResponseFormat.Type = "json_object"
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	var resp chatResponse
	if err := d.call(ctx, "/chat/completions", "application/json", bytes.NewReader(body), &resp); err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return nil, errors.New("chat completion returned no choices")
	}
	translations, err := parseTranslations(resp.Choices[0].Message.Content)
	if err != nil {
		return nil, err
	}
	if len(translations) != len(texts) {
		return nil, fmt.Errorf("translation returned %d segments for %d inputs", len(translations), len(texts))
	}
	return translations, nil
}

// parseTranslations accepts {"translations":[...]}, any single-array object,
// or a bare array.
func parseTranslations(content string) ([]string, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	var arr []string
	if err := json.Unmarshal([]byte(content), &arr); err == nil {
		return arr, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &obj); err != nil {
		return nil, fmt.Errorf("translation response is not JSON: %w", err)
	}
	if raw, ok := obj["translations"]; ok {
		if err := json.Unmarshal(raw, &arr); err == nil {
			return arr, nil
		}
	}
	for _, raw := range obj {
		if err := json.Unmarshal(raw, &arr); err == nil {
			return arr, nil
		}
	}
	return nil, errors.New("translation response carries no string array")
}

// ── step 3: speech ───────────────────────────────────────────────────────

func (d *OpenAIDubber) speech(ctx context.Context, text, outPath string) error {
	body, err := json.Marshal(map[string]any{
		"model":           d.ttsModel,
		"voice":           d.voice,
		"input":           text,
		"response_format": "mp3",
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.baseURL+"/audio/speech", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+d.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("speech status %d: %s", resp.StatusCode, string(raw))
	}
	f, err := os.OpenFile(outPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// call posts body to path and decodes a JSON reply.
func (d *OpenAIDubber) call(ctx context.Context, path, contentType string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+d.apiKey)
	req.Header.Set("Content-Type", contentType)
	resp, err := d.c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s status %d: %s", path, resp.StatusCode, string(raw))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
