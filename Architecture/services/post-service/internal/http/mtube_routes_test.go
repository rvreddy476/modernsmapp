package http

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/post-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// MTube contracts (2026-09-27). The inventory is read from the REAL router,
// the way story_route_inventory_test.go and playlist_routes_test.go do, so
// none of these can pass by someone writing a handler nobody wired up.

// mtubeRoutes is every route this pass added, with the policy the handler
// enforces before anything reaches the service.
var mtubeRoutes = map[string]string{
	"PATCH /v1/posts/:postId":              "owner",
	"GET /v1/posts/me/summary":             "owner",
	"POST /v1/posts/:postId/watch-later":   "viewer",
	"DELETE /v1/posts/:postId/watch-later": "viewer",
	"GET /v1/playlists/system/:kind":       "owner",
	"GET /v1/comments/inbox":               "owner",
	"POST /v1/comments/:commentId/heart":   "owner",
	"DELETE /v1/comments/:commentId/heart": "owner",
	"PUT /v1/comments/:commentId/pin":      "owner",
	"DELETE /v1/comments/:commentId/pin":   "owner",
	"POST /v1/uploads/bulk":                "owner",
	"GET /v1/posts/categories":             "public",
	"GET /v1/posts/:postId/comments":       "public",
	"PATCH /v1/videos/:videoId/category":   "owner",
	"PATCH /v1/channels/me":                "owner",
	"GET /v1/channels/:ref":                "public",
	"POST /v1/posts/:postId/tune":          "viewer",
	"DELETE /v1/posts/:postId/tune":        "viewer",
	"POST /v1/posts/:postId/chapters":      "owner",
	"GET /v1/posts/:postId/chapters":       "public",
	"GET /v1/uploads/videos":               "owner",
	"GET /v1/uploads/flicks":               "owner",
	"GET /v1/posts/live-recordings":        "public",
}

func registeredRoutes(t *testing.T) map[string]bool {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handler{}
	h.RegisterRoutes(r)
	h.RegisterMyUploadsRoutes(r)
	out := map[string]bool{}
	for _, info := range r.Routes() {
		out[info.Method+" "+info.Path] = true
	}
	return out
}

func TestMTubeRoutesAreRegistered(t *testing.T) {
	routes := registeredRoutes(t)
	for want := range mtubeRoutes {
		if !routes[want] {
			t.Errorf("%s is not registered", want)
		}
	}
	// The static children beside their parameter siblings resolve as
	// static: "system", "inbox", "bulk" and "me" are never read as ids.
	for _, path := range []string{"/v1/playlists/system/watch_later", "/v1/comments/inbox", "/v1/uploads/bulk", "/v1/posts/me/summary"} {
		method := http.MethodGet
		if path == "/v1/uploads/bulk" {
			method = http.MethodPost
		}
		w := performMTubeRequest(t, method, path, nil, "")
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s with no identity: status=%d want 401 (static segment read as an id?) body=%s", method, path, w.Code, w.Body.String())
		}
	}
}

// Every owner / viewer route refuses an anonymous caller at the handler.
// (The Handler has a nil svc: a request that got past the identity check
// would panic rather than quietly pass.)
func TestMTubeOwnerAndViewerRoutesRequireIdentity(t *testing.T) {
	id := uuid.NewString()
	for route, policy := range mtubeRoutes {
		if policy == "public" {
			continue
		}
		parts := strings.SplitN(route, " ", 2)
		path := strings.NewReplacer(":postId", id, ":commentId", id, ":kind", "watch_later", ":videoId", id).Replace(parts[1])
		t.Run(route, func(t *testing.T) {
			w := performMTubeRequest(t, parts[0], path, nil, `{}`)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d want 401 body=%s", w.Code, w.Body.String())
			}
		})
	}
}

