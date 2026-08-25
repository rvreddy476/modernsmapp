package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCallHandlerDefaultsDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(nil, nil).RegisterRoutes(router)
	request := httptest.NewRequest(http.MethodPost, "/v1/calls", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("default handler exposed calls with status %d", response.Code)
	}
}
