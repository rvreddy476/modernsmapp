package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/service"
	"github.com/gin-gonic/gin"
)

func uploadSizeRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := &Handler{svc: service.New(nil, nil)}
	r := gin.New()
	r.POST("/init", h.InitUpload)
	r.POST("/resumable/init", h.InitResumableUpload)
	return r
}

func postUploadInit(r http.Handler, path, body string) (int, string, map[string]any) {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "11111111-1111-1111-1111-111111111111")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	return rec.Code, env.Error.Code, env.Error.Details
}

// A video past 5 GiB on the simple init is 413 USE_RESUMABLE_UPLOAD, so a
// client knows to switch paths rather than give up. The size check runs
// before anything touches the stores.
func TestInitUploadOverFiveGiBSaysUseResumable(t *testing.T) {
	r := uploadSizeRouter()
	size := service.MaxSimpleVideoUploadBytes + 1
	code, errCode, details := postUploadInit(r, "/init",
		`{"file_type":"video","mime_type":"video/mp4","file_size_bytes":`+strconv.FormatInt(size, 10)+`}`)
	if code != http.StatusRequestEntityTooLarge || errCode != "USE_RESUMABLE_UPLOAD" {
		t.Fatalf("got %d %s, want 413 USE_RESUMABLE_UPLOAD", code, errCode)
	}
	if details["resumable_init"] != "/v1/media/upload/resumable/init" {
		t.Fatalf("details = %v", details)
	}
	// A non-video over the absolute cap is still FILE_TOO_LARGE.
	code, errCode, _ = postUploadInit(r, "/init",
		`{"file_type":"image","mime_type":"image/jpeg","file_size_bytes":`+strconv.FormatInt(service.MaxUploadSizeBytes+1, 10)+`}`)
	if code != http.StatusRequestEntityTooLarge || errCode != "FILE_TOO_LARGE" {
		t.Fatalf("image: got %d %s, want 413 FILE_TOO_LARGE", code, errCode)
	}
}

// A video over the worker's cap is refused at init on BOTH routes with 413
// VIDEO_TOO_LARGE and the cap, before a byte is uploaded (2026-11-07: the
// resumable route took 625 GiB and the worker refused everything over
// 16 GiB after the upload). Past 10,000 parts of 64 MiB is far beyond it.
func TestInitUploadOverVideoCapIsVideoTooLarge(t *testing.T) {
	r := uploadSizeRouter()
	limit := processing.MaxVideoOriginalBytes
	if service.VideoOriginalCap() != limit {
		t.Fatalf("upload cap %d is not the worker's %d", service.VideoOriginalCap(), limit)
	}
	for _, size := range []int64{limit + 1, service.MaxResumableUploadBytes + 1} {
		code, errCode, details := postUploadInit(r, "/resumable/init",
			`{"file_type":"video","mime_type":"video/mp4","total_bytes":`+strconv.FormatInt(size, 10)+`}`)
		if code != http.StatusRequestEntityTooLarge || errCode != "VIDEO_TOO_LARGE" {
			t.Fatalf("resumable %d bytes: got %d %s, want 413 VIDEO_TOO_LARGE", size, code, errCode)
		}
		if details["max_bytes"] != float64(limit) {
			t.Fatalf("resumable details = %v, want max_bytes %d", details, limit)
		}
		// The simple route says the same, not "use the resumable upload":
		// that route would refuse it too.
		code, errCode, details = postUploadInit(r, "/init",
			`{"file_type":"video","mime_type":"video/mp4","file_size_bytes":`+strconv.FormatInt(size, 10)+`}`)
		if code != http.StatusRequestEntityTooLarge || errCode != "VIDEO_TOO_LARGE" || details["max_bytes"] != float64(limit) {
			t.Fatalf("simple %d bytes: got %d %s %v, want 413 VIDEO_TOO_LARGE", size, code, errCode, details)
		}
	}
}
