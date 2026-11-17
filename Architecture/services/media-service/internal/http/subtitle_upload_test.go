package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// A malformed caption file is refused with its own 400 code before the
// service (ownership, store) is reached; the handler holds no service here,
// so reaching it would panic.
func TestCreateSubtitleRefusesMalformedFilesWithClearCodes(t *testing.T) {
	r := captionRouter(&fakeClipsService{})
	for _, tc := range []struct{ name, body, code string }{
		{"no WEBVTT header", `{"language":"en","source":"manual","format":"vtt","content":"00:00:01.000 --> 00:00:02.000\nhi\n"}`, "CAPTION_BAD_HEADER"},
		{"cues out of order", `{"language":"en","source":"manual","format":"vtt","content":"WEBVTT\n\n00:05.000 --> 00:06.000\na\n\n00:02.000 --> 00:04.000\nb\n"}`, "CAPTION_OUT_OF_ORDER"},
		{"end before start", `{"language":"en","source":"manual","format":"srt","content":"1\n00:00:02,000 --> 00:00:01,000\nhi\n"}`, "CAPTION_BAD_TIMING"},
		{"unknown format", `{"language":"en","source":"manual","format":"ass","content":"x"}`, "CAPTION_FORMAT_UNSUPPORTED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/subtitles/"+uuid.NewString(), strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-User-Id", uuid.NewString())
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"`+tc.code+`"`) {
				t.Fatalf("body %s does not carry code %s", rec.Body.String(), tc.code)
			}
		})
	}
}
