package mediaclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestVerifyOwned(t *testing.T) {
	owner, other, id := uuid.New(), uuid.New(), uuid.New()
	uploader, fileType, processing, moderation, status := owner, "image", "ready", "passed", 200
	var key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key = r.Header.Get("X-Internal-Service-Key")
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"data":{"id":%q,"uploader_id":%q,"file_type":%q,"processing_status":%q,"moderation_status":%q}}`,
			id, uploader, fileType, processing, moderation)
	}))
	defer srv.Close()
	c := New(srv.URL, "ik")
	ctx := context.Background()
	if err := c.VerifyOwned(ctx, id, owner, KindImage); err != nil || key != "ik" {
		t.Fatalf("owned image: %v key=%q", err, key)
	}
	if err := c.VerifyOwned(ctx, id, other, KindImage); !errors.Is(err, ErrNotYours) {
		t.Fatalf("someone else's: %v", err)
	}
	fileType = "video"
	if err := c.VerifyOwned(ctx, id, owner, KindImage, KindDocument); !errors.Is(err, ErrWrongKind) {
		t.Fatalf("video: %v", err)
	}
	fileType, processing = "document", "processing"
	if err := c.VerifyOwned(ctx, id, owner, KindImage, KindDocument); !errors.Is(err, ErrNotReady) {
		t.Fatalf("processing: %v", err)
	}
	processing, moderation = "ready", "rejected"
	if err := c.VerifyOwned(ctx, id, owner); !errors.Is(err, ErrNotPassed) {
		t.Fatalf("moderation: %v", err)
	}
	status = 404
	if err := c.VerifyOwned(ctx, id, owner); !errors.Is(err, ErrNotFound) {
		t.Fatalf("404: %v", err)
	}
	status = 502
	if err := c.VerifyOwned(ctx, id, owner); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("502: %v", err)
	}
	var nilClient *Client
	if err := nilClient.VerifyOwned(ctx, id, owner); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil client: %v", err)
	}
	if New("  ", "k") != nil {
		t.Fatal("blank URL built a client")
	}
}

func TestFetchImage(t *testing.T) {
	id := uuid.New()
	ct, status := "image/png", 200
	var key, auth, uid string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, auth, uid = r.Header.Get("X-Internal-Service-Key"), r.Header.Get("X-Service-Authorization"), r.Header.Get("X-User-Id")
		if r.URL.Path != "/v1/media/internal/"+id.String()+"/image-bytes" {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(status)
		_, _ = w.Write([]byte("PNGDATA"))
	}))
	defer srv.Close()
	c := New(srv.URL, "ik").WithImageToken(func() (string, error) { return "tok", nil })
	b, gotCT, err := c.FetchImage(context.Background(), id)
	if err != nil || string(b) != "PNGDATA" || gotCT != "image/png" || key != "ik" || auth != "Bearer tok" || uid != "" {
		t.Fatalf("fetch %q %q %v key=%q auth=%q uid=%q", b, gotCT, err, key, auth, uid)
	}
	ct = "image/svg+xml"
	if _, _, err := c.FetchImage(context.Background(), id); !errors.Is(err, ErrNotAnImage) {
		t.Fatalf("svg: %v", err)
	}
	ct, status = "image/png", 401
	if _, _, err := c.FetchImage(context.Background(), id); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("401: %v", err)
	}
	if _, _, err := c.FetchImage(context.Background(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("404: %v", err)
	}
}
