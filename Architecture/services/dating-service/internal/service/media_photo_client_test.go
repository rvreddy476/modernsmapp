package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// Lane D6 — the dating → media-service dating photo client over httptest.

type mediaPhotoServer struct {
	mu     sync.Mutex
	status int
	body   string
	reqs   []recordedMediaRequest
}

type recordedMediaRequest struct {
	method, path, query, key, userHeader string
	body                                 map[string]any
}

func (m *mediaPhotoServer) handler(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	m.reqs = append(m.reqs, recordedMediaRequest{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery,
		key: r.Header.Get("X-Internal-Service-Key"), userHeader: r.Header.Get("X-User-Id") + r.Header.Get("X-Scopes"), body: body})
	w.WriteHeader(m.status)
	_, _ = io.WriteString(w, m.body)
}

func (m *mediaPhotoServer) set(status int, body string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status, m.body = status, body
}

func (m *mediaPhotoServer) last() recordedMediaRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reqs[len(m.reqs)-1]
}

func TestHTTPMediaPhotoClient_Contract(t *testing.T) {
	srv := &mediaPhotoServer{}
	ts := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer ts.Close()
	c := NewHTTPMediaPhotoClient(ts.URL+"/", "svc-key", nil)
	ctx := context.Background()
	media, owner := uuid.New(), uuid.New()
	statusBody := `{"data":{"media_id":"` + media.String() + `","owner_matches":true,"kind":"image","status":"ready",` +
		`"moderation_status":"passed","content_type":"image/jpeg","moderation_scanned":true,"moderation_scanner":"rekognition",` +
		`"moderation_labels":[{"name":"Revealing Clothes","parent":"Suggestive","confidence":88.5}],"prepared":true,"face_count":1}}`

	srv.set(http.StatusOK, statusBody)
	st, err := c.PhotoOwnerStatus(ctx, media, owner)
	if err != nil || !st.Usable() || len(st.ModerationLabels) != 1 || st.ModerationLabels[0].Parent != "Suggestive" ||
		st.FaceCount == nil || *st.FaceCount != 1 {
		t.Fatalf("owner-status = %+v, %v", st, err)
	}
	req := srv.last()
	if req.method != http.MethodGet || req.path != "/internal/v1/media/dating-photos/"+media.String()+"/owner-status" ||
		req.query != "requester_user_id="+owner.String() || req.key != "svc-key" || req.userHeader != "" {
		t.Fatalf("owner-status request = %+v; want GET with the key, the requester in the query and no user headers", req)
	}

	for code, want := range map[int]error{
		http.StatusNotFound: ErrPhotoMediaNotFound, http.StatusConflict: ErrPhotoMediaNotReady,
		http.StatusUnprocessableEntity: ErrPhotoMediaUnsupported, http.StatusServiceUnavailable: ErrPhotoMediaUnavailable,
		http.StatusUnauthorized: ErrPhotoMediaUnavailable,
	} {
		srv.set(code, `{}`)
		if _, err := c.PreparePhoto(ctx, media, owner, true); !errors.Is(err, want) {
			t.Fatalf("prepare %d: err=%v, want %v", code, err, want)
		}
	}
	req = srv.last()
	if req.method != http.MethodPost || !strings.HasSuffix(req.path, "/prepare") ||
		req.body["requester_user_id"] != owner.String() || req.body["detect_faces"] != true {
		t.Fatalf("prepare request = %+v", req)
	}

	srv.set(http.StatusOK, strings.Replace(statusBody, media.String(), uuid.NewString(), 1))
	if _, err := c.PhotoOwnerStatus(ctx, media, owner); !errors.Is(err, ErrPhotoMediaUnavailable) {
		t.Fatalf("answer for another media: err=%v, want unavailable", err)
	}
	srv.set(http.StatusOK, `not json`)
	if _, err := c.PhotoOwnerStatus(ctx, media, owner); !errors.Is(err, ErrPhotoMediaUnavailable) {
		t.Fatalf("garbage: err=%v", err)
	}

	srv.set(http.StatusOK, `{"data":{"url":"https://cdn.test/user/x/dating-blurred/y.jpg?Signature=s","variant":"blurred"}}`)
	if u, err := c.PhotoDeliveryURL(ctx, media, owner, PhotoVariantBlurred); err != nil || !strings.HasPrefix(u, "https://cdn.test/") {
		t.Fatalf("delivery = %q, %v", u, err)
	}
	if req = srv.last(); req.body["owner_user_id"] != owner.String() || req.body["variant"] != "blurred" {
		t.Fatalf("delivery request = %+v", req)
	}
	srv.set(http.StatusOK, `{"data":{"url":"/relative"}}`)
	if _, err := c.PhotoDeliveryURL(ctx, media, owner, PhotoVariantFull); !errors.Is(err, ErrPhotoMediaUnavailable) {
		t.Fatalf("relative delivery URL: err=%v", err)
	}
	srv.set(http.StatusConflict, `{}`)
	if _, err := c.PhotoDeliveryURL(ctx, media, owner, PhotoVariantFull); !errors.Is(err, ErrPhotoMediaNotReady) {
		t.Fatalf("delivery 409: err=%v", err)
	}

	for _, code := range []int{http.StatusOK, http.StatusNotFound, http.StatusConflict} {
		srv.set(code, `{}`)
		if err := c.DeletePhotoMedia(ctx, media, owner); err != nil {
			t.Fatalf("delete %d: err=%v, want nil", code, err)
		}
	}
	if req = srv.last(); req.method != http.MethodDelete || req.query != "requester_user_id="+owner.String() {
		t.Fatalf("delete request = %+v", req)
	}
	srv.set(http.StatusServiceUnavailable, `{}`)
	if err := c.DeletePhotoMedia(ctx, media, owner); !errors.Is(err, ErrPhotoMediaUnavailable) {
		t.Fatalf("delete 503: err=%v", err)
	}

	ts.Close()
	if _, err := c.PhotoOwnerStatus(ctx, media, owner); !errors.Is(err, ErrPhotoMediaUnavailable) {
		t.Fatalf("unreachable: err=%v", err)
	}
}