// Validation that precedes the service: a malformed id, an unknown sort, a
// bad system kind, an empty bulk list, a bad inbox filter, links that are
// not https, a non-uuid cover. Each is a 4xx before any store is touched.
func TestMTubeHandlerValidationBeforeService(t *testing.T) {
	me := map[string]string{"X-User-Id": uuid.NewString()}
	id := uuid.NewString()
	cases := []struct {
		name, method, path, body string
		status                   int
		code                     string
	}{
		{"edit: bad post id", http.MethodPatch, "/v1/posts/not-a-uuid", `{"title":"x"}`, 400, "INVALID_ID"},
		{"edit: bad json", http.MethodPatch, "/v1/posts/" + id, `{"title":`, 400, "INVALID_REQUEST"},
		{"edit: cover not a uuid", http.MethodPatch, "/v1/posts/" + id, `{"cover_media_id":"nope"}`, 422, "MEDIA_NOT_FOUND"},
		{"comments: unknown sort", http.MethodGet, "/v1/posts/" + id + "/comments?sort=oldest", "", 400, "INVALID_REQUEST"},
		{"system playlist: unknown kind", http.MethodGet, "/v1/playlists/system/favourites", "", 400, "INVALID_KIND"},
		{"bulk: empty ids", http.MethodPost, "/v1/uploads/bulk", `{"post_ids":[],"patch":{"visibility":"private"}}`, 422, "INVALID_REQUEST"},
		{"bulk: no visibility", http.MethodPost, "/v1/uploads/bulk", `{"post_ids":["` + id + `"],"patch":{}}`, 422, "INVALID_REQUEST"},
		{"bulk: bad id", http.MethodPost, "/v1/uploads/bulk", `{"post_ids":["x"],"patch":{"visibility":"private"}}`, 400, "INVALID_ID"},
		{"inbox: bad status", http.MethodGet, "/v1/comments/inbox?status=read", "", 400, "INVALID_REQUEST"},
		{"inbox: bad content", http.MethodGet, "/v1/comments/inbox?content=stories", "", 400, "INVALID_REQUEST"},
		{"inbox: bad sort", http.MethodGet, "/v1/comments/inbox?sort=oldest", "", 400, "INVALID_REQUEST"},
		{"heart: bad comment id", http.MethodPost, "/v1/comments/nope/heart", "", 400, "INVALID_ID"},
		{"pin: bad comment id", http.MethodPut, "/v1/comments/nope/pin", "", 400, "INVALID_ID"},
		{"watch-later: bad post id", http.MethodPost, "/v1/posts/nope/watch-later", "", 400, "INVALID_ID"},
		{"channel: http link", http.MethodPatch, "/v1/channels/me", `{"links":[{"title":"x","url":"http://a.b"}]}`, 400, "INVALID_LINKS"},
		{"channel: eleven links", http.MethodPatch, "/v1/channels/me", `{"links":[` + strings.Repeat(`{"title":"x","url":"https://a.b"},`, 10) + `{"title":"x","url":"https://a.b"}]}`, 400, "INVALID_LINKS"},
		{"channel: bad email", http.MethodPatch, "/v1/channels/me", `{"contact_email":"nope"}`, 400, "INVALID_CONTACT_EMAIL"},
		{"channel: bad banner id", http.MethodPatch, "/v1/channels/me", `{"banner_media_id":"nope"}`, 400, "INVALID_REQUEST"},
		{"channel: bad featured id", http.MethodPatch, "/v1/channels/me", `{"featured_post_id":"nope"}`, 400, "INVALID_REQUEST"},
		// live-recordings is a static sibling of /:postId: a bad query is the
		// listing's 400, never GetPost's INVALID_ID for the word "live-recordings".
		{"live recordings: zero limit", http.MethodGet, "/v1/posts/live-recordings?limit=0", "", 400, "INVALID_REQUEST"},
		{"live recordings: limit over 50", http.MethodGet, "/v1/posts/live-recordings?limit=51", "", 400, "INVALID_REQUEST"},
		{"live recordings: non-numeric limit", http.MethodGet, "/v1/posts/live-recordings?limit=ten", "", 400, "INVALID_REQUEST"},
		{"live recordings: foreign cursor", http.MethodGet, "/v1/posts/live-recordings?cursor=abc", "", 400, "INVALID_REQUEST"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := performMTubeRequest(t, tc.method, tc.path, me, tc.body)
			if w.Code != tc.status || errorCode(t, w) != tc.code {
				t.Fatalf("status=%d code=%s want %d %s body=%s", w.Code, errorCode(t, w), tc.status, tc.code, w.Body.String())
			}
		})
	}
}

