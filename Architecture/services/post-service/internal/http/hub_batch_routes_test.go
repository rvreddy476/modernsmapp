package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Creator Hub batch (2026-09-28), driven through the REAL router: 401 / 403 /
// 404 / 422 for every new route and every new field, with in-memory stores
// behind a service built by service.NewForHandlerTests.

// ── in-memory stores ────────────────────────────────────────────────────────

type hubEditStore struct {
	posts     map[uuid.UUID]*postgres.Post
	failWrite map[uuid.UUID]bool
	writes    []uuid.UUID
}

func newHubEditStore() *hubEditStore {
	return &hubEditStore{posts: map[uuid.UUID]*postgres.Post{}, failWrite: map[uuid.UUID]bool{}}
}

func (f *hubEditStore) GetPost(_ context.Context, id uuid.UUID) (*postgres.Post, error) {
	p, ok := f.posts[id]
	if !ok {
		return nil, nil
	}
	cp := *p
	return &cp, nil
}

func (f *hubEditStore) UpdatePostFields(_ context.Context, postID, actorID uuid.UUID, patch postgres.PostEditPatch) (*postgres.Post, error) {
	p, ok := f.posts[postID]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	if p.AuthorID != actorID {
		return nil, postgres.ErrPostEditNotOwned
	}
	if f.failWrite[postID] {
		return nil, errors.New("connection reset")
	}
	f.writes = append(f.writes, postID)
	if patch.AgeRestricted != nil {
		p.AgeRestricted = *patch.AgeRestricted
	}
	if patch.Visibility != nil {
		p.Visibility = *patch.Visibility
	}
	if patch.DefaultCommentSort != nil {
		p.DefaultCommentSort = *patch.DefaultCommentSort
	}
	if patch.Category != nil {
		p.Category = *patch.Category
	}
	cp := *p
	return &cp, nil
}

func (f *hubEditStore) BatchGetMediaOwnership(context.Context, []uuid.UUID) (map[uuid.UUID]postgres.MediaOwnership, error) {
	return map[uuid.UUID]postgres.MediaOwnership{}, nil
}

func (f *hubEditStore) PostAuthorsByIDs(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]uuid.UUID, error) {
	out := map[uuid.UUID]uuid.UUID{}
	for _, id := range ids {
		if p, ok := f.posts[id]; ok {
			out[id] = p.AuthorID
		}
	}
	return out, nil
}

func (f *hubEditStore) CountCreatorContent(context.Context, uuid.UUID) (postgres.CreatorCounts, error) {
	return postgres.CreatorCounts{}, nil
}

func (f *hubEditStore) UpdatePostCategory(context.Context, uuid.UUID, string) error { return nil }

type hubShareStore struct {
	lists map[uuid.UUID][]postgres.PrivateShare
}

func (f *hubShareStore) ListPrivateShares(_ context.Context, postID uuid.UUID) ([]postgres.PrivateShare, error) {
	return f.lists[postID], nil
}

func (f *hubShareStore) ReplacePrivateShares(_ context.Context, postID, _ uuid.UUID, ids []uuid.UUID) ([]postgres.PrivateShare, error) {
	rows := []postgres.PrivateShare{}
	for _, id := range ids {
		rows = append(rows, postgres.PrivateShare{UserID: id, AddedAt: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)})
	}
	f.lists[postID] = rows
	return rows, nil
}

func (f *hubShareStore) PrivateSharedPostIDs(_ context.Context, viewer uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	for _, id := range ids {
		for _, sh := range f.lists[id] {
			if sh.UserID == viewer {
				out[id] = true
			}
		}
	}
	return out, nil
}

type hubAuthoringStore struct{ meta *postgres.VideoMetadata }

