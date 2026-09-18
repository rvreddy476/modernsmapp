package http

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// A playlist could be created and added to, but never edited (2026-09-18).
//
// There was no PATCH anywhere under /v1/playlists: a playlist could not be
// renamed, re-described, given a cover, reordered, or made public or private
// after the fact — the only way to change a playlist's visibility was to
// delete it and build it again. The inventory is read from the REAL router,
// the way story_route_inventory_test.go and the video-series routes test do,
// so this cannot pass by someone writing a handler nobody wired up.

func registeredPlaylistRoutes(t *testing.T) map[string]bool {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handler{}
	h.RegisterRoutes(r)

	out := map[string]bool{}
	for _, info := range r.Routes() {
		if strings.HasPrefix(info.Path, "/v1/playlists") {
			out[info.Method+" "+info.Path] = true
		}
	}
	return out
}

func TestPlaylistHasEditRoutes(t *testing.T) {
	routes := registeredPlaylistRoutes(t)
	for _, want := range []string{
		"PATCH /v1/playlists/:playlistId",
		"PATCH /v1/playlists/:playlistId/items/:postId",
	} {
		if !routes[want] {
			t.Errorf("%s is not registered.\nA playlist can be created and added to but never edited: "+
				"no rename, no cover, no reorder, and no way to make one public or private. "+
				"Registered playlist routes: %v", want, keysOf(routes))
		}
	}
}

// Both edits are the owner's alone, so an anonymous caller must be refused at
// the handler, before anything reaches the service. (The Handler here has a
// nil svc: a request that got past the identity check would panic rather
// than quietly pass.)
func TestPlaylistEditsRequireIdentity(t *testing.T) {
	playlistID := uuid.NewString()
	postID := uuid.NewString()
	cases := []struct{ method, path string }{
		{http.MethodPatch, "/v1/playlists/" + playlistID},
		{http.MethodPatch, "/v1/playlists/" + playlistID + "/items/" + postID},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			w := performVideoSeriesRequest(t, tc.method, tc.path, nil)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s with no X-User-Id: status=%d want 401 (body %s)",
					tc.method, tc.path, w.Code, w.Body.String())
			}
		})
	}
}

// position 0 — move to the top — is the commonest reorder there is, and a
// pointer field tagged binding:"required" would refuse it, because the
// validator dereferences the pointer and reads 0 as absent. A missing
// position is still a 400; a position of 0 must reach the service.
func TestMovePlaylistItemAcceptsPositionZero(t *testing.T) {
	path := "/v1/playlists/" + uuid.NewString() + "/items/" + uuid.NewString()
	headers := map[string]string{"X-User-Id": uuid.NewString()}

	w := performVideoSeriesRequestWithBody(t, http.MethodPatch, path, headers, `{}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("no position: status=%d want 400 (body %s)", w.Code, w.Body.String())
	}

	// With a nil svc, a request that gets past validation reaches the service
	// and panics; that panic — or any status that is not a 400 — is the
	// assertion that position 0 was NOT read as "absent".
	code, panicked := statusOrPanic(func() int {
		return performVideoSeriesRequestWithBody(t, http.MethodPatch, path, headers, `{"position":0}`).Code
	})
	if !panicked && code == http.StatusBadRequest {
		t.Fatal("position 0 was refused as a missing field; move-to-top is a legitimate reorder")
	}
}

// statusOrPanic runs a request against a Handler with a nil svc: reaching the
// service is a panic, which is the signal that validation let the request
// through.
func statusOrPanic(do func() int) (code int, panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	return do(), false
}
