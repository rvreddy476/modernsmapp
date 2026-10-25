package ownedmedia

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPrivateIntroduction(t *testing.T) {
	owner, id := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
		valid  bool
	}{
		{"one minute", func(map[string]any) {}, true},
		{"short", func(m map[string]any) { m["duration_ms"] = 1 }, true},
		{"too long", func(m map[string]any) { m["duration_ms"] = 60001 }, false},
		{"unknown duration", func(m map[string]any) { delete(m, "duration_ms") }, false},
		{"zero duration", func(m map[string]any) { m["duration_ms"] = 0 }, false},
		{"too large", func(m map[string]any) { m["file_size_bytes"] = (64 << 20) + 1 }, false},
		{"unknown size", func(m map[string]any) { delete(m, "file_size_bytes") }, false},
		{"foreign owner", func(m map[string]any) { m["uploader_id"] = uuid.New() }, false},
		{"wrong id", func(m map[string]any) { m["id"] = uuid.New() }, false},
		{"image", func(m map[string]any) { m["file_type"] = "image" }, false},
		{"processing", func(m map[string]any) { m["processing_status"] = "processing" }, false},
		{"pending moderation", func(m map[string]any) { m["moderation_status"] = "pending" }, false},
		{"public", func(m map[string]any) { m["storage_key"] = "public/video.mp4" }, false},
		{"composer lease", func(m map[string]any) { m["upload_purpose"] = "composer" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Internal-Service-Key") != "test" || r.Header.Get("X-User-Id") != "" {
					t.Error("unexpected identity")
				}
				data := map[string]any{"id": id, "uploader_id": owner, "file_type": "video", "processing_status": "ready", "moderation_status": "passed", "storage_key": "protected/video.mp4", "duration_ms": 60000, "file_size_bytes": 64 << 20}
				tc.change(data)
				if err := json.NewEncoder(w).Encode(map[string]any{"data": data}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			err := VerifyIntroduction(context.Background(), New(server.URL, "test"), owner, id)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
}

func TestIntroductionFailsClosedWithoutVideoVerifier(t *testing.T) {
	imageOnly := Func(func(context.Context, uuid.UUID, uuid.UUID) error { return nil })
	if !errors.Is(VerifyIntroduction(context.Background(), imageOnly, uuid.New(), uuid.New()), ErrUnavailable) {
		t.Fatal("image verifier accepted video")
	}
}