// The error mappers: every sentinel the services return lands on the
// documented status and code. 404 for a post / comment that is not there
// (or not visible), 403 for someone else's, 422 for a value a field cannot
// take, 409 for a write on a server-owned collection.
func TestWritePostEditErrorCodes(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{service.ErrPostNotFound, 404, "NOT_FOUND"},
		{service.ErrPostNotVisible, 404, "NOT_FOUND"},
		{service.ErrNotPostAuthor, 403, "FORBIDDEN"},
		{service.ErrPostForbidden, 403, "FORBIDDEN"},
		{service.ErrMediaNotOwned, 403, "MEDIA_NOT_OWNED"},
		{service.ErrInvalidCategory, 422, "INVALID_CATEGORY"},
		{service.ErrInvalidVisibility, 422, "INVALID_VISIBILITY"},
		{service.ErrTitleTooLong, 422, "TITLE_TOO_LONG"},
		{service.ErrTitleRequired, 422, "TITLE_REQUIRED"},
		{service.ErrTextTooLong, 422, "TEXT_TOO_LONG"},
		{service.ErrEmptyPost, 422, "EMPTY_POST"},
		{service.ErrInvalidHashtag, 422, "INVALID_HASHTAG"},
		{service.ErrTooManyHashtags, 422, "TOO_MANY_HASHTAGS"},
		{service.ErrInvalidLanguage, 422, "INVALID_LANGUAGE"},
		{service.ErrTooManyTags, 422, "INVALID_TAGS"},
		{service.ErrMediaNotFound, 422, "MEDIA_NOT_FOUND"},
		{service.ErrMediaNotReady, 422, "MEDIA_NOT_READY"},
		{service.ErrMediaTypeMismatch, 422, "MEDIA_TYPE_MISMATCH"},
		{service.ErrBulkTooMany, 422, "INVALID_REQUEST"},
		{fmt.Errorf("wrapped: %w", service.ErrNotPostAuthor), 403, "FORBIDDEN"},
		{errors.New("boom"), 500, "INTERNAL_ERROR"},
	}
	for _, tc := range cases {
		t.Run(tc.code+"/"+tc.err.Error(), func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPatch, "/v1/posts/x", nil)
			writePostEditError(c, tc.err)
			if rec.Code != tc.status || errorCode(t, rec) != tc.code {
				t.Fatalf("got %d %s want %d %s", rec.Code, errorCode(t, rec), tc.status, tc.code)
			}
		})
	}
}

func TestWriteVideoAuthoringErrorMapsSystemPlaylist(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{service.ErrSystemPlaylist, 409, "SYSTEM_PLAYLIST"},
		{service.ErrInvalidSystemPlaylistKind, 400, "INVALID_KIND"},
		{service.ErrNotPlaylistOwner, 403, "FORBIDDEN"},
		{service.ErrPlaylistNotFound, 404, "NOT_FOUND"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPatch, "/v1/playlists/x", nil)
		writeVideoAuthoringError(c, tc.err)
		if rec.Code != tc.status || errorCode(t, rec) != tc.code {
			t.Fatalf("%v: got %d %s want %d %s", tc.err, rec.Code, errorCode(t, rec), tc.status, tc.code)
		}
	}
}

func TestWriteCreatorCommentErrorCodes(t *testing.T) {
	h := &Handler{}
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{service.ErrNotPostAuthor, 403, "FORBIDDEN"},
		{service.ErrCannotPinReply, 422, "CANNOT_PIN_REPLY"},
		{errors.New("COMMENT_NOT_FOUND"), 404, "NOT_FOUND"},
		{service.ErrPostNotVisible, 404, "NOT_FOUND"},
		{service.ErrInvalidInboxContent, 400, "INVALID_REQUEST"},
		{errors.New("boom"), 500, "INTERNAL_ERROR"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPut, "/v1/comments/x/pin", nil)
		h.writeCreatorCommentError(c, tc.err)
		if rec.Code != tc.status || errorCode(t, rec) != tc.code {
			t.Fatalf("%v: got %d %s want %d %s", tc.err, rec.Code, errorCode(t, rec), tc.status, tc.code)
		}
	}
}

func TestWriteChannelErrorMapsBranding(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{service.ErrInvalidChannelLinks, 400, "INVALID_LINKS"},
		{service.ErrInvalidContactEmail, 400, "INVALID_CONTACT_EMAIL"},
		{service.ErrMediaNotOwned, 403, "MEDIA_NOT_OWNED"},
		{service.ErrMediaNotFound, 400, "MEDIA_NOT_FOUND"},
		{service.ErrMediaTypeMismatch, 400, "MEDIA_TYPE_MISMATCH"},
		{service.ErrFeaturedPostNotFound, 404, "NOT_FOUND"},
		{service.ErrFeaturedPostNotOwned, 403, "FORBIDDEN"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPatch, "/v1/channels/me", nil)
		if !writeChannelError(c, tc.err) {
			t.Fatalf("%v not handled", tc.err)
		}
		if rec.Code != tc.status || errorCode(t, rec) != tc.code {
			t.Fatalf("%v: got %d %s want %d %s", tc.err, rec.Code, errorCode(t, rec), tc.status, tc.code)
		}
	}
}

// The create guard's INVALID_CATEGORY is a 422 now (the code is unchanged).
func TestCreateGuardInvalidCategoryIs422(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/posts", nil)
	if !writeCreateGuardError(c, service.ErrInvalidCategory) {
		t.Fatal("not handled")
	}
	if rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != "INVALID_CATEGORY" {
		t.Fatalf("got %d %s", rec.Code, errorCode(t, rec))
	}
}

func performMTubeRequest(t *testing.T, method, path string, headers map[string]string, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handler{}
	h.RegisterRoutes(r)
	h.RegisterMyUploadsRoutes(r)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
