//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

// mediaInit is the slice of POST /v1/media/init the flows read
// (media-service internal/service/upload.go, InitUploadResponse).
type mediaInit struct {
	MediaID   string `json:"media_id"`
	UploadURL string `json:"upload_url"`
	// UploadHeaders are signed into upload_url: the object store refuses a
	// PUT that does not carry exactly these.
	UploadHeaders map[string]string `json:"upload_headers"`
}

// mediaStatus is the slice of the confirm answer and of
// GET /v1/media/:id/status the flows read.
type mediaStatus struct {
	ProcessingStatus string `json:"processing_status"`
	FileType         string `json:"file_type"`
	Width            *int   `json:"width"`
	Height           *int   `json:"height"`
}

// mediaInitAndPut reserves an upload slot and PUTs payload straight to the
// presigned URL, never through the app tier, exactly as a client does. It
// returns the media id for the confirm step.
func mediaInitAndPut(t *testing.T, ctx context.Context, uploader *HTTPClient, fileType, mimeType string, payload []byte) string {
	t.Helper()
	initRaw := uploader.MustOK(t, ctx, "POST", "/v1/media/init", map[string]any{
		"file_type":       fileType,
		"mime_type":       mimeType,
		"file_size_bytes": len(payload),
	})
	var init mediaInit
	if err := json.Unmarshal(initRaw, &init); err != nil || init.MediaID == "" || init.UploadURL == "" {
		t.Fatalf("init: media_id/upload_url missing (err=%v raw=%s)", err, string(initRaw))
	}

	putReq, err := http.NewRequestWithContext(ctx, http.MethodPut, init.UploadURL, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build presigned PUT: %v", err)
	}
	// Content-Length comes from the reader; it is signed too, and is the
	// declared file_size_bytes.
	putReq.Header.Set("Content-Type", mimeType)
	for k, v := range init.UploadHeaders {
		if http.CanonicalHeaderKey(k) == "Content-Length" {
			continue
		}
		putReq.Header.Set(k, v)
	}
	putResp, err := (&http.Client{Timeout: 15 * time.Second}).Do(putReq)
	if err != nil {
		t.Fatalf("PUT to presigned URL failed (object store reachable?): %v", err)
	}
	putResp.Body.Close()
	if putResp.StatusCode < 200 || putResp.StatusCode >= 300 {
		t.Fatalf("presigned PUT got %d, want 2xx", putResp.StatusCode)
	}
	return init.MediaID
}

// e2ePNG encodes a real size x size PNG: a gradient, so the encoder cannot
// collapse it to a few bytes and every decoder media-service runs over it
// (metadata strip, blurhash, thumbnails) has actual pixels to read.
func e2ePNG(t *testing.T, size int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 255 / size), G: uint8(y * 255 / size), B: 0x80, A: 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test PNG: %v", err)
	}
	return buf.Bytes()
}

// TestE2E_Media_UploadConfirm walks the direct-to-object-store upload path
// through the gateway with a real image:
//
//	init (presigned URL + media_id) → PUT the bytes to the store → confirm →
//	the asset is processed and its status reads "ready" for the uploader
//
// An image, because media-service processes one synchronously inside confirm
// (decode, strip metadata, renditions; internal/service/upload.go
// processImage), so "ready" proves the whole path: gateway → media-service →
// object store → confirm → processing → record. A video only queues a
// transcode at confirm; asserting a finished transcode needs a real sample
// and ffmpeg, which belongs in a media-specific job.
//
// This flow used to upload ~1 KB of text declared as video/mp4. Confirm
// refuses that now (the next flow pins it), so it could never pass.
func TestE2E_Media_UploadConfirm(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)

	uploader := NewHTTPClient(urls.APIGateway, uuid.New())
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	const side = 64
	mediaID := mediaInitAndPut(t, ctx, uploader, "image", "image/png", e2ePNG(t, side))

	// confirm processes an image before it answers. A processing failure is
	// a 200 carrying "failed", so the status is asserted, not just the 2xx.
	confirmRaw := uploader.MustOK(t, ctx, "POST", "/v1/media/confirm", map[string]any{"media_id": mediaID})
	var confirmed mediaStatus
	if err := json.Unmarshal(confirmRaw, &confirmed); err != nil || confirmed.ProcessingStatus != "ready" {
		t.Fatalf("confirm: processing_status %q, want \"ready\" (err=%v raw=%s)", confirmed.ProcessingStatus, err, string(confirmRaw))
	}

	// The record is readable by its uploader and says what was stored.
	statusRaw := uploader.MustOK(t, ctx, "GET", "/v1/media/"+mediaID+"/status", nil)
	var status mediaStatus
	if err := json.Unmarshal(statusRaw, &status); err != nil {
		t.Fatalf("status: decode: %v (raw=%s)", err, string(statusRaw))
	}
	if status.ProcessingStatus != "ready" || status.FileType != "image" {
		t.Errorf("status: processing_status=%q file_type=%q, want ready/image (raw=%s)", status.ProcessingStatus, status.FileType, string(statusRaw))
	}
	if status.Width == nil || status.Height == nil || *status.Width != side || *status.Height != side {
		t.Errorf("status: dimensions not the uploaded %dx%d (raw=%s)", side, side, string(statusRaw))
	}
}

// TestE2E_Media_ConfirmRefusesMistypedUpload pins the guard the old flow ran
// into: bytes that are not the declared type are refused at confirm with
// 409 UPLOAD_INTEGRITY_ERROR (media-service internal/service/upload.go
// uploadHeaderError, internal/http/handler.go confirmUploadError), and the
// asset never becomes usable.
func TestE2E_Media_ConfirmRefusesMistypedUpload(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)

	uploader := NewHTTPClient(urls.APIGateway, uuid.New())
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	// Plain text declared as an MP4.
	notAVideo := bytes.Repeat([]byte("atpost-e2e-media-"), 64)
	mediaID := mediaInitAndPut(t, ctx, uploader, "video", "video/mp4", notAVideo)

	env := uploader.MustDo(t, ctx, "POST", "/v1/media/confirm", map[string]any{"media_id": mediaID})
	code := ""
	if env.Error != nil {
		code = env.Error.Code
	}
	if env.Status != http.StatusConflict || code != "UPLOAD_INTEGRITY_ERROR" {
		t.Fatalf("confirm of a mistyped upload: got %d %q, want 409 UPLOAD_INTEGRITY_ERROR", env.Status, code)
	}

	// The refusal sticks: the uploader's own status read shows the asset
	// rejected, not waiting for a retry that could slip it through.
	statusRaw := uploader.MustOK(t, ctx, "GET", "/v1/media/"+mediaID+"/status", nil)
	var status mediaStatus
	if err := json.Unmarshal(statusRaw, &status); err != nil || status.ProcessingStatus != "rejected" {
		t.Errorf("status after refusal: processing_status %q, want \"rejected\" (err=%v raw=%s)", status.ProcessingStatus, err, string(statusRaw))
	}
}
