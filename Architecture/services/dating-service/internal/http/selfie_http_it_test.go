// Lane D5 selfie verification over HTTP (integration, dating_it_test): the
// blink challenge contract, the status codes clients branch on and the
// admin-only review queue. Skipped unless TEST_PG_DSN is set; refuses a
// database not named *_test.
package http

import (
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

// fakeSelfieLiveness answers every check with a clean two-blink recording of
// one face at the given similarity (or the given result).
type fakeSelfieLiveness struct{ result service.LivenessResult }

func (f fakeSelfieLiveness) CheckLiveness(context.Context, service.LivenessRequest) (*service.LivenessResult, error) {
	r := f.result
	return &r, nil
}

func blinkLiveness(similarity float64) fakeSelfieLiveness {
	return fakeSelfieLiveness{service.LivenessResult{BlinksDetected: 2, FramesAnalysed: 32, DurationMs: 3000,
		SingleFace: true, SameFaceAcrossFrames: true, Similarity: similarity, Provider: "mock"}}
}

type selfieHTTPEnv struct {
	r   *gin.Engine
	svc *service.Service
	st  *store.Store
}

func setupSelfieHTTP(t *testing.T) *selfieHTTPEnv {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping lane D5 selfie HTTP integration tests")
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
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(svc).WithInternalKey(testInternalKey).RegisterRoutes(r)
	return &selfieHTTPEnv{r: r, svc: svc, st: st}
}

// seedSelfieUser seeds basics and a primary photo (approved or pending) and
// walks the profile as far as the evidence allows.
func seedSelfieUser(t *testing.T, st *store.Store, id uuid.UUID, approvePhoto bool) {
	t.Helper()
	ctx := context.Background()
	// Lane D9: the biometric check needs explicit consent.
	if _, err := st.SetConsent(ctx, id, service.ConsentBiometricSelfie, true, "test"); err != nil {
		t.Fatalf("seed selfie consent: %v", err)
	}
	intent, gender, city, interested := "casual", "female", "Hyderabad", "everyone"
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
	events := []store.ProfileEvent{store.ProfileEventBasicsComplete}
	if approvePhoto {
		if _, err := st.SetPhotoModerationStatus(ctx, photo.ID, "approved", ""); err != nil {
			t.Fatalf("approve photo: %v", err)
		}
		events = append(events, store.ProfileEventPhotoApproved)
	}
	for _, ev := range events {
		if _, err := st.TransitionProfileStatus(ctx, id, ev, store.ProfileActorSystem); err != nil {
			t.Fatalf("seed transition %s: %v", ev, err)
		}
	}
}

func (e *selfieHTTPEnv) do(t *testing.T, method, path string, user uuid.UUID, scopes string, body any) (int, map[string]any) {
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
	if scopes != "" {
		req.Header.Set("X-Scopes", scopes)
	}
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func dataOf(t *testing.T, env map[string]any) map[string]any {
	t.Helper()
	d, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("no data in %v", env)
	}
	return d
}

func codeOf(env map[string]any) string {
	if e, ok := env["error"].(map[string]any); ok {
		s, _ := e["code"].(string)
		return s
	}
	return ""
}

func (e *selfieHTTPEnv) challenge(t *testing.T, user uuid.UUID) string {
	t.Helper()
	code, env := e.do(t, http.MethodPost, "/v1/dating/verification/selfie/challenge", user, "", nil)
	if code != http.StatusOK {
		t.Fatalf("challenge: %d %v", code, env)
	}
	d := dataOf(t, env)
	if d["instruction"] != "blink_twice" || d["max_duration_ms"] != float64(4000) || d["expires_at"] == "" {
		t.Fatalf("challenge body = %v; want instruction blink_twice, max_duration_ms 4000", d)
	}
	if _, hasPose := d["pose"]; hasPose {
		t.Fatalf("challenge still carries a pose: %v", d)
	}
	id, _ := d["challenge_id"].(string)
	return id
}

func submitBody(challenge string) map[string]string {
	return map[string]string{"video_media_id": uuid.NewString(), "challenge_id": challenge}
}

func TestSelfieHTTP_BlinkChallengeThenPassActivates(t *testing.T) {
	e := setupSelfieHTTP(t)
	user := uuid.New()
	seedSelfieUser(t, e.st, user, true)
	e.svc.SetLivenessClient(blinkLiveness(95))
	code, env := e.do(t, http.MethodPost, "/v1/dating/verification/selfie", user, "", submitBody(e.challenge(t, user)))
	if code != http.StatusOK {
		t.Fatalf("submit: %d %v", code, env)
	}
	d := dataOf(t, env)
	if d["status"] != "passed" || d["profile_status"] != "active" || d["passed"] != true {
		t.Fatalf("submit body = %v", d)
	}
	if _, leaked := d["similarity"]; leaked {
		t.Fatalf("response exposes the similarity score: %v", d)
	}
}

func TestSelfieHTTP_NotEnoughBlinksAndTooLong(t *testing.T) {
	e := setupSelfieHTTP(t)
	user := uuid.New()
	seedSelfieUser(t, e.st, user, true)
	one := blinkLiveness(97)
	one.result.BlinksDetected, one.result.Reason = 1, "NOT_ENOUGH_BLINKS"
	e.svc.SetLivenessClient(one)
	code, env := e.do(t, http.MethodPost, "/v1/dating/verification/selfie", user, "", submitBody(e.challenge(t, user)))
	if code != http.StatusOK || dataOf(t, env)["status"] != "failed" || dataOf(t, env)["reason"] != "NOT_ENOUGH_BLINKS" {
		t.Fatalf("one blink: %d %v", code, env)
	}
	long := blinkLiveness(97)
	long.result.Reason, long.result.DurationMs = "VIDEO_TOO_LONG", 6500
	e.svc.SetLivenessClient(long)
	code, env = e.do(t, http.MethodPost, "/v1/dating/verification/selfie", user, "", submitBody(e.challenge(t, user)))
	if code != http.StatusUnprocessableEntity || codeOf(env) != CodeSelfieVideoTooLong {
		t.Fatalf("too long: %d %v; want 422 SELFIE_VIDEO_TOO_LONG", code, env)
	}
}

func TestSelfieHTTP_PrimaryPhotoNotApproved409(t *testing.T) {
	e := setupSelfieHTTP(t)
	user := uuid.New()
	seedSelfieUser(t, e.st, user, false)
	e.svc.SetLivenessClient(blinkLiveness(99))
	if code, env := e.do(t, http.MethodPost, "/v1/dating/verification/selfie/challenge", user, "", nil); code != http.StatusConflict || codeOf(env) != CodePrimaryPhotoNotApproved {
		t.Fatalf("challenge: %d %v", code, env)
	}
	code, env := e.do(t, http.MethodPost, "/v1/dating/verification/selfie", user, "", submitBody(uuid.NewString()))
	if code != http.StatusConflict || codeOf(env) != CodePrimaryPhotoNotApproved {
		t.Fatalf("submit: %d %v", code, env)
	}
}

func TestSelfieHTTP_AttemptLimit429(t *testing.T) {
	e := setupSelfieHTTP(t)
	user := uuid.New()
	seedSelfieUser(t, e.st, user, true)
	e.svc.SetSelfieConfig(service.SelfieConfig{PassThreshold: 90, ReviewThreshold: 80, MaxAttemptsPerDay: 1, RequiredBlinks: 2, MaxVideoDurationMs: 4000})
	e.svc.SetLivenessClient(blinkLiveness(20))
	first, second := e.challenge(t, user), e.challenge(t, user)
	if code, env := e.do(t, http.MethodPost, "/v1/dating/verification/selfie", user, "", submitBody(first)); code != http.StatusOK || dataOf(t, env)["status"] != "failed" {
		t.Fatalf("attempt 1: %d %v", code, env)
	}
	code, env := e.do(t, http.MethodPost, "/v1/dating/verification/selfie", user, "", submitBody(second))
	if code != http.StatusTooManyRequests || codeOf(env) != CodeSelfieAttemptsExceeded {
		t.Fatalf("attempt 2: %d %v; want 429 SELFIE_ATTEMPTS_EXCEEDED", code, env)
	}
}

func TestSelfieHTTP_ReviewQueueIsAdminOnly(t *testing.T) {
	e := setupSelfieHTTP(t)
	user, admin := uuid.New(), uuid.New()
	seedSelfieUser(t, e.st, user, true)
	e.svc.SetLivenessClient(blinkLiveness(85))
	if code, env := e.do(t, http.MethodPost, "/v1/dating/verification/selfie", user, "", submitBody(e.challenge(t, user))); code != http.StatusOK || dataOf(t, env)["status"] != "pending_review" {
		t.Fatalf("borderline submit: %d %v", code, env)
	}
	reviewPath := "/v1/dating/admin/verification/selfie/" + user.String() + "/review"
	if code, _ := e.do(t, http.MethodGet, "/v1/dating/admin/verification/selfie/pending", user, "", nil); code != http.StatusForbidden {
		t.Fatalf("queue as a user: %d", code)
	}
	if code, _ := e.do(t, http.MethodPost, reviewPath, user, "user", map[string]string{"decision": "approve"}); code != http.StatusForbidden {
		t.Fatalf("self-approval: %d", code)
	}
	code, env := e.do(t, http.MethodGet, "/v1/dating/admin/verification/selfie/pending?limit=200", admin, "moderator", nil)
	if code != http.StatusOK {
		t.Fatalf("queue as moderator: %d %v", code, env)
	}
	found := false
	items, _ := dataOf(t, env)["items"].([]any)
	for _, it := range items {
		if m, ok := it.(map[string]any); ok && m["user_id"] == user.String() {
			found = m["blinks_detected"] == float64(2) && m["instruction"] == "blink_twice"
		}
	}
	if !found {
		t.Fatalf("user missing from the review queue (or without blinks/instruction)")
	}
	code, env = e.do(t, http.MethodPost, reviewPath, admin, "moderator", map[string]string{"decision": "approve", "reason": "same person"})
	if code != http.StatusOK || dataOf(t, env)["profile_status"] != "active" {
		t.Fatalf("approve: %d %v", code, env)
	}
	if code, env := e.do(t, http.MethodPost, reviewPath, admin, "moderator", map[string]string{"decision": "approve"}); code != http.StatusConflict || codeOf(env) != CodeSelfieNotPendingReview {
		t.Fatalf("second approve: %d %v", code, env)
	}
}
