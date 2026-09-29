package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// The wire contract for a standing refusal (Copyright Match plan 6.4):
// 403 AUTHOR_SUSPENDED with suspended_until, 503 STANDING_UNAVAILABLE.

func runStandingError(t *testing.T, err error) (int, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/posts", nil)
	if !writeStandingError(c, err) {
		return 0, nil
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("not an error envelope: %v (%s)", err, rec.Body.String())
	}
	details := env.Error.Details
	if details == nil {
		details = map[string]any{}
	}
	details["_code"] = env.Error.Code
	details["_message"] = env.Error.Message
	return rec.Code, details
}

func TestWriteStandingError_Suspended(t *testing.T) {
	until := time.Date(2026, 12, 27, 9, 30, 0, 0, time.FixedZone("IST", 5*3600+1800))
	err := fmt.Errorf("wrapped: %w", &service.AuthorSuspendedError{
		AuthorID: uuid.New(), Standing: "suspended", SuspendedUntil: &until, PolicyVersion: "standing-v1",
	})
	status, body := runStandingError(t, err)
	if status != http.StatusForbidden || body["_code"] != CodeAuthorSuspended {
		t.Fatalf("status=%d body=%v", status, body)
	}
	if body["suspended_until"] != "2026-12-27T04:00:00Z" {
		t.Fatalf("suspended_until must be RFC3339 UTC: %v", body["suspended_until"])
	}
	if body["standing"] != "suspended" || body["policy_version"] != "standing-v1" {
		t.Fatalf("details=%v", body)
	}
	if msg, _ := body["_message"].(string); msg == "" || len(msg) > 120 {
		t.Fatalf("message=%q", msg)
	}
}

func TestWriteStandingError_RestrictedHasNullUntil(t *testing.T) {
	status, body := runStandingError(t, &service.AuthorSuspendedError{Standing: "restricted"})
	if status != http.StatusForbidden || body["_code"] != CodeAuthorSuspended {
		t.Fatalf("status=%d body=%v", status, body)
	}
	if v, present := body["suspended_until"]; !present || v != nil {
		t.Fatalf("suspended_until must be present and null: %v", body)
	}
}

func TestWriteStandingError_Unknown(t *testing.T) {
	status, body := runStandingError(t, fmt.Errorf("x: %w", service.ErrStandingUnknown))
	if status != http.StatusServiceUnavailable || body["_code"] != CodeStandingUnavailable {
		t.Fatalf("status=%d body=%v", status, body)
	}
}

func TestWriteStandingError_LeavesOtherErrorsAlone(t *testing.T) {
	for _, err := range []error{errors.New("boom"), service.ErrPostNotFound, service.ErrInvalidThread, nil} {
		if status, _ := runStandingError(t, err); status != 0 {
			t.Fatalf("%v must not be claimed by writeStandingError", err)
		}
	}
}
