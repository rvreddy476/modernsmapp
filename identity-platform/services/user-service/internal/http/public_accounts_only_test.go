package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/identity-user-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Module 3 M3-P0-5 / SR-5 — launch is public-accounts-only.
//
// `account_visibility` accepted "private", stored it, and nothing read it.
// graph-service's follow path never consulted the setting, so the account
// stayed followable and its posts still reached the public feed and search.
// A privacy control that records a choice and changes no behaviour is a false
// promise, and people decide what to post based on it.

type visibilitySpyService struct {
	current *store.UserSettings
	// persisted records what actually reached the store, so a test can prove
	// a rejected value was never written rather than merely not echoed back.
	persisted *store.UserSettings
}

func (s *visibilitySpyService) GetUser(context.Context, uuid.UUID) (*store.User, error) {
	return &store.User{}, nil
}
func (s *visibilitySpyService) ListUsers(context.Context, int, int) ([]store.User, int, error) {
	return nil, 0, nil
}
func (s *visibilitySpyService) GetSettings(context.Context, uuid.UUID) (*store.UserSettings, error) {
	cp := *s.current
	return &cp, nil
}
func (s *visibilitySpyService) UpdateSettings(_ context.Context, in *store.UserSettings) (*store.UserSettings, error) {
	cp := *in
	s.persisted = &cp
	return in, nil
}

func visibilityRouter(t *testing.T) (*gin.Engine, *visibilitySpyService) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	spy := &visibilitySpyService{current: &store.UserSettings{AccountVisibility: "public"}}
	r := gin.New()
	h := New(spy, nil)
	h.RegisterRoutes(r, func(c *gin.Context) { c.Next() }, func(c *gin.Context) { c.Next() })
	return r, spy
}

func putSettings(t *testing.T, r *gin.Engine, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/v1/users/me/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", uuid.New().String())
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	return resp
}

func TestPrivateAccountIsRefusedAndNeverPersisted(t *testing.T) {
	for _, requested := range []string{"private", "followers", "PRIVATE", " private ", "friends_only"} {
		t.Run(requested, func(t *testing.T) {
			r, spy := visibilityRouter(t)
			resp := putSettings(t, r, `{"account_visibility":"`+requested+`"}`)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400. Accepting %q stores a privacy setting "+
					"nothing enforces: the account stays followable and its posts "+
					"still reach feed and search.", resp.Code, requested)
			}
			if !strings.Contains(resp.Body.String(), "UNSUPPORTED_ACCOUNT_VISIBILITY") {
				t.Errorf("the response does not tell the client the feature is absent: %s",
					resp.Body.String())
			}
			// The decisive check: nothing reached the store at all.
			if spy.persisted != nil {
				t.Fatalf("a rejected request still wrote to the store: %+v", spy.persisted)
			}
		})
	}
}

func TestPublicIsAcceptedAndOtherSettingsStillWork(t *testing.T) {
	r, spy := visibilityRouter(t)
	resp := putSettings(t, r, `{"account_visibility":"public","allow_messages_from":"connections"}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", resp.Code, resp.Body.String())
	}
	if spy.persisted == nil {
		t.Fatal("nothing was persisted")
	}
	if spy.persisted.AccountVisibility != "public" {
		t.Errorf("persisted visibility = %q, want public", spy.persisted.AccountVisibility)
	}
	if spy.persisted.AllowMessagesFrom != "connections" {
		t.Errorf("an unrelated privacy setting was lost: %+v", spy.persisted)
	}
}

// Omitting the field must not be treated as a request to change it — a partial
// update that touches only notification settings must still work.
func TestOmittingAccountVisibilityIsNotRejected(t *testing.T) {
	r, spy := visibilityRouter(t)
	resp := putSettings(t, r, `{"allow_comments_from":"connections"}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: a partial update that does not mention "+
			"account_visibility was refused: %s", resp.Code, resp.Body.String())
	}
	if spy.persisted == nil || spy.persisted.AllowCommentsFrom != "connections" {
		t.Fatalf("the unrelated setting was not applied: %+v", spy.persisted)
	}
}

func TestAccountVisibilityRejectedPredicate(t *testing.T) {
	for _, v := range []string{"private", "followers", "PRIVATE", "  private  ", "anything"} {
		if !AccountVisibilityRejected(v) {
			t.Errorf("%q should be rejected", v)
		}
	}
	for _, v := range []string{"", "   ", "public", "PUBLIC", " public "} {
		if AccountVisibilityRejected(v) {
			t.Errorf("%q should be accepted (empty means unchanged)", v)
		}
	}
}
