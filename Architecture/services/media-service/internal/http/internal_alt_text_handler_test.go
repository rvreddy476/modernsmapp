package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type fakeInternalAltText struct {
	calls      int
	mediaID    uuid.UUID
	uploaderID uuid.UUID
	alt        string
	decorative bool
	err        error
}

func (f *fakeInternalAltText) UpdateAltTextWithDecorative(_ context.Context, mediaID, userID uuid.UUID, alt string, decorative bool) error {
	f.calls++
	f.mediaID, f.uploaderID, f.alt, f.decorative = mediaID, userID, alt, decorative
	return f.err
}

const altKey = "alt-internal-key"

func altRig(t *testing.T, svc internalAltTextService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(nil).WithInternalKey(altKey)
	h.internalAltText = svc
	noop := func(c *gin.Context) { c.Next() }
	h.RegisterRoutes(r, noop, noop)
	return r
}

func altCall(r *gin.Engine, mediaID, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPatch, "/v1/media/internal/"+mediaID+"/alt-text", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", altKey)
	for k, v := range headers {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestInternalAltText_WritesTheUploadersOwnAsset(t *testing.T) {
	fake := &fakeInternalAltText{}
	r := altRig(t, fake)
	media, owner := uuid.New(), uuid.New()
	w := altCall(r, media.String(), `{"uploader_id":"`+owner.String()+`","alt_text":"  A red bicycle  "}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.calls != 1 || fake.mediaID != media || fake.uploaderID != owner || fake.alt != "A red bicycle" || fake.decorative {
		t.Fatalf("forwarded %+v", fake)
	}
}

func TestInternalAltText_Refusals(t *testing.T) {
	media, owner := uuid.New(), uuid.New()
	good := `{"uploader_id":"` + owner.String() + `","alt_text":"x"}`
	cases := []struct {
		name    string
		media   string
		body    string
		headers map[string]string
		want    int
	}{
		{"no key", media.String(), good, map[string]string{"X-Internal-Service-Key": ""}, http.StatusUnauthorized},
		{"wrong key", media.String(), good, map[string]string{"X-Internal-Service-Key": "nope"}, http.StatusUnauthorized},
		{"gateway identity", media.String(), good, map[string]string{"X-User-Id": owner.String()}, http.StatusForbidden},
		{"bad media id", "not-a-uuid", good, nil, http.StatusBadRequest},
		{"bad uploader", media.String(), `{"uploader_id":"x","alt_text":"x"}`, nil, http.StatusBadRequest},
		{"nil uploader", media.String(), `{"uploader_id":"` + uuid.Nil.String() + `","alt_text":"x"}`, nil, http.StatusBadRequest},
		{"nothing to write", media.String(), `{"uploader_id":"` + owner.String() + `","alt_text":"   "}`, nil, http.StatusBadRequest},
		{"too long", media.String(), `{"uploader_id":"` + owner.String() + `","alt_text":"` + strings.Repeat("a", 1001) + `"}`, nil, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeInternalAltText{}
			w := altCall(altRig(t, fake), tc.media, tc.body, tc.headers)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			if fake.calls != 0 {
				t.Fatalf("refused request still wrote the alt text")
			}
		})
	}
}

func TestInternalAltText_SomebodyElsesAssetIs404(t *testing.T) {
	fake := &fakeInternalAltText{err: pgx.ErrNoRows}
	w := altCall(altRig(t, fake), uuid.NewString(), `{"uploader_id":"`+uuid.NewString()+`","alt_text":"x"}`, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
}
