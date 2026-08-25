//go:build integration

package http

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/database"
	chatservice "github.com/atpost/chat-message-service/internal/service"
	pgstore "github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPrivateDerivedReadsAreMemberOnlyAndNonEnumerating(t *testing.T) {
	dsn := os.Getenv("CHAT_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("CHAT_POSTGRES_DSN is required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS chat CASCADE`); err != nil {
		t.Fatal(err)
	}
	if err := pgstore.BootstrapSchema(ctx, pool, database.SetupSQL); err != nil {
		t.Fatal(err)
	}

	conversationID, memberID, leftID, outsiderID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	messageID, parentID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO chat.conversations (id,type,created_by) VALUES ($1,'group',$2)`, conversationID, memberID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO chat.conversation_members (conversation_id,user_id,role,left_at)
		VALUES ($1,$2,'member',NULL),($1,$3,'member',NOW())
	`, conversationID, memberID, leftID); err != nil {
		t.Fatal(err)
	}
	store := pgstore.New(pool)
	if err := store.UpsertMessageTranslation(ctx, &pgstore.MessageTranslation{
		MessageID: messageID, ConversationID: conversationID, TargetLang: "hi", TranslatedText: "namaste",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetOrCreateThread(ctx, conversationID, parentID); err != nil {
		t.Fatal(err)
	}

	svc := chatservice.New(store, nil, nil, nil, slog.Default(), time.Second)
	handler := New(svc, slog.Default())
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if userID := c.GetHeader("X-Test-User"); userID != "" {
			c.Set(userIDKey, userID)
		}
		c.Next()
	})
	handler.RegisterRoutes(router)

	memberTranslation := performPrivateRead(t, router, memberID, "/v1/chat/messages/"+messageID.String()+"/translation?lang=hi")
	if memberTranslation.Code != http.StatusOK {
		t.Fatalf("member translation status=%d body=%s", memberTranslation.Code, memberTranslation.Body.String())
	}
	unknownTranslationPath := "/v1/chat/messages/" + uuid.NewString() + "/translation?lang=hi"
	unknownTranslation := performPrivateRead(t, router, outsiderID, unknownTranslationPath)
	for label, viewer := range map[string]uuid.UUID{"outsider": outsiderID, "left_or_severed": leftID} {
		response := performPrivateRead(t, router, viewer, "/v1/chat/messages/"+messageID.String()+"/translation?lang=hi")
		assertByteEquivalentNotFound(t, label+" translation", response, unknownTranslation)
	}

	memberThread := performPrivateRead(t, router, memberID, "/v1/chat/conversations/"+conversationID.String()+"/threads/"+parentID.String())
	if memberThread.Code != http.StatusOK {
		t.Fatalf("member thread status=%d body=%s", memberThread.Code, memberThread.Body.String())
	}
	unknownThread := performPrivateRead(t, router, outsiderID, "/v1/chat/conversations/"+uuid.NewString()+"/threads/"+parentID.String())
	for label, viewer := range map[string]uuid.UUID{"outsider": outsiderID, "left_or_severed": leftID} {
		response := performPrivateRead(t, router, viewer, "/v1/chat/conversations/"+conversationID.String()+"/threads/"+parentID.String())
		assertByteEquivalentNotFound(t, label+" thread", response, unknownThread)
	}
}

func performPrivateRead(t *testing.T, router http.Handler, userID uuid.UUID, path string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("X-Test-User", userID.String())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func assertByteEquivalentNotFound(t *testing.T, label string, got, unknown *httptest.ResponseRecorder) {
	t.Helper()
	gotBody := got.Body.Bytes()
	unknownBody := unknown.Body.Bytes()
	if got.Code != http.StatusNotFound || unknown.Code != http.StatusNotFound || string(gotBody) != string(unknownBody) {
		t.Fatalf("%s enumerates private content: got=(%d %q) unknown=(%d %q)", label, got.Code, gotBody, unknown.Code, unknownBody)
	}
}
