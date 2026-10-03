package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Mechanic M15: the clip client calls media-service's internal dating-clip
// routes with the internal key and no identity, and maps its refusals.
func TestHTTPMediaClipClient(t *testing.T) {
	media, owner := uuid.New(), uuid.New()
	var refuse string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Service-Key") != "k" || r.Header.Get("X-User-Id") != "" {
			t.Errorf("%s %s: key %q user %q", r.Method, r.URL.Path, r.Header.Get("X-Internal-Service-Key"), r.Header.Get("X-User-Id"))
		}
		if !strings.HasPrefix(r.URL.Path, MediaDatingClipsPath+media.String()) {
			t.Errorf("path %s", r.URL.Path)
		}
		if refuse != "" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"error":{"code":"` + refuse + `","details":{"max_ms":20000}}}`))
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]string
		_ = json.Unmarshal(raw, &body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/owner-status"):
			if r.URL.Query().Get("requester_user_id") != owner.String() {
				t.Errorf("owner-status requester %q", r.URL.Query().Get("requester_user_id"))
			}
			_, _ = w.Write([]byte(`{"data":{"owner_user_id":"` + owner.String() + `","kind":"audio","processing":"ready","moderation":"review","duration_ms":7000,"usable":false}}`))
		case strings.HasSuffix(r.URL.Path, "/prepare"):
			if body["requester_user_id"] != owner.String() {
				t.Errorf("prepare body %v", body)
			}
			_, _ = w.Write([]byte(`{"data":{"kind":"audio","duration_ms":7000}}`))
		case strings.HasSuffix(r.URL.Path, "/delivery-url"):
			if body["owner_user_id"] != owner.String() {
				t.Errorf("delivery body %v", body)
			}
			_, _ = w.Write([]byte(`{"data":{"kind":"audio","url":"https://cdn/x","expires_at":"2026-10-03T10:00:00Z"}}`))
		case r.Method == http.MethodDelete:
			_, _ = w.Write([]byte(`{"data":{"status":"deleted"}}`))
		}
	}))
	defer srv.Close()
	c := NewHTTPMediaClipClient(srv.URL, "k", nil)
	ctx := context.Background()
	st, err := c.ClipOwnerStatus(ctx, media, owner)
	if err != nil || st.Kind != "audio" || st.Moderation != "review" || st.DurationMs != 7000 {
		t.Fatalf("owner-status %+v %v", st, err)
	}
	if kind, ms, err := c.PrepareClip(ctx, media, owner); err != nil || kind != "audio" || ms != 7000 {
		t.Fatalf("prepare %s %d %v", kind, ms, err)
	}
	if d, err := c.ClipDeliveryURL(ctx, media, owner); err != nil || d.URL != "https://cdn/x" {
		t.Fatalf("delivery %+v %v", d, err)
	}
	if err := c.DeleteClip(ctx, media, owner); err != nil {
		t.Fatalf("delete %v", err)
	}
	for code, want := range map[string]error{"CLIP_NOT_FOUND": ErrClipMediaNotFound, "CLIP_NOT_READY": ErrClipNotReady, "CLIP_UNSUPPORTED": ErrClipUnsupported} {
		refuse = code
		if _, _, err := c.PrepareClip(ctx, media, owner); !errors.Is(err, want) {
			t.Fatalf("%s mapped to %v", code, err)
		}
	}
	refuse = "CLIP_TOO_LONG"
	var long *ClipTooLongError
	if _, _, err := c.PrepareClip(ctx, media, owner); !errors.As(err, &long) || long.MaxMs != 20000 {
		t.Fatalf("CLIP_TOO_LONG mapped to %v", err)
	}
}
