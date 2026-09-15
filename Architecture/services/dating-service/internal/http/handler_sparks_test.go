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
	"strings"
	"testing"
	"time"

	"github.com/atpost/dating-service/database"
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
	if strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := database.BootstrapSchema(ctx, pool); err != nil {
			t.Fatalf("bootstrap schema: %v", err)
		}
	}
	st := store.New(pool)
	st.SetPII(testPII(t)) // lane D9
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

// mustSeedActiveProfile builds a valid active 18+ profile through the lane
// D2 status machine: basics (identity-sourced birth date and first name,
// interested_in), an approved primary photo and a passed selfie, then the
// three onboarding transitions.
func mustSeedActiveProfile(t *testing.T, st *store.Store, id uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	intent, gender, city, interested := "casual", "female", "Hyderabad", "male"
	if _, err := st.UpsertProfile(ctx, id, store.UpsertProfileParams{Intent: &intent, Gender: &gender, City: &city}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	if _, err := st.SetProfileBirthDate(ctx, id, time.Date(1995, 1, 1, 0, 0, 0, 0, time.UTC), store.BasicsSourceIdentity); err != nil {
		t.Fatalf("seed birth date: %v", err)
	}
	if _, err := st.SetProfileFirstName(ctx, id, "Asha", store.BasicsSourceIdentity); err != nil {
		t.Fatalf("seed first name: %v", err)
	}
	if _, err := st.UpsertPreferences(ctx, id, store.UpsertPreferencesParams{InterestedInGender: &interested}); err != nil {
		t.Fatalf("seed preferences: %v", err)
	}
	photo, err := st.CreatePhoto(ctx, id, store.CreatePhotoParams{MediaID: uuid.New(), IsPrimary: true, Visibility: "public"})
	if err != nil {
		t.Fatalf("seed photo: %v", err)
	}
	if _, err := st.SetPhotoModerationStatus(ctx, photo.ID, "approved", ""); err != nil {
		t.Fatalf("approve photo: %v", err)
	}
	if err := st.RecordSelfieAttempt(ctx, id, 0.99, "passed"); err != nil {
		t.Fatalf("seed selfie: %v", err)
	}
	for _, ev := range []store.ProfileEvent{store.ProfileEventBasicsComplete, store.ProfileEventPhotoApproved, store.ProfileEventSelfiePassed} {
		if _, err := st.TransitionProfileStatus(ctx, id, ev, store.ProfileActorSystem); err != nil {
			t.Fatalf("seed transition %s: %v", ev, err)
		}
	}
}

func TestHandler_CreateSpark(t *testing.T) {
	r, st, cleanup := setupTestRouter(t)
	defer cleanup()
	from, to := uuid.New(), uuid.New()
	mustSeedActiveProfile(t, st, from)
	mustSeedActiveProfile(t, st, to)

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
