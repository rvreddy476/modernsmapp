// HTTP handler tests for /v1/dating/sparks. Requires TEST_PG_DSN; skipped
// otherwise.
package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/dating-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// stubMessageClient avoids hitting message-service during handler tests.
type stubMessageClient struct{}

func (s *stubMessageClient) CreateConversation(ctx context.Context, req service.CreateConversationRequest) (*service.CreateConversationResponse, error) {
	return &service.CreateConversationResponse{ConversationID: uuid.New().String()}, nil
}

func setupTestRouter(t *testing.T) (*gin.Engine, *store.Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping http handler tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	st := store.New(pool)
	svc := service.New(st, nil)
	svc.SetMessageClient(&stubMessageClient{})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(svc)
	h.RegisterRoutes(r)
	return r, st, func() { pool.Close() }
}

func mustSeedProfile(t *testing.T, st *store.Store, id uuid.UUID) {
	t.Helper()
	intent := "casual"
	if _, err := st.UpsertProfile(context.Background(), id, store.UpsertProfileParams{Intent: &intent}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestHandler_CreateSpark(t *testing.T) {
	r, st, cleanup := setupTestRouter(t)
	defer cleanup()
	from, to := uuid.New(), uuid.New()
	mustSeedProfile(t, st, from)
	mustSeedProfile(t, st, to)

	body, _ := json.Marshal(map[string]string{
		"to_user_id":  to.String(),
		"target_kind": "photo",
		"target_ref":  "0",
		"note":        "love",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/dating/sparks", bytes.NewReader(body))
	req.Header.Set("X-User-ID", from.String())
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestHandler_CreateSpark_RejectsMissingUser(t *testing.T) {
	r, _, cleanup := setupTestRouter(t)
	defer cleanup()
	req := httptest.NewRequest(http.MethodPost, "/v1/dating/sparks", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandler_ListIncoming(t *testing.T) {
	r, st, cleanup := setupTestRouter(t)
	defer cleanup()
	user := uuid.New()
	mustSeedProfile(t, st, user)

	req := httptest.NewRequest(http.MethodGet, "/v1/dating/sparks/incoming", nil)
	req.Header.Set("X-User-ID", user.String())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestHandler_RevokeSpark(t *testing.T) {
	r, st, cleanup := setupTestRouter(t)
	defer cleanup()
	from, to := uuid.New(), uuid.New()
	mustSeedProfile(t, st, from)
	mustSeedProfile(t, st, to)

	sp, err := st.CreateSpark(context.Background(), from, to, "photo", "0", "")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/v1/dating/sparks/"+sp.ID.String(), nil)
	req.Header.Set("X-User-ID", from.String())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
