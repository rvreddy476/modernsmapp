package dubbing

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// A stand-in for api.openai.com that records each call's shape.
type fakeOpenAI struct {
	mu            sync.Mutex
	transcribe    []recordedMultipart
	chat          []map[string]any
	speech        []map[string]any
	translations  string // raw chat content to return
	segments      []map[string]any
	transcribeErr int
}

type recordedMultipart struct {
	fields   map[string]string
	filename string
	fileSize int
	auth     string
}

func (f *fakeOpenAI) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/audio/transcriptions", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		rec := recordedMultipart{fields: map[string]string{}, auth: r.Header.Get("Authorization")}
		for k, v := range r.MultipartForm.Value {
			rec.fields[k] = v[0]
		}
		if fh := r.MultipartForm.File["file"]; len(fh) > 0 {
			rec.filename = fh[0].Filename
			file, _ := fh[0].Open()
			b, _ := io.ReadAll(file)
			rec.fileSize = len(b)
		}
		f.mu.Lock()
		f.transcribe = append(f.transcribe, rec)
		f.mu.Unlock()
		if f.transcribeErr != 0 {
			http.Error(w, "nope", f.transcribeErr)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"language": "english",
			"text":     "hello there. how are you?",
			"segments": f.segments,
		})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		body["_auth"] = r.Header.Get("Authorization")
		f.mu.Lock()
		f.chat = append(f.chat, body)
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": f.translations}}},
		})
	})
	mux.HandleFunc("/v1/audio/speech", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		body["_auth"] = r.Header.Get("Authorization")
		f.mu.Lock()
		f.speech = append(f.speech, body)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("MP3:" + body["input"].(string)))
	})
	return mux
}

