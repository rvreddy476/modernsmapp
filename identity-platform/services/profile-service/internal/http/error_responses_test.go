package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/identity-profile-service/internal/service"
	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// driverErr is the shape of a real pgx failure wrapped by the service: table,
// constraint and SQLSTATE text that must never reach a client.
var driverErr = fmt.Errorf("failed to create block: %w",
	errors.New(`ERROR: duplicate key value violates unique constraint "blocks_pkey" (SQLSTATE 23505)`))

var leakMarkers = []string{"SQLSTATE", "duplicate key", "blocks_pkey", "failed to", "ERROR:", "Key: '", "Field validation"}

// errorStubService fails the follow, block, handle and about writes with err
// and resolves every target identifier to a fixed profile.
type errorStubService struct {
	stubProfileService
	err error
}

func (s *errorStubService) GetProfileByUsername(context.Context, string) (*store.Profile, error) {
	return &store.Profile{UserID: uuid.New()}, nil
}
func (s *errorStubService) FollowUser(context.Context, uuid.UUID, uuid.UUID) (*store.Follow, error) {
	return nil, s.err
}
func (s *errorStubService) BlockUser(context.Context, uuid.UUID, uuid.UUID) error { return s.err }
func (s *errorStubService) ChangeHandle(context.Context, uuid.UUID, string) (*store.Profile, error) {
	return nil, s.err
}

type errorBody struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

func runHandler(t *testing.T, err error, body string, params gin.Params, call func(*Handler, *gin.Context)) (*httptest.ResponseRecorder, errorBody) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := &Handler{svc: &errorStubService{err: err}, log: slog.Default()}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("X-User-Id", uuid.NewString())
	c.Params = params
	call(h, c)
	var out errorBody
	if decodeErr := json.Unmarshal(w.Body.Bytes(), &out); decodeErr != nil {
		t.Fatalf("body is not the error envelope: %v: %s", decodeErr, w.Body.String())
	}
	return w, out
}

func assertNoLeak(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	for _, leak := range leakMarkers {
		if strings.Contains(w.Body.String(), leak) {
			t.Fatalf("body leaks %q: %s", leak, w.Body.String())
		}
	}
}

func assertInternal(t *testing.T, w *httptest.ResponseRecorder, out errorBody) {
	t.Helper()
	if w.Code != http.StatusInternalServerError || out.Error.Code != "INTERNAL_ERROR" {
		t.Fatalf("got %d %s, want 500 INTERNAL_ERROR", w.Code, w.Body.String())
	}
	assertNoLeak(t, w)
}

var userParam = gin.Params{{Key: "username", Value: "someone"}}

func TestFollowUserSelfIsFixed400(t *testing.T) {
	w, out := runHandler(t, service.ErrCannotFollowSelf, ``, userParam, (*Handler).FollowUser)
	if w.Code != http.StatusBadRequest || out.Error.Code != "FOLLOW_FAILED" || out.Error.Message != "You cannot follow yourself" {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
}

func TestFollowUserBlockedIsFixed400(t *testing.T) {
	w, out := runHandler(t, service.ErrCannotFollowUser, ``, userParam, (*Handler).FollowUser)
	if w.Code != http.StatusBadRequest || out.Error.Code != "FOLLOW_FAILED" || out.Error.Message != "You cannot follow this user" {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
}

func TestFollowUserStoreErrorIs500WithoutDetail(t *testing.T) {
	w, out := runHandler(t, driverErr, ``, userParam, (*Handler).FollowUser)
	assertInternal(t, w, out)
}

func TestBlockUserSelfIsFixed400(t *testing.T) {
	w, out := runHandler(t, service.ErrCannotBlockSelf, ``, userParam, (*Handler).BlockUser)
	if w.Code != http.StatusBadRequest || out.Error.Code != "BLOCK_FAILED" || out.Error.Message != "You cannot block yourself" {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
}

func TestBlockUserStoreErrorIs500WithoutDetail(t *testing.T) {
	w, out := runHandler(t, driverErr, ``, userParam, (*Handler).BlockUser)
	assertInternal(t, w, out)
}

func TestChangeHandleTakenIsFixed400WithReason(t *testing.T) {
	w, out := runHandler(t, service.ErrUsernameTaken, `{"username":"ada"}`, nil, (*Handler).ChangeHandle)
	if w.Code != http.StatusBadRequest || out.Error.Code != "BAD_REQUEST" || out.Error.Details["reason"] != "USERNAME_TAKEN" {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
}

func TestChangeHandleCooldownCarriesUntil(t *testing.T) {
	until := time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC)
	w, out := runHandler(t, &service.HandleCooldownError{Until: until}, `{"username":"ada"}`, nil, (*Handler).ChangeHandle)
	if w.Code != http.StatusBadRequest || out.Error.Details["reason"] != "HANDLE_COOLDOWN" ||
		out.Error.Details["cooldown_until"] != "2026-12-01T10:00:00Z" {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(out.Error.Message, "cooldown until") {
		t.Fatalf("message carries the internal error text: %s", out.Error.Message)
	}
}

func TestChangeHandleStoreErrorIs500WithoutDetail(t *testing.T) {
	err := fmt.Errorf("failed to update username: %w", driverErr)
	w, out := runHandler(t, err, `{"username":"ada"}`, nil, (*Handler).ChangeHandle)
	assertInternal(t, w, out)
}

func TestBindErrorIsFixed400WithoutBinderText(t *testing.T) {
	// media_id is required: gin's binder answers with Go field and tag names.
	w, out := runHandler(t, nil, `{}`, nil, (*Handler).UpdateMyAvatar)
	if w.Code != http.StatusBadRequest || out.Error.Code != "INVALID_REQUEST" || out.Error.Message != msgInvalidBody {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	assertNoLeak(t, w)
	if strings.Contains(w.Body.String(), "MediaID") {
		t.Fatalf("body names the struct field: %s", w.Body.String())
	}
}

func TestAboutItemValidationIsFixed400(t *testing.T) {
	params := gin.Params{{Key: "section", Value: "not-a-section"}}
	w, out := runHandler(t, nil, `{"data":{"title":"x"}}`, params, (*Handler).UpsertMyAboutItem)
	if w.Code != http.StatusBadRequest || out.Error.Code != "INVALID_ABOUT_ITEM" || out.Error.Message != errAboutSection.Error() {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
}

func TestProfileLinkValidationIsFixed400(t *testing.T) {
	w, out := runHandler(t, nil, `{"title":"Site","url":"javascript:alert(1)"}`, nil, (*Handler).CreateMyProfileLink)
	if w.Code != http.StatusBadRequest || out.Error.Code != "INVALID_PROFILE_LINK" || out.Error.Message != errLinkURL.Error() {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
}

func TestUnknownValidationErrorIs500WithoutDetail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{log: slog.Default()}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	h.writeValidationError(c, "INVALID_PROFILE_LINK", driverErr)
	var out errorBody
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	assertInternal(t, w, out)
}
