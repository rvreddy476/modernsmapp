//go:build integration

package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/atpost/media-service/database"
	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLiveChatAttachmentReservationHTTPContract(t *testing.T) {
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
	uploaderID, mediaID, referenceID := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO media_assets (
			id, uploader_id, file_type, media_subtype, mime_type,
			file_size_bytes, storage_bucket, storage_key,
			processing_status, moderation_status, created_at, updated_at
		) VALUES ($1,$2,'image','chat','image/jpeg',100,'media',$3,'ready','passed',NOW(),NOW())
	`, mediaID, uploaderID, "protected/chat/http-"+mediaID.String()+".jpg"); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(service.New(postgres.New(pool), nil)).WithInternalKey("exact-internal-key").RegisterRoutes(router, func(c *gin.Context) { c.Next() }, func(c *gin.Context) { c.Next() })
	body, _ := json.Marshal(map[string]string{
		"reference_id": referenceID.String(),
		"uploader_id":  uploaderID.String(),
		"media_id":     mediaID.String(),
	})

	for label, testCase := range map[string]struct {
		key  string
		want int
	}{
		"missing key": {"", http.StatusUnauthorized},
		"wrong key":   {"wrong", http.StatusUnauthorized},
		"exact key":   {"exact-internal-key", http.StatusNoContent},
	} {
		t.Run(label, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/media/internal/chat-attachment/reserve", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			if testCase.key != "" {
				request.Header.Set("X-Internal-Service-Key", testCase.key)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != testCase.want {
				t.Fatalf("status=%d body=%s want=%d", response.Code, response.Body.String(), testCase.want)
			}
		})
	}
	var reserved int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM media_chat_attachment_reservations WHERE reference_id=$1 AND media_id=$2`, referenceID, mediaID).Scan(&reserved); err != nil {
		t.Fatal(err)
	}
	if reserved != 1 {
		t.Fatalf("HTTP success did not durably reserve the canonical asset: count=%d", reserved)
	}
}
