//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestE2E_Media_UploadConfirm walks the direct-to-object-store upload path
// through the gateway:
//
//	init (presigned URL + media_id) → PUT bytes to the store → confirm →
//	media record becomes queryable
//
// It does NOT assert a finished transcode (we upload a tiny non-video blob, so
// the worker can't produce renditions) — it validates the upload wiring
// (gateway → media-service → object store → confirm), which is the part that
// breaks on misconfiguration. A real-sample transcode assertion belongs in a
// nightly media-specific job.
func TestE2E_Media_UploadConfirm(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)

	uploader := NewHTTPClient(urls.APIGateway, uuid.New())
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	payload := bytes.Repeat([]byte("atpost-e2e-media-"), 64) // ~1KB dummy blob

	// 1. init — reserve an upload slot + presigned PUT URL.
	initRaw := uploader.MustOK(t, ctx, "POST", "/v1/media/init", map[string]any{
		"file_type":       "video",
		"mime_type":       "video/mp4",
		"file_size_bytes": len(payload),
	})
	var init struct {
		MediaID   string `json:"media_id"`
		UploadURL string `json:"upload_url"`
	}
	if err := json.Unmarshal(initRaw, &init); err != nil || init.MediaID == "" || init.UploadURL == "" {
		t.Fatalf("init: media_id/upload_url missing (err=%v raw=%s)", err, string(initRaw))
	}

	// 2. PUT the bytes straight to the (MinIO/S3) presigned URL — never through
	// the app tier, exactly like the real client.
	putReq, _ := http.NewRequestWithContext(ctx, http.MethodPut, init.UploadURL, bytes.NewReader(payload))
	putReq.Header.Set("Content-Type", "video/mp4")
	putResp, err := (&http.Client{Timeout: 15 * time.Second}).Do(putReq)
	if err != nil {
		t.Fatalf("PUT to presigned URL failed (object store reachable?): %v", err)
	}
	putResp.Body.Close()
	if putResp.StatusCode < 200 || putResp.StatusCode >= 300 {
		t.Fatalf("presigned PUT got %d, want 2xx", putResp.StatusCode)
	}

	// 3. confirm — promote the uploaded object to a media record.
	uploader.MustOK(t, ctx, "POST", "/v1/media/confirm", map[string]any{"media_id": init.MediaID})

	// 4. the media record is now queryable via the gateway.
	Eventually(t, 20*time.Second, "media record queryable after confirm", func() bool {
		env, derr := uploader.Do(ctx, "GET", "/v1/media/"+init.MediaID+"/status", nil)
		return derr == nil && env.Status == 200
	})
}
