package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/group-service/internal/service"
	"github.com/gin-gonic/gin"
)

/*
The reaction routes are registered on the real engine beside the legacy spark
pair, and a reaction outside the allowlist reaches the client as 422 through
the error mapping the handler actually uses.
*/

func TestReactionRoutesAreRegisteredAndSparkRoutesRemain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(nil).RegisterRoutes(r)

	const reaction = "/v1/groups/:groupId/posts/v2/:postId/reaction"
	const spark = "/v1/groups/:groupId/posts/v2/:postId/spark"

	byKey := map[string]string{}
	for _, ri := range r.Routes() {
		byKey[ri.Method+" "+ri.Path] = ri.Handler
	}
	want := map[string]string{
		http.MethodPut + " " + reaction:    ".SetGroupPostReaction",
		http.MethodDelete + " " + reaction: ".RemoveGroupPostReaction",
		// Mobile depends on these; they must not be removed or re-pointed.
		http.MethodPost + " " + spark:   ".SparkGroupPost",
		http.MethodDelete + " " + spark: ".UnsparkGroupPost",
	}
	for key, handler := range want {
		h, ok := byKey[key]
		if !ok {
			t.Errorf("%s is not registered", key)
			continue
		}
		if !strings.Contains(h, handler) {
			t.Errorf("%s is served by %s, not %s", key, h, handler)
		}
	}
}

func TestReactionOutsideAllowlistIs422(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/", nil)

	handleServiceError(c, service.ErrReactionNotInAllowlist)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422; body %s", w.Code, w.Body.String())
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v — %s", err, w.Body.String())
	}
	if body.Error.Code != "VALIDATION_ERROR" {
		t.Errorf("code %q, want VALIDATION_ERROR", body.Error.Code)
	}
	for _, r := range service.ReactionAllowlist {
		if !strings.Contains(body.Error.Message, r) {
			t.Errorf("message %q does not list %q — the client cannot learn the allowlist from the refusal", body.Error.Message, r)
		}
	}
}

// The handler encodes the service's own state type; a bespoke response
// struct here would drift from what the service documents.
func TestReactionHandlersEncodeServiceState(t *testing.T) {
	for _, name := range []string{"SetGroupPostReaction", "RemoveGroupPostReaction"} {
		body := codeOnly(handlerBody(t, "handler_reactions.go", name))
		if !strings.Contains(body, "api.JSON(c.Writer, http.StatusOK, state, nil)") {
			t.Errorf("%s does not return the service state as the response body", name)
		}
		if !strings.Contains(body, "handleServiceError(c, err)") {
			t.Errorf("%s does not route service errors through handleServiceError", name)
		}
	}
}
