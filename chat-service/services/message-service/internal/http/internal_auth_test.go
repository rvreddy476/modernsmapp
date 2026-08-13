package http

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestInternalChatBypassIsExactAndKeyBound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(AuthMiddlewareWithKeys(JWTKeySet{InternalServiceKey: "internal-key"}, slog.New(slog.NewTextHandler(io.Discard, nil))))
	router.POST("/internal/v1/chat/media-access", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	router.POST("/internal/v1/chatty/media-access", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	request := httptest.NewRequest(http.MethodPost, "/internal/v1/chat/media-access", strings.NewReader(`{}`))
	request.Header.Set("X-Internal-Service-Key", "internal-key")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("valid internal request returned %d", response.Code)
	}

	for name, path := range map[string]string{
		"wrong key":         "/internal/v1/chat/media-access",
		"confusable prefix": "/internal/v1/chatty/media-access",
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
			request.Header.Set("X-Internal-Service-Key", "wrong")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("unsafe internal bypass returned %d", response.Code)
			}
		})
	}
}
