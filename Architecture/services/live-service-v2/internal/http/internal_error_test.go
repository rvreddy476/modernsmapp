package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestInternalErrorsAreNotSentToClients is LV-L5: an error with no mapping
// answers 500 INTERNAL_ERROR with a fixed message; the wrapped detail
// (LiveKit, Postgres, Redis) never reaches the body.
func TestInternalErrorsAreNotSentToClients(t *testing.T) {
	gin.SetMode(gin.TestMode)
	detail := `livekit create room: dial tcp 10.0.3.7:7880: connection refused; pq: relation "live_v2.secret_table"`
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/livestream/streams/x/start", nil)
	writeServiceErr(c, fmt.Errorf("start: %w", errors.New(detail)))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	for _, leak := range []string{"livekit", "10.0.3.7", "secret_table", "connection refused", "start:"} {
		if strings.Contains(body, leak) {
			t.Fatalf("500 body leaks %q: %s", leak, body)
		}
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("body: %v %s", err, body)
	}
	if env.Error.Code != "INTERNAL_ERROR" || env.Error.Message != internalErrorMessage {
		t.Fatalf("error = %+v", env.Error)
	}
}
