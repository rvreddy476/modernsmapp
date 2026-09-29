// Appeal routes (Copyright Match plan section 6.3, P-3), no database: each
// service error answers with exactly one status and code, the author
// identity and admin scope gates hold, and a malformed decision_id never
// reaches the service. The state machine itself is in
// service/appeals_integration_test.go.
package http

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/atpost/trust-safety-service/internal/service"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

type fakeAppeals struct {
	submitErr, reviewErr error
	calls                int
	lastUser             uuid.UUID
	lastDecision         *uuid.UUID
	lastStatus, lastNote string
	lastMeta             postgres.AuditMeta
}

func (f *fakeAppeals) SubmitAppeal(_ context.Context, userID uuid.UUID, _, _, _ string, decisionID *uuid.UUID) (*postgres.ContentAppeal, error) {
	f.calls++
	f.lastUser, f.lastDecision = userID, decisionID
	if f.submitErr != nil {
		return nil, f.submitErr
	}
	return &postgres.ContentAppeal{ID: uuid.New(), UserID: userID, Status: postgres.AppealOpen}, nil
}

func (f *fakeAppeals) ReviewAppeal(_ context.Context, _ uuid.UUID, status, note string, meta postgres.AuditMeta) error {
	f.calls++
	f.lastStatus, f.lastNote, f.lastMeta = status, note, meta
	return f.reviewErr
}

func newAppealsRig(t *testing.T) (*fakeAppeals, func(method, path, body string, hdr map[string]string) (int, string)) {
	t.Helper()
	fake := &fakeAppeals{}
	h := New(nil)
	h.appeals = fake
	r := newTokenTestRouter(t, h)
	return fake, func(method, path, body string, hdr map[string]string) (int, string) {
		w := serveAdmin(r, method, path, body, hdr)
		return w.Code, adminErrorCode(w)
	}
}

func author(id uuid.UUID) map[string]string {
	return map[string]string{"X-Internal-Service-Key": tokenTestInternalKey, "X-User-Id": id.String()}
}

const appealBody = `{"content_type":"post","content_id":"11111111-1111-1111-1111-111111111111","appeal_reason":"mine"}`

func TestAppealSubmit_IdentityAndValidationBeforeTheService(t *testing.T) {
	fake, do := newAppealsRig(t)
	cases := []struct {
		name     string
		hdr      map[string]string
		body     string
		wantCode int
		wantErr  string
	}{
		{"no user id", map[string]string{"X-Internal-Service-Key": tokenTestInternalKey}, appealBody, http.StatusUnauthorized, "UNAUTHORIZED"},
		{"nil user id", author(uuid.Nil), appealBody, http.StatusUnauthorized, "UNAUTHORIZED"},
		{"not json", author(uuid.New()), `{`, http.StatusBadRequest, "BAD_REQUEST"},
		{"missing reason", author(uuid.New()), `{"content_type":"post","content_id":"x"}`, http.StatusBadRequest, "BAD_REQUEST"},
		{"bad decision id", author(uuid.New()), `{"content_type":"post","content_id":"x","appeal_reason":"r","decision_id":"nope"}`, http.StatusUnprocessableEntity, CodeAppealInvalid},
		{"nil decision id", author(uuid.New()), `{"content_type":"post","content_id":"x","appeal_reason":"r","decision_id":"00000000-0000-0000-0000-000000000000"}`, http.StatusUnprocessableEntity, CodeAppealInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, errCode := do(http.MethodPost, "/v1/appeals", tc.body, tc.hdr)
			if code != tc.wantCode || errCode != tc.wantErr {
				t.Fatalf("status=%d code=%q, want %d %s", code, errCode, tc.wantCode, tc.wantErr)
			}
		})
	}
	if fake.calls != 0 {
		t.Fatalf("service reached %d times before validation", fake.calls)
	}
	// Without the internal key the route is not reachable at all.
	if code, _ := do(http.MethodPost, "/v1/appeals", appealBody, map[string]string{"X-User-Id": uuid.NewString()}); code != http.StatusUnauthorized {
		t.Fatalf("no key: status=%d", code)
	}
}

func TestAppealSubmit_ServiceErrorsMapToOneCodeEach(t *testing.T) {
	fake, do := newAppealsRig(t)
	user, decision := uuid.New(), uuid.New()
	cases := []struct {
		err      error
		wantCode int
		wantErr  string
	}{
		{nil, http.StatusCreated, ""},
		{service.ErrAppealInvalid, http.StatusUnprocessableEntity, CodeAppealInvalid},
		{postgres.ErrActiveAppealExists, http.StatusConflict, CodeAppealActiveExists},
		{service.ErrAppealCopyrightCase, http.StatusConflict, CodeAppealCopyrightCase},
		{service.ErrAppealDecisionStale, http.StatusConflict, CodeAppealDecisionStale},
		{service.ErrAppealNotEligible, http.StatusConflict, CodeAppealNotAppealable},
		{service.ErrAppealsUnavailable, http.StatusServiceUnavailable, CodeAppealsUnavailable},
		{errors.New("boom"), http.StatusInternalServerError, "INTERNAL_ERROR"},
	}
	body := `{"content_type":"post","content_id":"` + uuid.NewString() + `","appeal_reason":"mine","decision_id":"` + decision.String() + `"}`
	for _, tc := range cases {
		fake.submitErr = tc.err
		code, errCode := do(http.MethodPost, "/v1/appeals", body, author(user))
		if code != tc.wantCode || errCode != tc.wantErr {
			t.Fatalf("%v: status=%d code=%q, want %d %s", tc.err, code, errCode, tc.wantCode, tc.wantErr)
		}
		if fake.lastUser != user || fake.lastDecision == nil || *fake.lastDecision != decision {
			t.Fatalf("service saw user=%s decision=%v", fake.lastUser, fake.lastDecision)
		}
	}
}