func newTestDubber(t *testing.T, fake *fakeOpenAI) (*OpenAIDubber, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	env := map[string]string{
		"OPENAI_API_KEY":  "test-key",
		"OPENAI_BASE_URL": srv.URL + "/v1",
	}
	d, err := NewOpenAIDubber(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	// No ffmpeg on the test machine: clip length is derived from the fake
	// bytes and assembly just concatenates the clips.
	d.probe = func(_ context.Context, path string) (int, error) {
		b, err := os.ReadFile(path)
		if err != nil {
			return 0, err
		}
		return len(b) * 100, nil
	}
	d.assemble = func(_ context.Context, segs []Segment, durationMs int, outPath string) error {
		var all []byte
		for _, s := range segs {
			b, err := os.ReadFile(s.Path)
			if err != nil {
				return err
			}
			all = append(all, b...)
		}
		return os.WriteFile(outPath, all, 0o600)
	}
	return d, srv
}

func sourceAudio(t *testing.T) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "audio.m4a")
	if err := os.WriteFile(path, []byte("fake-audio-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

func TestOpenAIDubberMakesTheThreeCallsWithTheRightShapes(t *testing.T) {
	fake := &fakeOpenAI{
		segments: []map[string]any{
			{"start": 0.0, "end": 1.5, "text": " hello there."},
			{"start": 2.0, "end": 3.5, "text": " how are you?"},
		},
		translations: `{"translations":["नमस्ते।","आप कैसे हैं?"]}`,
	}
	d, _ := newTestDubber(t, fake)
	dir, src := sourceAudio(t)

	out, err := d.Dub(context.Background(), DubInput{
		VideoPath: "video.mp4", SourceAudioPath: src, SourceLanguage: "en", TargetLanguage: "hi",
		DurationMs: 5000, WorkDir: filepath.Join(dir, "work"),
	})
	if err != nil {
		t.Fatalf("Dub: %v", err)
	}

	// (a) transcription: multipart, verbose_json with segment timings.
	if len(fake.transcribe) != 1 {
		t.Fatalf("transcription calls: %d", len(fake.transcribe))
	}
	tr := fake.transcribe[0]
	if tr.auth != "Bearer test-key" {
		t.Errorf("transcription auth: %q", tr.auth)
	}
	if tr.fields["model"] != "whisper-1" || tr.fields["response_format"] != "verbose_json" ||
		tr.fields["timestamp_granularities[]"] != "segment" || tr.fields["language"] != "en" {
		t.Errorf("transcription fields: %v", tr.fields)
	}
	if tr.fileSize != len("fake-audio-bytes") || tr.filename != "audio.m4a" {
		t.Errorf("transcription file: %q %d bytes", tr.filename, tr.fileSize)
	}

	// (b) one chat call carrying every segment, JSON mode, default model.
	if len(fake.chat) != 1 {
		t.Fatalf("chat calls: %d", len(fake.chat))
	}
	chat := fake.chat[0]
	if chat["model"] != "gpt-4o-mini" || chat["_auth"] != "Bearer test-key" {
		t.Errorf("chat model/auth: %v %v", chat["model"], chat["_auth"])
	}
	if rf, _ := chat["response_format"].(map[string]any); rf["type"] != "json_object" {
		t.Errorf("chat must ask for JSON: %v", chat["response_format"])
	}
	msgs := chat["messages"].([]any)
	user := msgs[len(msgs)-1].(map[string]any)["content"].(string)
	if !strings.Contains(user, `"hello there."`) || !strings.Contains(user, `"how are you?"`) {
		t.Errorf("chat user message must carry the segment texts: %s", user)
	}
	system := msgs[0].(map[string]any)["content"].(string)
	if !strings.Contains(system, "hi") || !strings.Contains(system, "same order") {
		t.Errorf("chat system prompt must name the target and the ordering rule: %s", system)
	}

	// (c) one speech call per segment with the translated line.
	if len(fake.speech) != 2 {
		t.Fatalf("speech calls: %d", len(fake.speech))
	}
	for i, want := range []string{"नमस्ते।", "आप कैसे हैं?"} {
		sp := fake.speech[i]
		if sp["input"] != want || sp["model"] != "tts-1" || sp["voice"] != "alloy" || sp["response_format"] != "mp3" {
			t.Errorf("speech %d: %v", i, sp)
		}
	}

	// (d) the assembled track is the returned file.
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "MP3:नमस्ते।MP3:आप कैसे हैं?" {
		t.Errorf("assembled bytes: %q", got)
	}
}

func TestOpenAIDubberRejectsMismatchedTranslationCount(t *testing.T) {
	fake := &fakeOpenAI{
		segments:     []map[string]any{{"start": 0.0, "end": 1.0, "text": "one"}, {"start": 1.0, "end": 2.0, "text": "two"}},
		translations: `{"translations":["uno"]}`,
	}
	d, _ := newTestDubber(t, fake)
	dir, src := sourceAudio(t)
	_, err := d.Dub(context.Background(), DubInput{SourceAudioPath: src, TargetLanguage: "es", DurationMs: 3000, WorkDir: dir})
	if err == nil || !strings.Contains(err.Error(), "1 segments for 2 inputs") {
		t.Errorf("want a count mismatch error, got %v", err)
	}
	if len(fake.speech) != 0 {
		t.Errorf("no speech may be synthesised from a bad translation")
	}
}

func TestOpenAIDubberSurfacesProviderErrors(t *testing.T) {
	fake := &fakeOpenAI{transcribeErr: 429}
	d, _ := newTestDubber(t, fake)
	dir, src := sourceAudio(t)
	_, err := d.Dub(context.Background(), DubInput{SourceAudioPath: src, TargetLanguage: "hi", DurationMs: 3000, WorkDir: dir})
	if err == nil || !strings.Contains(err.Error(), "status 429") {
		t.Errorf("want the provider status in the error, got %v", err)
	}
}

func TestParseTranslationsAcceptsObjectOrArray(t *testing.T) {
	for _, in := range []string{
		`{"translations":["a","b"]}`,
		`["a","b"]`,
		"```json\n{\"lines\":[\"a\",\"b\"]}\n```",
	} {
		got, err := parseTranslations(in)
		if err != nil || len(got) != 2 || got[0] != "a" || got[1] != "b" {
			t.Errorf("%s: got %v, %v", in, got, err)
		}
	}
	if _, err := parseTranslations(`{"count": 2}`); err == nil {
		t.Error("an object with no array must be an error")
	}
}

func TestNewOpenAIDubberNeedsAKey(t *testing.T) {
	_, err := NewOpenAIDubber(func(string) string { return "" })
	if !errors.Is(err, ErrNotConfigured) {
		t.Errorf("want ErrNotConfigured, got %v", err)
	}
}

func TestSelectRefusesTheStubInProduction(t *testing.T) {
	env := map[string]string{"MEDIA_DUBBING_BACKEND": "stub", "ENV": "production"}
	getenv := func(k string) string { return env[k] }
	if _, err := Select(getenv); !errors.Is(err, ErrStubInProduction) {
		t.Errorf("production stub: want ErrStubInProduction, got %v", err)
	}

	env["ENV"] = "dev"
	d, err := Select(getenv)
	if err != nil || d == nil || d.Name() != "stub" {
		t.Errorf("dev stub: got %v, %v", d, err)
	}

	env = map[string]string{}
	d, err = Select(getenv)
	if err != nil || d != nil {
		t.Errorf("unset: want (nil, nil), got %v, %v", d, err)
	}

	env["MEDIA_DUBBING_BACKEND"] = "openai"
	if _, err := Select(getenv); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("openai without key: want ErrNotConfigured, got %v", err)
	}

	env["MEDIA_DUBBING_BACKEND"] = "elevenlabs"
	if _, err := Select(getenv); err == nil {
		t.Error("unknown backend must be refused")
	}
}

func TestStubDubberCopiesTheSource(t *testing.T) {
	dir, src := sourceAudio(t)
	out, err := StubDubber{}.Dub(context.Background(), DubInput{SourceAudioPath: src, TargetLanguage: "hi", WorkDir: filepath.Join(dir, "w")})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	if string(b) != "fake-audio-bytes" || out == src {
		t.Errorf("stub must copy the source to a new file: %q -> %q", src, out)
	}
}
