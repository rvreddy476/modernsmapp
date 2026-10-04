package facecompare

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestModeSelection(t *testing.T) {
	if c, err := New("mock", true, "http://m", "k"); err == nil || c != nil {
		t.Fatal("mock accepted in production")
	}
	if c, err := New("mock", false, "", ""); err != nil || c.NeedsReference() {
		t.Fatalf("mock in dev: %v", err)
	}
	if _, err := New("http", true, "", "k"); err == nil {
		t.Fatal("http without media URL accepted")
	}
	if c, err := New("http", true, "http://m", "k"); err != nil || !c.NeedsReference() {
		t.Fatalf("http: %v", err)
	}
	if _, err := New("magic", false, "http://m", "k"); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

func TestHTTPCompare(t *testing.T) {
	var body map[string]string
	var key, uid string
	status, payload := 200, `{"data":{"similarity":91.5,"face_count_source":1,"face_count_target":1,"match":true,"provider":"rekognition"}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, uid = r.Header.Get("X-Internal-Service-Key"), r.Header.Get("X-User-Id")
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()
	c := NewHTTP(srv.URL, "ik", nil)
	req := Request{SourceMediaID: uuid.New(), TargetMediaID: uuid.New(), RequesterUserID: uuid.New()}
	res, err := c.CompareFaces(context.Background(), req)
	if err != nil || res.Similarity != 91.5 {
		t.Fatalf("%+v %v", res, err)
	}
	if key != "ik" || uid != "" || body["requester_user_id"] != req.RequesterUserID.String() || body["source_media_id"] != req.SourceMediaID.String() {
		t.Fatalf("request key=%q uid=%q body=%v", key, uid, body)
	}
	for st, want := range map[int]error{404: ErrMediaNotFound, 422: ErrImageUnsupported, 503: ErrUnavailable} {
		status = st
		if _, err := c.CompareFaces(context.Background(), req); !errors.Is(err, want) {
			t.Errorf("status %d: %v", st, err)
		}
	}
	status, payload = 200, `{"data":{"similarity":140}}`
	if _, err := c.CompareFaces(context.Background(), req); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("out of range: %v", err)
	}
	if _, err := c.CompareFaces(context.Background(), Request{SourceMediaID: uuid.New(), RequesterUserID: uuid.New()}); err == nil {
		t.Fatal("missing target accepted")
	}
}