func TestAppealReview_GatesBeforeTheService(t *testing.T) {
	fake, do := newAppealsRig(t)
	path := "/v1/appeals/" + uuid.NewString()
	cases := []struct {
		name     string
		hdr      map[string]string
		path     string
		body     string
		wantCode int
		wantErr  string
	}{
		{"no admin scope", gatewayAdmin(uuid.New(), "user moderator"), path, `{"status":"upheld"}`, http.StatusForbidden, "FORBIDDEN"},
		{"admin scope, no verified actor", map[string]string{"X-Internal-Service-Key": tokenTestInternalKey, "X-Scopes": "admin"}, path, `{"status":"upheld"}`, http.StatusBadRequest, codeActorRequired},
		{"bad appeal id", gatewayAdmin(uuid.New(), "admin"), "/v1/appeals/nope", `{"status":"upheld"}`, http.StatusBadRequest, "BAD_REQUEST"},
		{"missing status", gatewayAdmin(uuid.New(), "admin"), path, `{"note":"x"}`, http.StatusBadRequest, "BAD_REQUEST"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, errCode := do(http.MethodPatch, tc.path, tc.body, tc.hdr)
			if code != tc.wantCode || errCode != tc.wantErr {
				t.Fatalf("status=%d code=%q, want %d %s", code, errCode, tc.wantCode, tc.wantErr)
			}
		})
	}
	if fake.calls != 0 {
		t.Fatalf("service reached %d times before the gates", fake.calls)
	}
}

func TestAppealReview_ServiceErrorsMapToOneCodeEach(t *testing.T) {
	fake, do := newAppealsRig(t)
	admin := uuid.New()
	path := "/v1/appeals/" + uuid.NewString()
	cases := []struct {
		err      error
		wantCode int
		wantErr  string
	}{
		{nil, http.StatusOK, ""},
		{service.ErrAppealInvalid, http.StatusUnprocessableEntity, CodeAppealInvalid},
		{service.ErrActorRequired, http.StatusBadRequest, codeActorRequired},
		{service.ErrAppealNotFound, http.StatusNotFound, CodeAppealNotFound},
		{service.ErrAppealNotEligible, http.StatusConflict, CodeAppealNotAppealable},
		{service.ErrAppealSuperseded, http.StatusConflict, CodeAppealSuperseded},
		{service.ErrAppealSubjectChanged, http.StatusConflict, CodeAppealSubjectChanged},
		{service.ErrPostDecisionConflict, http.StatusConflict, CodeAppealDecisionConflict},
		{service.ErrAppealTransition, http.StatusConflict, CodeAppealTransition},
		{service.ErrAppealOverturnInFlight, http.StatusConflict, CodeAppealOverturnInFlight},
		{service.ErrAppealOverturnPending, http.StatusServiceUnavailable, CodeAppealOverturnPending},
		{service.ErrAppealsUnavailable, http.StatusServiceUnavailable, CodeAppealsUnavailable},
		{errors.New("boom"), http.StatusInternalServerError, "INTERNAL_ERROR"},
	}
	for _, tc := range cases {
		fake.reviewErr = tc.err
		code, errCode := do(http.MethodPatch, path, `{"status":"overturned","note":"fine"}`, gatewayAdmin(admin, "admin"))
		if code != tc.wantCode || errCode != tc.wantErr {
			t.Fatalf("%v: status=%d code=%q, want %d %s", tc.err, code, errCode, tc.wantCode, tc.wantErr)
		}
		if fake.lastStatus != "overturned" || fake.lastNote != "fine" || fake.lastMeta.Actor.UserID != admin || fake.lastMeta.Reason != "fine" {
			t.Fatalf("service saw status=%q note=%q meta=%+v", fake.lastStatus, fake.lastNote, fake.lastMeta)
		}
	}
	// Wrapped errors keep their mapping.
	fake.reviewErr = errors.Join(errors.New("ctx"), service.ErrAppealTransition)
	if code, errCode := do(http.MethodPatch, path, `{"status":"upheld"}`, gatewayAdmin(admin, "admin")); code != http.StatusConflict || errCode != CodeAppealTransition {
		t.Fatalf("wrapped transition: status=%d code=%q", code, errCode)
	}
}
