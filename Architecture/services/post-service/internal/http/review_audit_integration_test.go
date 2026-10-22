//go:build integration

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

	"github.com/atpost/post-service/database"
	"github.com/atpost/post-service/internal/service"
	store "github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// openReviewAuditDB applies the real setup.sql and every embedded migration to
// a *_test database named by M7_POSTGRES_DSN (the post moderation suite's DSN).
func openReviewAuditDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("M7_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("M7_POSTGRES_DSN is required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("parse M7_POSTGRES_DSN")
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to run against database %q: name must end in _test", cfg.ConnConfig.Database)
	}
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	// Cross-service parent table, as in openM7PostDB.
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS media_assets (
			id UUID PRIMARY KEY,
			uploader_id UUID NOT NULL,
			file_type TEXT NOT NULL,
			processing_status TEXT NOT NULL,
			moderation_status TEXT NOT NULL DEFAULT 'pending',
			duration_seconds INTEGER,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		t.Fatal(err)
	}
	// media-service's real media_assets carries duration_ms, which the search
	// eligibility bump reads.
	if _, err := pool.Exec(ctx, `ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS duration_ms INT`); err != nil {
		t.Fatal(err)
	}
	// The real startup path: setup.sql, then every pending embedded migration.
	// The full chain references other services' tables (e.g. user_preferences),
	// so the DSN must name a post-service integration database that already
	// carries them (post_it_test does).
	if err := store.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatalf("bootstrap real schema: %v", err)
	}
	return pool
}

func newAuditRouter(pool *pgxpool.Pool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(service.New(store.New(pool), nil, nil), nil).WithInternalKey(gateTestKey)
	h.RegisterRoutes(r)
	h.RegisterReelDiscoveryRoutes(r)
	h.RegisterReportRoutes(r)
	return r
}

// newAuditReviewerRouter is newAuditRouter with the service-token verifier
// wired, and the reviewer-service signer the internal review writes need
// (security sweep #11).
func newAuditReviewerRouter(t *testing.T, pool *pgxpool.Pool) (*gin.Engine, *servicetoken.Signer) {
	t.Helper()
	keys := adminTestVerifier(t)
	reviewer, err := servicetoken.NewSignerFromBase64(IssuerReviewerService, "v1", keys.reviewer)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(service.New(store.New(pool), nil, nil), nil).WithInternalKey(gateTestKey).WithServiceAuth(keys.v)
	h.RegisterRoutes(r)
	h.RegisterReelDiscoveryRoutes(r)
	h.RegisterReportRoutes(r)
	return r, reviewer
}

// serveAsReviewer sends exactly what reviewer-service sends: the key, its
// token for op, a JSON body and no user headers.
func serveAsReviewer(t *testing.T, r *gin.Engine, reviewer *servicetoken.Signer, op, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	tok, err := reviewer.Mint(AudiencePost, "ml-prefilter", []string{op}, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return adminServe(r, http.MethodPost, path, body, map[string]string{
		"X-Internal-Service-Key": gateTestKey,
		ServiceAuthHeader:        "Bearer " + tok,
	})
}

func seedReviewPost(t *testing.T, pool *pgxpool.Pool, visibility, reviewStatus string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO posts (id,author_id,text,visibility,content_type,review_status,search_rev,created_at,updated_at)
		VALUES ($1,$2,'a4 audit proof',$3,'post',$4,1,NOW(),NOW())
	`, id, uuid.New(), visibility, reviewStatus); err != nil {
		t.Fatal(err)
	}
	return id
}

func postState(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) (visibility, reviewStatus string) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT visibility, review_status FROM posts WHERE id=$1`, id).Scan(&visibility, &reviewStatus); err != nil {
		t.Fatal(err)
	}
	return
}

