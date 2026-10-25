//go:build integration

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

	"github.com/atpost/media-service/database"
	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The wire contract end to end on real PostgreSQL (CI): a service claims
// through the keyed route, the owner's DELETE /v1/media/:id is a 409 naming
// where the asset is used, and after the release the claim is gone.
func TestLiveMediaClaimHTTPContract(t *testing.T) {
	dsn := os.Getenv("MEDIA_CHAT_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("MEDIA_CHAT_POSTGRES_DSN is required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := postgres.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatal(err)
	}
	owner, mediaID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO media_assets (
			id, uploader_id, file_type, media_subtype, mime_type,
			file_size_bytes, storage_bucket, storage_key,
			processing_status, moderation_status, created_at, updated_at
		) VALUES ($1,$2,'image','general','image/jpeg',100,'media',$3,'ready','passed',NOW(),NOW())
	`, mediaID, owner, "user/"+owner.String()+"/"+mediaID.String()+"/original"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM media_claims WHERE media_id = $1`, mediaID)
		_, _ = pool.Exec(bg, `DELETE FROM media_assets WHERE id = $1`, mediaID)
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	noop := func(c *gin.Context) { c.Next() }
	New(service.New(postgres.New(pool), nil)).WithInternalKey("exact-internal-key").RegisterRoutes(router, noop, noop)
	call := func(method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	keyed := map[string]string{"X-Internal-Service-Key": "exact-internal-key"}
	ref := uuid.NewString()
	claim, _ := json.Marshal(map[string]any{
		"claimant": "channel-service", "kind": "channel_update", "ref": ref, "media_ids": []uuid.UUID{mediaID},
	})

	if w := call(http.MethodPost, "/v1/media/internal/claims", string(claim), keyed); w.Code != http.StatusOK {
		t.Fatalf("claim: %d %s", w.Code, w.Body.String())
	}
	missing, _ := json.Marshal(map[string]any{
		"claimant": "channel-service", "kind": "channel_update", "ref": ref, "media_ids": []uuid.UUID{uuid.New()},
	})
	if w := call(http.MethodPost, "/v1/media/internal/claims", string(missing), keyed); w.Code != http.StatusNotFound ||
		!strings.Contains(w.Body.String(), `"MEDIA_NOT_FOUND"`) {
		t.Fatalf("claim of a missing asset: %d %s", w.Code, w.Body.String())
	}

	w := call(http.MethodDelete, "/v1/media/"+mediaID.String(), "", map[string]string{"X-User-Id": owner.String()})
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"MEDIA_IN_USE"`) ||
		!strings.Contains(w.Body.String(), "1 channel update") {
		t.Fatalf("owner delete of claimed media: %d %s", w.Code, w.Body.String())
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM media_assets WHERE id = $1)`, mediaID).Scan(&exists); err != nil || !exists {
		t.Fatalf("a refused owner delete removed the asset (exists=%v err=%v)", exists, err)
	}

	release, _ := json.Marshal(map[string]any{"claimant": "channel-service", "kind": "channel_update", "ref": ref})
	if w := call(http.MethodPost, "/v1/media/internal/claims/release", string(release), keyed); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), `"released":1`) {
		t.Fatalf("release: %d %s", w.Code, w.Body.String())
	}
	var claims int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM media_claims WHERE media_id = $1`, mediaID).Scan(&claims); err != nil || claims != 0 {
		t.Fatalf("claims after release = %d (%v)", claims, err)
	}
}
