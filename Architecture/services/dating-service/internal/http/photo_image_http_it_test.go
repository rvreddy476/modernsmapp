// Lane D6 dating photos over HTTP (integration, dating_it_test): attach
// refusals and the image route's per-viewer decision. Skipped unless
// TEST_PG_DSN is set; refuses a database not named *_test. media-service is a
// fake MediaPhotoClient.
package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/dating-service/database"
	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/dating-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type httpFakeMedia struct {
	mu         sync.Mutex
	owners     map[uuid.UUID]uuid.UUID
	status     map[uuid.UUID]string
	deliveries []string
}

func (f *httpFakeMedia) add(owner uuid.UUID, status string) uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := uuid.New()
	f.owners[id], f.status[id] = owner, status
	return id
}

func (f *httpFakeMedia) st(id uuid.UUID) *service.MediaPhotoStatus {
	return &service.MediaPhotoStatus{MediaID: id, OwnerMatches: true, Kind: "image", Status: f.status[id],
		ModerationStatus: "passed", ContentType: "image/jpeg", ModerationScanned: true, ModerationScanner: "mock",
		ModerationLabels: []service.MediaPhotoLabel{}, Prepared: true}
}

func (f *httpFakeMedia) PhotoOwnerStatus(_ context.Context, id, requester uuid.UUID) (*service.MediaPhotoStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.owners[id] != requester {
		return nil, service.ErrPhotoMediaNotFound
	}
	return f.st(id), nil
}

func (f *httpFakeMedia) PreparePhoto(_ context.Context, id, _ uuid.UUID, _ bool) (*service.MediaPhotoStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st(id), nil
}

func (f *httpFakeMedia) PhotoDeliveryURL(_ context.Context, id, owner uuid.UUID, variant string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.owners[id] != owner {
		return "", service.ErrPhotoMediaNotFound
	}
	f.deliveries = append(f.deliveries, variant)
	return "https://cdn.test/signed/" + variant + "?Signature=sig", nil
}

func (f *httpFakeMedia) DeletePhotoMedia(context.Context, uuid.UUID, uuid.UUID) error { return nil }

func (f *httpFakeMedia) delivered() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deliveries...)
}

type photoHTTPEnv struct {
	r     *gin.Engine
	svc   *service.Service
	st    *store.Store
	pool  *pgxpool.Pool
	media *httpFakeMedia
}

func setupPhotoHTTP(t *testing.T) *photoHTTPEnv {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping lane D6 photo HTTP integration tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to run against database %q: name must end in _test", cfg.ConnConfig.Database)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.BootstrapSchema(ctx, pool); err != nil {
		t.Fatalf("bootstrap schema: %v", err)
	}
	st := store.New(pool)
	svc := service.New(st, nil)
	svc.SetMessageClient(&stubMessageClient{})
	media := &httpFakeMedia{owners: map[uuid.UUID]uuid.UUID{}, status: map[uuid.UUID]string{}}
	svc.SetMediaPhotoClient(media)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(svc).WithInternalKey(testInternalKey).RegisterRoutes(r)
	return &photoHTTPEnv{r: r, svc: svc, st: st, pool: pool, media: media}
}