func auditRows(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) []store.ReviewAuditEntry {
	t.Helper()
	rows, err := store.New(pool).ListReviewAudit(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// Security sweep #11: the internal review-status write no longer takes a
// gateway moderator or the bare internal key. Both are refused before
// anything changes; reviewer-service's signed token is admitted and the
// audit row names it.
func TestReviewStatusChangeAuditsReviewerService(t *testing.T) {
	pool := openReviewAuditDB(t)
	r, reviewer := newAuditReviewerRouter(t, pool)
	postID := seedReviewPost(t, pool, "public", "flagged")
	body := `{"post_id":"` + postID.String() + `","status":"approved","reason":"cleared on review"}`

	for _, who := range []gateCaller{callerUser, callerModerator, {name: "key only", key: gateTestKey}} {
		if res := serveGate(r, http.MethodPost, "/v1/posts/internal/review-status", body, who); res.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d body=%s, want 401", who.name, res.Code, res.Body.String())
		}
	}
	if _, rs := postState(t, pool, postID); rs != "flagged" {
		t.Fatalf("a refused caller changed review_status to %q", rs)
	}
	if n := len(auditRows(t, pool, postID)); n != 0 {
		t.Fatalf("refused calls wrote %d audit rows", n)
	}

	res := serveAsReviewer(t, r, reviewer, OpReviewStatusWrite, "/v1/posts/internal/review-status", body)
	if res.Code != http.StatusOK {
		t.Fatalf("reviewer-service status=%d body=%s", res.Code, res.Body.String())
	}
	if _, rs := postState(t, pool, postID); rs != "approved" {
		t.Fatalf("review_status=%q want approved", rs)
	}
	rows := auditRows(t, pool, postID)
	if len(rows) != 1 {
		t.Fatalf("audit rows=%d want 1", len(rows))
	}
	got := rows[0]
	if got.Field != "review_status" || got.PreviousValue != "flagged" || got.NewValue != "approved" ||
		got.ActorType != "service" || got.ActorUserID != nil || got.ActorService == nil ||
		*got.ActorService != IssuerReviewerService || got.Reason == nil || *got.Reason != "cleared on review" || got.CreatedAt.IsZero() {
		b, _ := json.Marshal(got)
		t.Fatalf("audit row wrong: %s", b)
	}

	// A repeat is a no-op (no longer flagged) and writes no second row.
	if res := serveAsReviewer(t, r, reviewer, OpReviewStatusWrite, "/v1/posts/internal/review-status", body); res.Code != http.StatusOK {
		t.Fatalf("repeat status=%d", res.Code)
	}
	if n := len(auditRows(t, pool, postID)); n != 1 {
		t.Fatalf("no-op wrote an audit row: rows=%d", n)
	}
}

func TestVisibilityChangeAuditsReviewerService(t *testing.T) {
	pool := openReviewAuditDB(t)
	r, reviewer := newAuditReviewerRouter(t, pool)
	postID := seedReviewPost(t, pool, "staged", "approved")
	body := `{"post_id":"` + postID.String() + `","visibility":"public"}`
	for _, who := range []gateCaller{callerUser, callerModerator, {name: "key only", key: gateTestKey}} {
		if res := serveGate(r, http.MethodPost, "/v1/posts/internal/visibility", body, who); res.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d, want 401", who.name, res.Code)
		}
	}
	if v, _ := postState(t, pool, postID); v != "staged" {
		t.Fatalf("a refused caller changed visibility to %q", v)
	}
	// The review-status op does not open the visibility write.
	if res := serveAsReviewer(t, r, reviewer, OpReviewStatusWrite, "/v1/posts/internal/visibility", body); res.Code != http.StatusForbidden {
		t.Fatalf("wrong op status=%d, want 403", res.Code)
	}
	res := serveAsReviewer(t, r, reviewer, OpVisibilityWrite, "/v1/posts/internal/visibility", body)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if v, _ := postState(t, pool, postID); v != "public" {
		t.Fatalf("visibility=%q want public", v)
	}
	rows := auditRows(t, pool, postID)
	if len(rows) != 1 {
		t.Fatalf("audit rows=%d want 1", len(rows))
	}
	got := rows[0]
	if got.Field != "visibility" || got.PreviousValue != "staged" || got.NewValue != "public" ||
		got.ActorType != "service" || got.ActorService == nil || *got.ActorService != IssuerReviewerService {
		b, _ := json.Marshal(got)
		t.Fatalf("audit row wrong: %s", b)
	}
}

func TestReviewAuditIsAppendOnly(t *testing.T) {
	pool := openReviewAuditDB(t)
	r, reviewer := newAuditReviewerRouter(t, pool)
	postID := seedReviewPost(t, pool, "public", "flagged")
	body := `{"post_id":"` + postID.String() + `","status":"approved"}`
	if res := serveAsReviewer(t, r, reviewer, OpReviewStatusWrite, "/v1/posts/internal/review-status", body); res.Code != http.StatusOK {
		t.Fatalf("status=%d", res.Code)
	}
	ctx := context.Background()
	if n := len(auditRows(t, pool, postID)); n != 1 {
		t.Fatalf("audit rows=%d want 1 before tamper attempts", n)
	}
	if _, err := pool.Exec(ctx, `UPDATE post_review_audit SET new_value='rejected' WHERE post_id=$1`, postID); err == nil {
		t.Fatal("audit UPDATE was allowed")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM post_review_audit WHERE post_id=$1`, postID); err == nil {
		t.Fatal("audit DELETE was allowed")
	}
}

func TestFlaggedReelsQueueServesModeratorOnly(t *testing.T) {
	pool := openReviewAuditDB(t)
	r := newAuditRouter(pool)
	reelID := uuid.New()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO moderation_reviews (reel_id, reviewer_type, decision, reason)
		VALUES ($1, 'auto', 'flagged', 'a4 proof')
	`, reelID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/reels/moderation/flagged?limit=100", "/v1/reels/" + reelID.String() + "/moderation"} {
		if got := serveGate(r, http.MethodGet, path, "", callerAnon).Code; got != http.StatusUnauthorized {
			t.Errorf("%s anonymous status=%d want 401", path, got)
		}
		if got := serveGate(r, http.MethodGet, path, "", callerUser).Code; got != http.StatusForbidden {
			t.Errorf("%s user status=%d want 403", path, got)
		}
		res := serveGate(r, http.MethodGet, path, "", callerModerator)
		if res.Code != http.StatusOK {
			t.Errorf("%s moderator status=%d body=%s", path, res.Code, res.Body.String())
		}
		// The shared queue may page past this reel; its own history must show it.
		if strings.Contains(path, reelID.String()) && !strings.Contains(res.Body.String(), reelID.String()) {
			t.Errorf("%s moderator response does not include the seeded review", path)
		}
	}
}

// /v1/admin/reports reads content_reports, which post-service's migrations do
// not create; its gate matrix is covered by TestModeratorRoutesMatrix.
func TestAdminCommentQueueServesModeratorOnly(t *testing.T) {
	pool := openReviewAuditDB(t)
	r := newAuditRouter(pool)
	for _, path := range []string{"/v1/admin/comments/moderation"} {
		if got := serveGate(r, http.MethodGet, path, "", callerAnon).Code; got != http.StatusUnauthorized {
			t.Errorf("%s anonymous status=%d want 401", path, got)
		}
		if got := serveGate(r, http.MethodGet, path, "", callerUser).Code; got != http.StatusForbidden {
			t.Errorf("%s user status=%d want 403", path, got)
		}
		if res := serveGate(r, http.MethodGet, path, "", callerAdmin); res.Code != http.StatusOK {
			t.Errorf("%s admin status=%d body=%s", path, res.Code, res.Body.String())
		}
	}
}
