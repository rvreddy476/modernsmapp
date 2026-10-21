package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

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

// Past 10,000 parts of 64 MiB the resumable init refuses with 413.
func TestInitResumableUploadPastPartLimitIs413(t *testing.T) {
	r := uploadSizeRouter()
	size := service.MaxResumableUploadBytes + 1
	code, errCode, _ := postUploadInit(r, "/resumable/init",
		`{"file_type":"video","mime_type":"video/mp4","total_bytes":`+strconv.FormatInt(size, 10)+`}`)
	if code != http.StatusRequestEntityTooLarge || errCode != "FILE_TOO_LARGE" {
		t.Fatalf("got %d %s, want 413 FILE_TOO_LARGE", code, errCode)
	}
}