func (e *photoHTTPEnv) do(t *testing.T, method, path string, user uuid.UUID, body any) *httptest.ResponseRecorder {
	t.Helper()
	payload := ""
	if body != nil {
		raw, _ := json.Marshal(body)
		payload = string(raw)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", testInternalKey)
	req.Header.Set("X-User-Id", user.String())
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w
}

func envelopeOf(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out
}

// seedPhotoBasics writes the onboarding basics and walks to pending_photo.
func seedPhotoBasics(t *testing.T, st *store.Store, id uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	intent, gender, city, interested := "casual", "female", "Hyderabad", "male"
	if _, err := st.UpsertProfile(ctx, id, store.UpsertProfileParams{Intent: &intent, Gender: &gender, City: &city}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	if _, err := st.SetProfileBirthDate(ctx, id, time.Date(1996, 3, 1, 0, 0, 0, 0, time.UTC), store.BasicsSourceIdentity); err != nil {
		t.Fatalf("seed birth date: %v", err)
	}
	if _, err := st.SetProfileFirstName(ctx, id, "Meera", store.BasicsSourceIdentity); err != nil {
		t.Fatalf("seed first name: %v", err)
	}
	if _, err := st.UpsertPreferences(ctx, id, store.UpsertPreferencesParams{InterestedInGender: &interested}); err != nil {
		t.Fatalf("seed preferences: %v", err)
	}
	if _, err := st.TransitionProfileStatus(ctx, id, store.ProfileEventBasicsComplete, store.ProfileActorSystem); err != nil {
		t.Fatalf("basics: %v", err)
	}
}

func TestPhotoHTTP_AttachRefusals(t *testing.T) {
	e := setupPhotoHTTP(t)
	owner, other := uuid.New(), uuid.New()
	seedPhotoBasics(t, e.st, owner)

	w := e.do(t, http.MethodPost, "/v1/dating/photos", owner, map[string]any{"media_id": e.media.add(other, "ready"), "is_primary": true})
	if w.Code != http.StatusNotFound || codeOf(envelopeOf(t, w)) != CodePhotoMediaNotFound {
		t.Fatalf("foreign media: %d %s; want 404 PHOTO_MEDIA_NOT_FOUND", w.Code, w.Body.String())
	}
	w = e.do(t, http.MethodPost, "/v1/dating/photos", owner, map[string]any{"media_id": e.media.add(owner, "processing")})
	if w.Code != http.StatusConflict || codeOf(envelopeOf(t, w)) != CodePhotoMediaNotReady {
		t.Fatalf("processing media: %d %s; want 409 PHOTO_MEDIA_NOT_READY", w.Code, w.Body.String())
	}
	for i := 0; i < 6; i++ {
		w = e.do(t, http.MethodPost, "/v1/dating/photos", owner, map[string]any{"media_id": e.media.add(owner, "ready"), "sort_order": i})
		if w.Code != http.StatusCreated {
			t.Fatalf("photo %d: %d %s", i+1, w.Code, w.Body.String())
		}
	}
	w = e.do(t, http.MethodPost, "/v1/dating/photos", owner, map[string]any{"media_id": e.media.add(owner, "ready")})
	env := envelopeOf(t, w)
	if w.Code != http.StatusConflict || codeOf(env) != CodePhotoLimitReached {
		t.Fatalf("7th photo: %d %s; want 409 PHOTO_LIMIT_REACHED", w.Code, w.Body.String())
	}
	if details, _ := env["error"].(map[string]any)["details"].(map[string]any); details == nil || details["max_photos"] != float64(6) {
		t.Fatalf("7th photo details = %v; want max_photos 6", env["error"])
	}
}

func TestPhotoHTTP_NonMatchedViewerGetsOnlyTheBlurredImage(t *testing.T) {
	e := setupPhotoHTTP(t)
	ctx := context.Background()
	owner, stranger, match := uuid.New(), uuid.New(), uuid.New()
	seedPhotoBasics(t, e.st, owner)

	// The owner attaches a match_only primary and becomes active.
	media := e.media.add(owner, "ready")
	w := e.do(t, http.MethodPost, "/v1/dating/photos", owner, map[string]any{"media_id": media, "is_primary": true, "visibility": "match_only"})
	if w.Code != http.StatusCreated {
		t.Fatalf("attach: %d %s", w.Code, w.Body.String())
	}
	photoID, _ := dataOf(t, envelopeOf(t, w))["id"].(string)
	if err := e.st.RecordSelfieAttempt(ctx, owner, 0.99, "passed"); err != nil {
		t.Fatal(err)
	}
	if p, err := e.st.TransitionProfileStatus(ctx, owner, store.ProfileEventSelfiePassed, store.ProfileActorSystem); err != nil || p.ProfileStatus != store.ProfileStatusActive {
		t.Fatalf("activate: %+v %v", p, err)
	}
	if _, err := e.pool.Exec(ctx, `
        INSERT INTO dating_matches (user_a, user_b, status)
        VALUES (LEAST($1::uuid, $2::uuid), GREATEST($1::uuid, $2::uuid), 'matched')`, owner, match); err != nil {
		t.Fatalf("seed match: %v", err)
	}

	// The deck card each viewer receives (the nebula re-renders a passed card).
	cardURL := func(viewer uuid.UUID) (string, string) {
		t.Helper()
		if _, err := e.st.RecordPass(ctx, viewer, owner, ""); err != nil {
			t.Fatalf("record pass: %v", err)
		}
		deck, err := e.svc.GetPulseNebulaPassed(ctx, viewer, 100, 0)
		if err != nil {
			t.Fatalf("nebula: %v", err)
		}
		for _, card := range deck.Data {
			if card.CandidateID == owner {
				raw, _ := json.Marshal(card)
				return card.Profile.PrimaryPhotoURL, string(raw)
			}
		}
		t.Fatalf("owner not in %s's nebula", viewer)
		return "", ""
	}

	strangerURL, strangerCard := cardURL(stranger)
	if strangerURL != "/v1/dating/photos/"+photoID+"/blurred" {
		t.Fatalf("stranger card url = %q; want only the blurred route", strangerURL)
	}
	for _, leak := range []string{media.String(), "/full", "/media/", "?blurred=1"} {
		if strings.Contains(strangerCard, leak) {
			t.Fatalf("stranger card contains %q: %s", leak, strangerCard)
		}
	}
	if w := e.do(t, http.MethodGet, strangerURL, stranger, nil); w.Code != http.StatusTemporaryRedirect ||
		w.Header().Get("Location") != "https://cdn.test/signed/blurred?Signature=sig" ||
		w.Header().Get("Cache-Control") != "private, max-age=60" {
		t.Fatalf("stranger blurred: %d loc=%q cache=%q", w.Code, w.Header().Get("Location"), w.Header().Get("Cache-Control"))
	}
	before := len(e.media.delivered())
	if w := e.do(t, http.MethodGet, "/v1/dating/photos/"+photoID+"/full", stranger, nil); w.Code != http.StatusNotFound {
		t.Fatalf("stranger full: %d %s; want 404", w.Code, w.Body.String())
	}
	if len(e.media.delivered()) != before {
		t.Fatal("a full-image URL was requested from media-service for a stranger")
	}

	matchURL, _ := cardURL(match)
	if matchURL != "/v1/dating/photos/"+photoID+"/full" {
		t.Fatalf("matched card url = %q; want the full image", matchURL)
	}
	if w := e.do(t, http.MethodGet, matchURL, match, nil); w.Code != http.StatusTemporaryRedirect ||
		w.Header().Get("Location") != "https://cdn.test/signed/full?Signature=sig" {
		t.Fatalf("matched full: %d loc=%q", w.Code, w.Header().Get("Location"))
	}
	if w := e.do(t, http.MethodGet, "/v1/dating/photos/"+photoID+"/full", owner, nil); w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("owner full: %d", w.Code)
	}

	// Closing the match takes the full image back, cached deck or not.
	if _, err := e.pool.Exec(ctx, `UPDATE dating_matches SET status = 'closed' WHERE user_a = LEAST($1::uuid, $2::uuid) AND user_b = GREATEST($1::uuid, $2::uuid)`, owner, match); err != nil {
		t.Fatal(err)
	}
	if w := e.do(t, http.MethodGet, matchURL, match, nil); w.Code != http.StatusNotFound {
		t.Fatalf("full after the match closed: %d; want 404", w.Code)
	}
	if w := e.do(t, http.MethodGet, "/v1/dating/photos/"+uuid.NewString()+"/blurred", stranger, nil); w.Code != http.StatusNotFound {
		t.Fatalf("unknown photo: %d", w.Code)
	}
}
