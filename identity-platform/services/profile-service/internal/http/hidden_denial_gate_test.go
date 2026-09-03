package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Account lifecycle (auth-service 30-day deactivate / scheduled-deletion):
// GetProfile, GetProfileByUsername and the batch lookup must treat a hidden
// account exactly like a missing one for every viewer except the account's
// own owner. See hidden_denial_gate.go.

var hiddenTargetID = uuid.MustParse("cccccccc-3333-4333-8333-cccccccccccc")

// hiddenGateService serves a fixed profile for hiddenTargetID (or, on the
// batch route, one profile per requested id) and answers IsHidden from a
// caller-supplied set — the fake-store pattern used across this package's
// other handler tests (see dtoProfileService in public_profile_dto_test.go).
type hiddenGateService struct {
	stubProfileService
	hidden map[uuid.UUID]bool
}

func (s *hiddenGateService) GetProfile(_ context.Context, id uuid.UUID) (*store.Profile, error) {
	return &store.Profile{UserID: id, Username: str("someone"), DisplayName: "Someone"}, nil
}
func (s *hiddenGateService) GetProfileByUsername(_ context.Context, username string) (*store.Profile, error) {
	return &store.Profile{UserID: hiddenTargetID, Username: &username, DisplayName: "Someone"}, nil
}
func (s *hiddenGateService) GetProfilesBatch(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]*store.Profile, error) {
	out := map[uuid.UUID]*store.Profile{}
	for _, id := range ids {
		out[id] = &store.Profile{UserID: id, Username: str("someone"), DisplayName: "Someone"}
	}
	return out, nil
}
func (s *hiddenGateService) IsHidden(_ context.Context, id uuid.UUID) (bool, error) {
	return s.hidden[id], nil
}

func hiddenRouter(t *testing.T, hidden map[uuid.UUID]bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(&hiddenGateService{hidden: hidden}, nil)
	// A permissive block checker isolates these tests to the hidden gate:
	// without one, deniedByBlock fails closed (blocks == nil) for any
	// authenticated non-self viewer and every case below would 404 for that
	// reason instead of the one under test.
	h.WithBlockChecker(staticBlockChecker{})
	h.RegisterRoutes(r, func(c *gin.Context) { c.Next() }, func(c *gin.Context) { c.Next() })
	return r
}

func TestGetProfileHiddenDeniedForOtherViewer(t *testing.T) {
	r := hiddenRouter(t, map[uuid.UUID]bool{hiddenTargetID: true})
	req := httptest.NewRequest(http.MethodGet, "/v1/profiles/"+hiddenTargetID.String(), nil)
	req.Header.Set("X-User-Id", uuid.New().String())
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a hidden profile, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestGetProfileHiddenDeniedForAnonymousViewer(t *testing.T) {
	// Hidden state is a property of the TARGET, not relative to who is
	// asking (unlike blocks) — an anonymous caller must be denied too.
	r := hiddenRouter(t, map[uuid.UUID]bool{hiddenTargetID: true})
	req := httptest.NewRequest(http.MethodGet, "/v1/profiles/"+hiddenTargetID.String(), nil)
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a hidden profile viewed anonymously, got %d", resp.Code)
	}
}

func TestGetProfileHiddenAllowedForSelf(t *testing.T) {
	r := hiddenRouter(t, map[uuid.UUID]bool{hiddenTargetID: true})
	req := httptest.NewRequest(http.MethodGet, "/v1/profiles/"+hiddenTargetID.String(), nil)
	req.Header.Set("X-User-Id", hiddenTargetID.String()) // viewer IS the hidden user
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("self-view of a hidden profile must always succeed, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestGetProfileNotHiddenVisibleToEveryone(t *testing.T) {
	r := hiddenRouter(t, map[uuid.UUID]bool{}) // unhidden
	req := httptest.NewRequest(http.MethodGet, "/v1/profiles/"+hiddenTargetID.String(), nil)
	req.Header.Set("X-User-Id", uuid.New().String())
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("unhide must restore visibility for other viewers, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestGetProfileByUsernameHiddenDeniedForOtherViewer(t *testing.T) {
	r := hiddenRouter(t, map[uuid.UUID]bool{hiddenTargetID: true})
	req := httptest.NewRequest(http.MethodGet, "/v1/profiles/by-username/someone", nil)
	req.Header.Set("X-User-Id", uuid.New().String())
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a hidden profile by username, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestGetProfileByUsernameHiddenAllowedForSelf(t *testing.T) {
	r := hiddenRouter(t, map[uuid.UUID]bool{hiddenTargetID: true})
	req := httptest.NewRequest(http.MethodGet, "/v1/profiles/by-username/someone", nil)
	req.Header.Set("X-User-Id", hiddenTargetID.String())
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("self-view by username of a hidden profile must always succeed, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestGetProfilesBatchOmitsHiddenExceptSelf(t *testing.T) {
	visibleID := uuid.New()
	selfID := hiddenTargetID // the caller IS the hidden user in this batch
	r := hiddenRouter(t, map[uuid.UUID]bool{hiddenTargetID: true})

	body, _ := json.Marshal(map[string][]string{
		"user_ids": {hiddenTargetID.String(), visibleID.String(), selfID.String()},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/profiles/batch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", selfID.String())
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, present := out[hiddenTargetID.String()]; !present {
		t.Fatal("the caller's own (hidden) entry must be present, not omitted")
	}
	if _, present := out[visibleID.String()]; !present {
		t.Fatal("a non-hidden entry must be present")
	}
}

func TestGetProfilesBatchOmitsHiddenForOtherViewer(t *testing.T) {
	visibleID := uuid.New()
	r := hiddenRouter(t, map[uuid.UUID]bool{hiddenTargetID: true})

	body, _ := json.Marshal(map[string][]string{
		"user_ids": {hiddenTargetID.String(), visibleID.String()},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/profiles/batch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", uuid.New().String()) // unrelated viewer
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, present := out[hiddenTargetID.String()]; present {
		t.Fatal("a hidden entry must be omitted for an unrelated viewer")
	}
	if _, present := out[visibleID.String()]; !present {
		t.Fatal("a non-hidden entry must remain present")
	}
}
