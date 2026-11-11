package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atpost/identity-profile-service/internal/personverify"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// stubPerson records calls and answers fixed results.
type stubPerson struct {
	err        error
	gotState   string
	gotCode    string
	gotMedia   uuid.UUID
	gotUser    uuid.UUID
	startCalls int
}

func (s *stubPerson) Start(_ context.Context, u uuid.UUID) (*personverify.StartResult, error) {
	s.startCalls++
	s.gotUser = u
	if s.err != nil {
		return nil, s.err
	}
	return &personverify.StartResult{AttemptID: uuid.New(), AuthorizeURL: "https://digilocker.example/authorize", ExpiresAt: time.Now()}, nil
}

func (s *stubPerson) Callback(_ context.Context, u uuid.UUID, state, code string) (*personverify.Status, error) {
	s.gotUser, s.gotState, s.gotCode = u, state, code
	if s.err != nil {
		return nil, s.err
	}
	return &personverify.Status{Next: "selfie"}, nil
}

func (s *stubPerson) SubmitSelfie(_ context.Context, u, media uuid.UUID) (*personverify.Status, error) {
	s.gotUser, s.gotMedia = u, media
	if s.err != nil {
		return nil, s.err
	}
	return &personverify.Status{IsVerified: true, VerificationLevel: "person", BadgeFlags: 1}, nil
}

func (s *stubPerson) Status(_ context.Context, u uuid.UUID) (*personverify.Status, error) {
	s.gotUser = u
	if s.err != nil {
		return nil, s.err
	}
	return &personverify.Status{Next: "digilocker"}, nil
}

func personRouter(p PersonVerification) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(&stubProfileService{}, nil)
	if p != nil {
		h.WithPersonVerification(p)
	}
	pass := func(c *gin.Context) { c.Next() }
	h.RegisterRoutes(r, pass, pass)
	return r
}

func personCall(r *gin.Engine, method, path, user string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if user != "" {
		req.Header.Set("X-User-Id", user)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

var personPaths = []struct{ method, path string }{
	{http.MethodGet, "/v1/profiles/me/verification/person"},
	{http.MethodPost, "/v1/profiles/me/verification/person/start"},
	{http.MethodPost, "/v1/profiles/me/verification/person/callback"},
	{http.MethodPost, "/v1/profiles/me/verification/person/selfie"},
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	return env.Error.Code
}

func TestPersonVerification_SwitchOffAnswers503OnEveryRoute(t *testing.T) {
	user := uuid.NewString()
	disabled := personverify.New(personverify.Config{}, nil, nil, nil, nil)
	for name, r := range map[string]*gin.Engine{"unwired": personRouter(nil), "disabled": personRouter(disabled)} {
		for _, p := range personPaths {
			w := personCall(r, p.method, p.path, user, map[string]string{
				"state": "s", "code": "c", "selfie_media_id": uuid.NewString(),
			})
			if w.Code != http.StatusServiceUnavailable || errorCode(t, w) != "PERSON_VERIFICATION_DISABLED" {
				t.Errorf("%s %s %s = %d %s, want 503 PERSON_VERIFICATION_DISABLED", name, p.method, p.path, w.Code, w.Body.String())
			}
		}
	}
}

func TestPersonVerification_RoutesPassTheSignedInUserAndBody(t *testing.T) {
	stub := &stubPerson{}
	r := personRouter(stub)
	user := uuid.New()

	if w := personCall(r, http.MethodPost, "/v1/profiles/me/verification/person/start", user.String(), nil); w.Code != http.StatusCreated || stub.gotUser != user {
		t.Fatalf("start = %d", w.Code)
	}
	if w := personCall(r, http.MethodPost, "/v1/profiles/me/verification/person/callback", user.String(),
		map[string]string{"state": "st", "code": "cd"}); w.Code != http.StatusOK || stub.gotState != "st" || stub.gotCode != "cd" {
		t.Fatalf("callback = %d", w.Code)
	}
	media := uuid.New()
	if w := personCall(r, http.MethodPost, "/v1/profiles/me/verification/person/selfie", user.String(),
		map[string]string{"selfie_media_id": media.String()}); w.Code != http.StatusOK || stub.gotMedia != media {
		t.Fatalf("selfie = %d", w.Code)
	}
	if w := personCall(r, http.MethodPost, "/v1/profiles/me/verification/person/selfie", user.String(),
		map[string]string{"selfie_media_id": "nope"}); w.Code != http.StatusBadRequest {
		t.Fatalf("bad selfie id = %d", w.Code)
	}
	if w := personCall(r, http.MethodGet, "/v1/profiles/me/verification/person", user.String(), nil); w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	// No signed-in user: 401, the service is never called.
	before := stub.startCalls
	if w := personCall(r, http.MethodPost, "/v1/profiles/me/verification/person/start", "", nil); w.Code != http.StatusUnauthorized || stub.startCalls != before {
		t.Fatalf("anonymous start = %d", w.Code)
	}
}

func TestPersonVerification_RefusalsKeepTheirStatusAndCode(t *testing.T) {
	stub := &stubPerson{err: &personverify.Error{Status: http.StatusConflict, Code: "DUPLICATE_PERSON", Message: "x"}}
	r := personRouter(stub)
	w := personCall(r, http.MethodPost, "/v1/profiles/me/verification/person/callback", uuid.NewString(), map[string]string{"state": "s", "code": "c"})
	if w.Code != http.StatusConflict || errorCode(t, w) != "DUPLICATE_PERSON" {
		t.Fatalf("refusal = %d %s", w.Code, w.Body.String())
	}
	stub.err = context.DeadlineExceeded
	w = personCall(r, http.MethodPost, "/v1/profiles/me/verification/person/start", uuid.NewString(), nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("internal error = %d", w.Code)
	}
}