func (f *hubAuthoringStore) GetPostAuthorID(context.Context, uuid.UUID) (uuid.UUID, error) {
	return uuid.Nil, nil
}
func (f *hubAuthoringStore) GetPlaylist(context.Context, uuid.UUID) (*postgres.Playlist, error) {
	return nil, nil
}
func (f *hubAuthoringStore) ListPlaylistsByCreator(context.Context, uuid.UUID, bool, int, int) ([]postgres.Playlist, error) {
	return nil, nil
}
func (f *hubAuthoringStore) GetPlaylistItems(context.Context, uuid.UUID) ([]postgres.PlaylistItem, error) {
	return nil, nil
}
func (f *hubAuthoringStore) GetVideoMetadata(context.Context, uuid.UUID) (*postgres.VideoMetadata, error) {
	return f.meta, nil
}

// ── rig ─────────────────────────────────────────────────────────────────────

type hubRig struct {
	owner, viewer, shared uuid.UUID
	post, privatePost     *postgres.Post
	edits                 *hubEditStore
	shares                *hubShareStore
	router                *gin.Engine
}

func newHubRouteRig(t *testing.T, extra func(*service.HandlerTestDeps)) *hubRig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := &hubRig{owner: uuid.New(), viewer: uuid.New(), shared: uuid.New(), edits: newHubEditStore(),
		shares: &hubShareStore{lists: map[uuid.UUID][]postgres.PrivateShare{}}}
	r.post = &postgres.Post{ID: uuid.New(), AuthorID: r.owner, ContentType: "long_video", Visibility: "public", Title: "One"}
	r.privatePost = &postgres.Post{ID: uuid.New(), AuthorID: r.owner, ContentType: "long_video", Visibility: "private", Title: "Two"}
	r.edits.posts[r.post.ID] = r.post
	r.edits.posts[r.privatePost.ID] = r.privatePost
	r.shares.lists[r.privatePost.ID] = []postgres.PrivateShare{{UserID: r.shared}}
	profiles := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			r.shared.String(): map[string]any{"username": "call.b", "display_name": "Call B"},
			r.viewer.String(): map[string]any{"username": "call.c", "display_name": "Call C"},
		})
	}))
	t.Cleanup(profiles.Close)
	deps := service.HandlerTestDeps{PostEdits: r.edits, PrivateShares: r.shares, ProfileServiceURL: profiles.URL,
		Now: func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) }}
	if extra != nil {
		extra(&deps)
	}
	r.router = gin.New()
	h := New(service.NewForHandlerTests(deps), nil)
	h.RegisterRoutes(r.router)
	h.RegisterMyUploadsRoutes(r.router)
	return r
}

func (r *hubRig) do(t *testing.T, method, path string, caller *uuid.UUID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if caller != nil {
		req.Header.Set("X-User-Id", caller.String())
	}
	w := httptest.NewRecorder()
	r.router.ServeHTTP(w, req)
	return w
}

func expectCode(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status || errorCode(t, w) != code {
		t.Fatalf("status=%d code=%s want %d %s body=%s", w.Code, errorCode(t, w), status, code, w.Body.String())
	}
}

// ── routes and identity ─────────────────────────────────────────────────────

func TestHubBatchRoutesRegisteredAndIdentityRequired(t *testing.T) {
	routes := registeredRoutes(t)
	id := uuid.NewString()
	for route, path := range map[string]string{
		"POST /v1/uploads/bulk-delete":         "/v1/uploads/bulk-delete",
		"GET /v1/posts/:postId/private-shares": "/v1/posts/" + id + "/private-shares",
		"PUT /v1/posts/:postId/private-shares": "/v1/posts/" + id + "/private-shares",
		"PATCH /v1/posts/:postId":              "/v1/posts/" + id,
		"POST /v1/uploads/bulk":                "/v1/uploads/bulk",
	} {
		if !routes[route] {
			t.Errorf("%s is not registered", route)
			continue
		}
		method := strings.SplitN(route, " ", 2)[0]
		w := performMTubeRequest(t, method, path, nil, `{}`)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s with no identity: %d %s", route, w.Code, w.Body.String())
		}
	}
}

// ── A. PATCH ────────────────────────────────────────────────────────────────

