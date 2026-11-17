package http

import (
	"net/http"
	"testing"

	"github.com/atpost/post-service/internal/service"
	"github.com/google/uuid"
)

// reels-18 through the real router: saving a post the caller may not open
// (pending, rejected, processing, scheduled, outside its audience — the
// loader's ErrPostNotVisible) is the 404 a missing post gets, byte for byte.
// It used to be a 500 echoing which of the two it was.
func TestAddBookmark_PostNotOpenIsTheMissingPost404(t *testing.T) {
	hidden := gatedRepostRouter(nil, service.ErrPostNotVisible)
	missing := gatedRepostRouter(nil, service.ErrPostNotFound)
	id := uuid.NewString()
	for _, body := range []string{"", `{"collection_id":"` + uuid.NewString() + `"}`} {
		path := "/v1/posts/" + id + "/bookmark"
		code, env, hiddenBody := heartCall(t, hidden, http.MethodPost, path, body)
		if code != http.StatusNotFound || env.Error == nil || env.Error.Code != "POST_NOT_FOUND" {
			t.Fatalf("hidden: POST %s %q = %d %s, want 404 POST_NOT_FOUND", path, body, code, hiddenBody)
		}
		missingCode, _, missingBody := heartCall(t, missing, http.MethodPost, path, body)
		if missingCode != code || missingBody != hiddenBody {
			t.Fatalf("POST %s %q: hidden = %d %s, missing = %d %s; the two must not differ", path, body, code, hiddenBody, missingCode, missingBody)
		}
	}
}
