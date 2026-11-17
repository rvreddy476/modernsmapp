package http

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/media-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// POST /v1/audio/voiceover (reels-16, gate line 6): the body is capped before
// it is buffered, and bytes that are not an audio container never reach
// ffmpeg. Both refusals happen before the service touches storage, so the
// handler runs here with no service behind it.

func voiceoverRequest(t *testing.T, part []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("audio", "voice.webm")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(part); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/voiceover", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-User-Id", uuid.NewString())
	return req
}

func serveVoiceover(t *testing.T, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	noop := func(c *gin.Context) { c.Next() }
	New(nil).RegisterAudioRoutes(r, noop)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestVoiceoverBodyOverTheCapIs413(t *testing.T) {
	// A WebM header followed by one byte more than the cap.
	part := make([]byte, service.MaxVoiceoverUploadBytes+1)
	copy(part, []byte{0x1A, 0x45, 0xDF, 0xA3, 0, 0, 0, 0, 0, 0, 0, 0})
	w := serveVoiceover(t, voiceoverRequest(t, part))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413: %s", w.Code, w.Body.String())
	}
	if got := errorCode(t, w); got != "PAYLOAD_TOO_LARGE" {
		t.Fatalf("code %q, want PAYLOAD_TOO_LARGE", got)
	}
}

// A body far over the cap is cut off by MaxBytesReader while the multipart
// parser reads it: still a clear 413, not a 400 "file required".
func TestVoiceoverBodyFarOverTheCapIsCutOffAt413(t *testing.T) {
	part := make([]byte, service.MaxVoiceoverUploadBytes+multipartOverheadBytes+1)
	copy(part, []byte{0x1A, 0x45, 0xDF, 0xA3, 0, 0, 0, 0, 0, 0, 0, 0})
	w := serveVoiceover(t, voiceoverRequest(t, part))
	if w.Code != http.StatusRequestEntityTooLarge || errorCode(t, w) != "PAYLOAD_TOO_LARGE" {
		t.Fatalf("status %d code %q, want 413 PAYLOAD_TOO_LARGE", w.Code, errorCode(t, w))
	}
}

func TestVoiceoverThatIsNotAudioIs415BeforeTheTool(t *testing.T) {
	for name, part := range map[string][]byte{
		"hls playlist":  []byte("#EXTM3U\n#EXT-X-VERSION:3\nfile:///etc/passwd\n"),
		"concat list":   []byte("ffconcat version 1.0\nfile '/etc/passwd'\n"),
		"html":          []byte("<html><script>alert(1)</script></html>"),
		"jpeg":          {0xFF, 0xD8, 0xFF, 0xE0, 0, 0x10, 'J', 'F', 'I', 'F', 0, 1},
		"too short":     []byte("RIFF"),
		"empty-ish zip": {'P', 'K', 3, 4, 0, 0, 0, 0, 0, 0, 0, 0},
	} {
		w := serveVoiceover(t, voiceoverRequest(t, part))
		if w.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("%s: status %d, want 415: %s", name, w.Code, w.Body.String())
		}
		if got := errorCode(t, w); got != "UNSUPPORTED_MEDIA_TYPE" {
			t.Fatalf("%s: code %q", name, got)
		}
	}
}