func TestHubPatchRouteStatuses(t *testing.T) {
	r := newHubRouteRig(t, nil)
	stranger := uuid.New()
	path := "/v1/posts/" + r.post.ID.String()
	expectCode(t, r.do(t, http.MethodPatch, path, &stranger, `{"age_restricted":true}`), 403, "FORBIDDEN")
	expectCode(t, r.do(t, http.MethodPatch, "/v1/posts/"+uuid.NewString(), &r.owner, `{"age_restricted":true}`), 404, "NOT_FOUND")
	expectCode(t, r.do(t, http.MethodPatch, path, &r.owner, `{"age_restricted":"yes"}`), 400, "INVALID_REQUEST")
	for _, tc := range []struct{ body, code string }{
		{`{"license":"cc0"}`, "INVALID_LICENSE"},
		{`{"recording_date":"2026-13-01"}`, "INVALID_RECORDING_DATE"},
		{`{"recording_date":"2026-10-01"}`, "INVALID_RECORDING_DATE"},
		{`{"recording_location":"` + strings.Repeat("x", 101) + `"}`, "INVALID_RECORDING_LOCATION"},
		{`{"remix_setting":"maybe"}`, "INVALID_REMIX_SETTING"},
		{`{"comment_moderation":"loose"}`, "INVALID_COMMENT_MODERATION"},
		{`{"comment_access":"friends"}`, "INVALID_COMMENT_ACCESS"},
		{`{"default_comment_sort":"oldest"}`, "INVALID_COMMENT_SORT"},
		{`{"related_post_id":"` + uuid.NewString() + `"}`, "RELATED_NOT_FOUND"},
		{`{"related_post_id":"` + r.post.ID.String() + `"}`, "RELATED_SELF"},
	} {
		expectCode(t, r.do(t, http.MethodPatch, path, &r.owner, tc.body), 422, tc.code)
	}
	if len(r.edits.writes) != 0 {
		t.Fatalf("refused PATCHes reached the store: %v", r.edits.writes)
	}
	w := r.do(t, http.MethodPatch, path, &r.owner, `{"age_restricted":true,"default_comment_sort":"newest","related_post_id":"`+r.privatePost.ID.String()+`","notify_subscribers":false,"hide_like_count":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("valid PATCH: %d %s", w.Code, w.Body.String())
	}
	var env struct {
		Data map[string]any `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if env.Data["age_restricted"] != true || env.Data["default_comment_sort"] != "newest" {
		t.Fatalf("response: %s", w.Body.String())
	}
}

// ── C. bulk ─────────────────────────────────────────────────────────────────

func TestHubBulkRouteStatusesAndIsolation(t *testing.T) {
	r := newHubRouteRig(t, nil)
	flick := &postgres.Post{ID: uuid.New(), AuthorID: r.owner, ContentType: "flick", Visibility: "public"}
	broken := &postgres.Post{ID: uuid.New(), AuthorID: r.owner, ContentType: "long_video", Visibility: "public"}
	r.edits.posts[flick.ID], r.edits.posts[broken.ID] = flick, broken
	r.edits.failWrite[broken.ID] = true
	foreign := &postgres.Post{ID: uuid.New(), AuthorID: uuid.New(), ContentType: "long_video", Visibility: "public"}
	r.edits.posts[foreign.ID] = foreign
	ids := func(list ...uuid.UUID) string {
		s := make([]string, len(list))
		for i, id := range list {
			s[i] = `"` + id.String() + `"`
		}
		return "[" + strings.Join(s, ",") + "]"
	}
	one := ids(r.post.ID)
	for _, tc := range []struct{ body, code string }{
		{`{"post_ids":` + one + `,"patch":{}}`, "INVALID_REQUEST"},
		{`{"post_ids":` + one + `,"patch":{"title":"x","age_restricted":true}}`, "INVALID_REQUEST"},
		{`{"post_ids":` + one + `,"patch":{"text":"x"}}`, "INVALID_REQUEST"},
		{`{"post_ids":` + one + `,"patch":{"tags":["a"],"tags_mode":"merge"}}`, "INVALID_REQUEST"},
		{`{"post_ids":` + one + `,"patch":{"license":"cc0"}}`, "INVALID_LICENSE"},
		{`{"post_ids":` + one + `,"patch":{"comment_access":"friends"}}`, "INVALID_COMMENT_ACCESS"},
		{`{"post_ids":` + one + `,"patch":{"comment_moderation":"x"}}`, "INVALID_COMMENT_MODERATION"},
		{`{"post_ids":` + one + `,"patch":{"remix_setting":"x"}}`, "INVALID_REMIX_SETTING"},
		{`{"post_ids":` + one + `,"patch":{"default_comment_sort":"x"}}`, "INVALID_COMMENT_SORT"},
		{`{"post_ids":` + one + `,"patch":{"recording_date":"2030-01-01"}}`, "INVALID_RECORDING_DATE"},
		{`{"post_ids":` + one + `,"patch":{"visibility":"staged"}}`, "INVALID_VISIBILITY"},
	} {
		expectCode(t, r.do(t, http.MethodPost, "/v1/uploads/bulk", &r.owner, tc.body), 422, tc.code)
	}
	expectCode(t, r.do(t, http.MethodPost, "/v1/uploads/bulk", &r.owner, `{"post_ids":`+ids(r.post.ID, foreign.ID)+`,"patch":{"age_restricted":true}}`), 403, "FORBIDDEN")
	if len(r.edits.writes) != 0 {
		t.Fatalf("refused bulks reached the store: %v", r.edits.writes)
	}
	w := r.do(t, http.MethodPost, "/v1/uploads/bulk", &r.owner, `{"post_ids":`+ids(r.post.ID, flick.ID, broken.ID)+`,"patch":{"category":"podcasts","age_restricted":true}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("bulk: %d %s", w.Code, w.Body.String())
	}
	var env struct {
		Data bulkUploadsResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	got := env.Data.Results
	if len(got) != 3 || !got[0].OK || got[1].OK || got[1].Error != "INVALID_CATEGORY" || got[2].OK || got[2].Error != "INTERNAL_ERROR" {
		t.Fatalf("outcomes: %+v", got)
	}
	if !r.post.AgeRestricted || flick.AgeRestricted {
		t.Fatal("the good post was rolled back, or the refused one was written")
	}
}

// ── D. bulk delete ──────────────────────────────────────────────────────────

func TestHubBulkDeleteRouteStatuses(t *testing.T) {
	gone := uuid.New()
	r := newHubRouteRig(t, func(d *service.HandlerTestDeps) {
		d.BulkDelete = func(_ context.Context, id, _ uuid.UUID) error {
			if id == gone {
				return service.ErrPostNotFound
			}
			return nil
		}
	})
	expectCode(t, r.do(t, http.MethodPost, "/v1/uploads/bulk-delete", &r.owner, `{"post_ids":[]}`), 422, "INVALID_REQUEST")
	expectCode(t, r.do(t, http.MethodPost, "/v1/uploads/bulk-delete", &r.owner, `{"post_ids":["nope"]}`), 400, "INVALID_ID")
	many := make([]string, service.MaxBulkPostIDs+1)
	for i := range many {
		many[i] = `"` + uuid.NewString() + `"`
	}
	expectCode(t, r.do(t, http.MethodPost, "/v1/uploads/bulk-delete", &r.owner, `{"post_ids":[`+strings.Join(many, ",")+`]}`), 422, "INVALID_REQUEST")
	w := r.do(t, http.MethodPost, "/v1/uploads/bulk-delete", &r.owner, `{"post_ids":["`+r.post.ID.String()+`","`+gone.String()+`"]}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"error":"NOT_FOUND"`) || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("bulk delete: %d %s", w.Code, w.Body.String())
	}
}

// ── F. private shares ───────────────────────────────────────────────────────

func TestHubPrivateSharesRouteStatuses(t *testing.T) {
	r := newHubRouteRig(t, nil)
	stranger := uuid.New()
	path := "/v1/posts/" + r.privatePost.ID.String() + "/private-shares"
	// 404: a private post the caller is not on the list of; a missing post.
	expectCode(t, r.do(t, http.MethodGet, path, &stranger, ""), 404, "NOT_FOUND")
	expectCode(t, r.do(t, http.MethodPut, path, &stranger, `{"user_ids":[]}`), 404, "NOT_FOUND")
	expectCode(t, r.do(t, http.MethodGet, "/v1/posts/"+uuid.NewString()+"/private-shares", &r.owner, ""), 404, "NOT_FOUND")
	// 403: a viewer on the list sees the post but never the list.
	expectCode(t, r.do(t, http.MethodGet, path, &r.shared, ""), 403, "FORBIDDEN")
	expectCode(t, r.do(t, http.MethodPut, path, &r.shared, `{"user_ids":[]}`), 403, "FORBIDDEN")
	// 400 / 422.
	expectCode(t, r.do(t, http.MethodGet, "/v1/posts/nope/private-shares", &r.owner, ""), 400, "INVALID_ID")
	expectCode(t, r.do(t, http.MethodPut, path, &r.owner, `{}`), 400, "INVALID_REQUEST")
	expectCode(t, r.do(t, http.MethodPut, path, &r.owner, `{"user_ids":["nope"]}`), 422, "INVALID_USER")
	expectCode(t, r.do(t, http.MethodPut, path, &r.owner, `{"user_ids":["`+r.owner.String()+`"]}`), 422, "INVALID_USER")
	expectCode(t, r.do(t, http.MethodPut, path, &r.owner, `{"user_ids":["`+uuid.NewString()+`"]}`), 422, "INVALID_USER")
	many := make([]string, service.MaxPrivateShares+1)
	for i := range many {
		many[i] = `"` + uuid.NewString() + `"`
	}
	expectCode(t, r.do(t, http.MethodPut, path, &r.owner, `{"user_ids":[`+strings.Join(many, ",")+`]}`), 422, "TOO_MANY_SHARES")
	// 200: the owner reads and replaces the list.
	w := r.do(t, http.MethodPut, path, &r.owner, `{"user_ids":["`+r.viewer.String()+`","`+r.shared.String()+`","`+r.viewer.String()+`"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body.String())
	}
	var env struct {
		Data service.PrivateSharesView `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil || len(env.Data.Users) != 2 || env.Data.Users[0].Username != "call.c" {
		t.Fatalf("put body: %s", w.Body.String())
	}
	w = r.do(t, http.MethodGet, path, &r.owner, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"users":[`) {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}
}

// ── B. read gates: video detail and comments answer the age codes ──────────

func TestHubReadGateStatusesOnVideoDetailAndComments(t *testing.T) {
	for _, tc := range []struct {
		refusal error
		status  int
		code    string
	}{
		{service.ErrAgeSignIn, 401, "AGE_RESTRICTED_SIGN_IN"},
		{service.ErrAgeRestricted, 403, "AGE_RESTRICTED"},
		{service.ErrAgeUnverified, 403, "AGE_UNVERIFIED"},
		{service.ErrPostNotVisible, 404, "NOT_FOUND"},
	} {
		refusal := tc.refusal
		r := newHubRouteRig(t, func(d *service.HandlerTestDeps) {
			d.Authoring = &hubAuthoringStore{meta: &postgres.VideoMetadata{PlaybackURL: strp("https://cdn/x.m3u8")}}
			d.ReadGate = func(context.Context, uuid.UUID, *uuid.UUID) error { return refusal }
		})
		id := uuid.NewString()
		expectCode(t, r.do(t, http.MethodGet, "/v1/videos/"+id, nil, ""), tc.status, tc.code)
		expectCode(t, r.do(t, http.MethodGet, "/v1/posts/"+id+"/comments", &r.viewer, ""), tc.status, tc.code)
		expectCode(t, r.do(t, http.MethodGet, "/v1/posts/"+id+"/comments/around/"+uuid.NewString(), nil, ""), tc.status, tc.code)
	}
	// A readable post still answers the metadata.
	r := newHubRouteRig(t, func(d *service.HandlerTestDeps) {
		d.Authoring = &hubAuthoringStore{meta: &postgres.VideoMetadata{PlaybackURL: strp("https://cdn/x.m3u8")}}
		d.ReadGate = func(context.Context, uuid.UUID, *uuid.UUID) error { return nil }
	})
	if w := r.do(t, http.MethodGet, "/v1/videos/"+uuid.NewString(), nil, ""); w.Code != http.StatusOK {
		t.Fatalf("readable video detail: %d %s", w.Code, w.Body.String())
	}
}

// Every new code the mapper knows lands on the documented status.
func TestHubBatchErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{service.ErrInvalidLicense, 422, "INVALID_LICENSE"},
		{service.ErrInvalidRecordingDate, 422, "INVALID_RECORDING_DATE"},
		{service.ErrInvalidRecordingLocation, 422, "INVALID_RECORDING_LOCATION"},
		{service.ErrInvalidRemixSetting, 422, "INVALID_REMIX_SETTING"},
		{service.ErrInvalidCommentModeration, 422, "INVALID_COMMENT_MODERATION"},
		{service.ErrInvalidCommentAccess, 422, "INVALID_COMMENT_ACCESS"},
		{service.ErrInvalidCommentSort, 422, "INVALID_COMMENT_SORT"},
		{service.ErrRelatedNotFound, 422, "RELATED_NOT_FOUND"},
		{service.ErrRelatedSelf, 422, "RELATED_SELF"},
		{service.ErrTooManyShares, 422, "TOO_MANY_SHARES"},
		{service.ErrInvalidShareUser, 422, "INVALID_USER"},
		{service.ErrBulkEmptyPatch, 422, "INVALID_REQUEST"},
		{service.ErrBulkTagsMode, 422, "INVALID_REQUEST"},
		{service.ErrShareUsersUnknown, 503, "PROFILES_UNAVAILABLE"},
		{service.ErrAgeSignIn, 401, "AGE_RESTRICTED_SIGN_IN"},
		{service.ErrAgeRestricted, 403, "AGE_RESTRICTED"},
		{service.ErrAgeUnverified, 403, "AGE_UNVERIFIED"},
	} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPatch, "/v1/posts/x", nil)
		writePostEditError(c, tc.err)
		if rec.Code != tc.status || errorCode(t, rec) != tc.code {
			t.Fatalf("%v: got %d %s want %d %s", tc.err, rec.Code, errorCode(t, rec), tc.status, tc.code)
		}
	}
}

// writeReadGateError is what GET /v1/posts/:postId answers its refusals
// with (the success path needs Scylla, so the route itself is covered by
// the service integration test; this pins the mapping).
func TestWriteReadGateErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{service.ErrAgeSignIn, 401, "AGE_RESTRICTED_SIGN_IN"},
		{service.ErrAgeRestricted, 403, "AGE_RESTRICTED"},
		{service.ErrAgeUnverified, 403, "AGE_UNVERIFIED"},
		{service.ErrPostNotVisible, 404, "NOT_FOUND"},
		{service.ErrPostNotFound, 404, "NOT_FOUND"},
	} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/posts/x", nil)
		if !writeReadGateError(c, tc.err) || rec.Code != tc.status || errorCode(t, rec) != tc.code {
			t.Fatalf("%v: got %d %s want %d %s", tc.err, rec.Code, errorCode(t, rec), tc.status, tc.code)
		}
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/posts/x", nil)
	if writeReadGateError(c, errors.New("boom")) {
		t.Fatal("an unrelated error was answered as a read-gate refusal")
	}
}
