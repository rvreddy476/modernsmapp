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
)

// The copyright title review routes (copyright_review.go): the reviewer
// routes behind admin-service tokens with the decision narrowing the
// permission, the author's request route, and the two publication
// refusals on the wire.

type routeTitleStore struct {
	review  *postgres.TitleReview
	decided []postgres.DecideTitleReviewInput
	created []postgres.CopyrightTitleWrite
	reqNote string
	// Track removals (migration 076).
	removed    [][3]uuid.UUID
	removeNote string
	noTrack    bool
	// The owner-match route.
	title     *postgres.CopyrightTitle
	matchPost uuid.UUID
	matches   []postgres.OwnerMatchInput
}

func (f *routeTitleStore) ListActiveCopyrightTitles(context.Context) ([]postgres.CopyrightTitle, error) {
	return nil, nil
}
func (f *routeTitleStore) ListCopyrightTitles(context.Context, postgres.CopyrightTitleFilter) ([]postgres.CopyrightTitle, string, error) {
	return []postgres.CopyrightTitle{}, "", nil
}
func (f *routeTitleStore) GetCopyrightTitle(_ context.Context, id uuid.UUID) (*postgres.CopyrightTitle, error) {
	if f.title != nil && f.title.ID == id {
		t := *f.title
		return &t, nil
	}
	return nil, postgres.ErrCopyrightTitleNotFound
}
func (f *routeTitleStore) CreateCopyrightTitle(_ context.Context, id uuid.UUID, w postgres.CopyrightTitleWrite, actor uuid.UUID) (*postgres.CopyrightTitle, error) {
	f.created = append(f.created, w)
	return &postgres.CopyrightTitle{ID: id, Title: w.Title, Aliases: w.Aliases, OwnerChannelIDs: w.OwnerChannelIDs, Active: w.Active, CreatedBy: actor}, nil
}
func (f *routeTitleStore) UpdateCopyrightTitle(context.Context, uuid.UUID, postgres.CopyrightTitleWrite, uuid.UUID, string) (*postgres.CopyrightTitle, error) {
	return nil, postgres.ErrCopyrightTitleNotFound
}
func (f *routeTitleStore) ListTitleReviews(context.Context, string, string, int) ([]postgres.TitleReviewQueueItem, string, error) {
	return []postgres.TitleReviewQueueItem{}, "", nil
}
func (f *routeTitleStore) GetTitleReview(context.Context, uuid.UUID) (*postgres.TitleReview, error) {
	if f.review == nil {
		return nil, postgres.ErrTitleReviewNotFound
	}
	return f.review, nil
}
func (f *routeTitleStore) DecideTitleReview(_ context.Context, in postgres.DecideTitleReviewInput, _ postgres.TitleReviewAnnounce) (*postgres.TitleReviewOutcome, error) {
	f.decided = append(f.decided, in)
	r := *f.review
	return &postgres.TitleReviewOutcome{Review: &r}, nil
}
func (f *routeTitleStore) ResolveTitleReviewDuration(context.Context, uuid.UUID, int, bool, bool, string, postgres.TitleReviewAnnounce) (postgres.TitleReviewDurationOutcome, error) {
	return postgres.TitleReviewDurationNone, nil
}
func (f *routeTitleStore) CopyrightMatchPostAuthor(_ context.Context, postID uuid.UUID) (uuid.UUID, error) {
	if f.matchPost != uuid.Nil && postID == f.matchPost {
		return uuid.New(), nil
	}
	return uuid.Nil, postgres.ErrPostNotFound
}
func (f *routeTitleStore) ApplyOwnerMatch(_ context.Context, in postgres.OwnerMatchInput) (*postgres.OwnerMatchResult, error) {
	if f.title == nil {
		return nil, errors.New("not used by the routes")
	}
	f.matches = append(f.matches, in)
	id := uuid.New()
	return &postgres.OwnerMatchResult{Outcome: postgres.OwnerMatchBlocked, ReviewID: &id}, nil
}
func (f *routeTitleStore) RemoveCopyrightOwnerTrack(_ context.Context, postID, titleID, actor uuid.UUID, note string) (*postgres.OwnerTrackRemoval, error) {
	f.removed = append(f.removed, [3]uuid.UUID{postID, titleID, actor})
	f.removeNote = note
	if f.noTrack {
		return nil, postgres.ErrOwnerTrackNotFound
	}
	return &postgres.OwnerTrackRemoval{PostID: postID, TitleID: titleID}, nil
}
func (f *routeTitleStore) RequestTitleReview(_ context.Context, _, _ uuid.UUID, note string) (*postgres.TitleReview, error) {
	f.reqNote = note
	if f.review == nil {
		return nil, postgres.ErrTitleReviewNotFound
	}
	if f.review.AppealedAt != nil {
		return nil, postgres.ErrTitleReviewAppealed
	}
	if f.review.State == postgres.TitleReviewBlocked {
		now := time.Now()
		r := *f.review
		r.State, r.AppealedAt, r.AuthorRequestedAt, r.DecidedAt = postgres.TitleReviewPending, &now, &now, nil
		return &r, nil
	}
	if f.review.State != postgres.TitleReviewPending {
		return nil, postgres.ErrTitleReviewDecided
	}
	now := time.Now()
	r := *f.review
	r.AuthorRequestedAt = &now
	return &r, nil
}
func (f *routeTitleStore) TitleReviewsByPost(context.Context, []uuid.UUID) (map[uuid.UUID]*postgres.TitleReview, error) {
	return map[uuid.UUID]*postgres.TitleReview{}, nil
}

func copyrightRouter(t *testing.T, store *routeTitleStore) (*gin.Engine, *adminTokenRig) {
	t.Helper()
	rg := newAdminTokenRig(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	svc := service.NewForHandlerTests(service.HandlerTestDeps{TitleReviews: store})
	New(svc, nil).WithInternalKey(adminTestKey).WithServiceAuth(rg.v).RegisterRoutes(r)
	return r, rg
}

func TestCopyrightDecision_BlockNeedsVideosRemove(t *testing.T) {
	store := &routeTitleStore{review: &postgres.TitleReview{ID: uuid.New(), PostID: uuid.New(), State: postgres.TitleReviewPending}}
	r, rg := copyrightRouter(t, store)
	path := InternalAdminPrefix + "/copyright/reviews/" + store.review.ID.String() + "/decision"
	moderate := rg.mint(t, rg.admin, AudiencePost, []string{PermVideosModerate}, rg.actor.String())

	w := adminServe(r, http.MethodPost, path, `{"decision":"block","note":"pirated"}`, bearer(moderate))
	if w.Code != http.StatusForbidden || adminErrorCode(t, w) != CodeAdminPermissionScope {
		t.Fatalf("block with moderate only: %d %s", w.Code, w.Body.String())
	}
	if len(store.decided) != 0 {
		t.Fatal("a refused block reached the store")
	}
	remove := rg.mint(t, rg.admin, AudiencePost, []string{PermVideosRemove}, rg.actor.String())
	if w := adminServe(r, http.MethodPost, path, `{"decision":"block","note":"pirated"}`, bearer(remove)); w.Code != http.StatusOK {
		t.Fatalf("block with remove: %d %s", w.Code, w.Body.String())
	}
	if w := adminServe(r, http.MethodPost, path, `{"decision":"clear"}`, bearer(moderate)); w.Code != http.StatusOK {
		t.Fatalf("clear with moderate: %d %s", w.Code, w.Body.String())
	}
	if len(store.decided) != 2 || store.decided[0].Decision != "block" || store.decided[0].ActorID != rg.actor || store.decided[1].Decision != "clear" {
		t.Fatalf("decided = %+v", store.decided)
	}
	if w := adminServe(r, http.MethodPost, path, `{"decision":"approve"}`, bearer(remove)); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown decision: %d", w.Code)
	}
	// A reels or posts permission never reaches a Tube decision.
	reels := rg.mint(t, rg.admin, AudiencePost, []string{PermReelsRemove, PermPostsRemove}, rg.actor.String())
	if w := adminServe(r, http.MethodPost, path, `{"decision":"clear"}`, bearer(reels)); w.Code != http.StatusForbidden {
		t.Fatalf("reels permission decided a video: %d", w.Code)
	}
}

func TestCopyrightTitles_CreateValidatesAndNeedsRemove(t *testing.T) {
	store := &routeTitleStore{}
	r, rg := copyrightRouter(t, store)
	path := InternalAdminPrefix + "/copyright/titles"
	moderate := rg.mint(t, rg.admin, AudiencePost, []string{PermVideosModerate}, rg.actor.String())
	remove := rg.mint(t, rg.admin, AudiencePost, []string{PermVideosRemove}, rg.actor.String())
	body := `{"title":"Jailer 2","aliases":["Jailer II"],"language":"ta","owner_channel_ids":["` + uuid.NewString() + `"]}`

	if w := adminServe(r, http.MethodPost, path, body, bearer(moderate)); w.Code != http.StatusForbidden {
		t.Fatalf("create with moderate: %d", w.Code)
	}
	w := adminServe(r, http.MethodPost, path, body, bearer(remove))
	if w.Code != http.StatusCreated || len(store.created) != 1 || !store.created[0].Active || *store.created[0].Language != "ta" {
		t.Fatalf("create: %d %s %+v", w.Code, w.Body.String(), store.created)
	}
	for name, bad := range map[string]string{
		"one letter":    `{"title":"M"}`,
		"unknown field": `{"title":"Jailer","owner":"x"}`,
		"bad owner":     `{"title":"Jailer","owner_channel_ids":["nope"]}`,
		"bad language":  `{"title":"Jailer","language":"english!"}`,
	} {
		if w := adminServe(r, http.MethodPost, path, bad, bearer(remove)); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if len(store.created) != 1 {
		t.Fatalf("an invalid title was written: %+v", store.created)
	}
	if w := adminServe(r, http.MethodGet, InternalAdminPrefix+"/copyright/reviews?state=nope", "", bearer(moderate)); w.Code != http.StatusBadRequest {
		t.Fatalf("bad queue state: %d", w.Code)
	}
	if w := adminServe(r, http.MethodGet, InternalAdminPrefix+"/copyright/reviews", "", bearer(moderate)); w.Code != http.StatusOK {
		t.Fatalf("queue: %d %s", w.Code, w.Body.String())
	}
}

// auto_block and rights_owner ride the create body; an automatic block
// without a named owner is refused before the store.
func TestCopyrightTitles_AutoBlockNeedsRightsOwner(t *testing.T) {
	store := &routeTitleStore{}
	r, rg := copyrightRouter(t, store)
	path := InternalAdminPrefix + "/copyright/titles"
	remove := rg.mint(t, rg.admin, AudiencePost, []string{PermVideosRemove}, rg.actor.String())

	for name, bad := range map[string]string{
		"no owner":         `{"title":"Jailer 2","auto_block":true}`,
		"blank owner":      `{"title":"Jailer 2","auto_block":true,"rights_owner":"   "}`,
		"null owner":       `{"title":"Jailer 2","auto_block":true,"rights_owner":null}`,
		"owner not string": `{"title":"Jailer 2","auto_block":true,"rights_owner":7}`,
		"control in owner": `{"title":"Jailer 2","auto_block":true,"rights_owner":"Sun\u0000Pictures"}`,
	} {
		if w := adminServe(r, http.MethodPost, path, bad, bearer(remove)); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if len(store.created) != 0 {
		t.Fatalf("an owner-less automatic block was written: %+v", store.created)
	}
	w := adminServe(r, http.MethodPost, path, `{"title":"Jailer 2","auto_block":true,"rights_owner":"  Sun   Pictures "}`, bearer(remove))
	if w.Code != http.StatusCreated || len(store.created) != 1 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if c := store.created[0]; !c.AutoBlock || c.RightsOwner == nil || *c.RightsOwner != "Sun Pictures" {
		t.Fatalf("written = %+v, want auto_block with the owner named", c)
	}
	// Default: not an automatic block.
	if w := adminServe(r, http.MethodPost, path, `{"title":"Leo"}`, bearer(remove)); w.Code != http.StatusCreated || store.created[1].AutoBlock {
		t.Fatalf("default entry: %d %+v", w.Code, store.created[1])
	}
}

func TestCopyrightReviewRequest_AuthorRoute(t *testing.T) {
	store := &routeTitleStore{review: &postgres.TitleReview{ID: uuid.New(), PostID: uuid.New(), State: postgres.TitleReviewPending, MatchedTitle: "Jailer 2", AuthorNotified: true}}
	r, _ := copyrightRouter(t, store)
	path := "/v1/posts/" + store.review.PostID.String() + "/copyright-review/request"
	hdr := map[string]string{"X-Internal-Service-Key": adminTestKey, "X-User-Id": uuid.NewString()}

	w := adminServe(r, http.MethodPost, path, `{"note":"I own the rights; licence attached"}`, hdr)
	if w.Code != http.StatusOK || store.reqNote != "I own the rights; licence attached" {
		t.Fatalf("request: %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Data map[string]any `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Data["state"] != "pending" || body.Data["matched_title"] != "Jailer 2" || body.Data["review_requested_at"] == nil {
		t.Fatalf("view = %v", body.Data)
	}
	if w := adminServe(r, http.MethodPost, path, `{"note":"  "}`, hdr); w.Code != http.StatusBadRequest {
		t.Fatalf("empty note: %d", w.Code)
	}
	if w := adminServe(r, http.MethodPost, path, `{"note":"x"}`, map[string]string{"X-Internal-Service-Key": adminTestKey}); w.Code != http.StatusUnauthorized {
		t.Fatalf("no user: %d", w.Code)
	}
	// A blocked video: the request is the author's one appeal (076).
	store.review.State = postgres.TitleReviewBlocked
	w = adminServe(r, http.MethodPost, path, `{"note":"licence attached"}`, hdr)
	body.Data = nil
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != http.StatusOK || body.Data["state"] != "pending" || body.Data["appealed_at"] == nil || body.Data["can_appeal"] != false {
		t.Fatalf("appeal: %d %s", w.Code, w.Body.String())
	}
	now := time.Now()
	store.review.AppealedAt = &now
	if w := adminServe(r, http.MethodPost, path, `{"note":"again"}`, hdr); w.Code != http.StatusConflict || adminErrorCode(t, w) != CodeCopyrightAppealUsed {
		t.Fatalf("second appeal: %d %s", w.Code, w.Body.String())
	}
	store.review.AppealedAt = nil
	store.review.State = postgres.TitleReviewCleared
	if w := adminServe(r, http.MethodPost, path, `{"note":"x"}`, hdr); w.Code != http.StatusConflict || adminErrorCode(t, w) != CodeCopyrightReviewDecided {
		t.Fatalf("decided: %d %s", w.Code, w.Body.String())
	}
	store.review = nil
	if w := adminServe(r, http.MethodPost, path, `{"note":"x"}`, hdr); w.Code != http.StatusNotFound {
		t.Fatalf("no review: %d", w.Code)
	}
}

func TestPublicationRefusals_OnTheWire(t *testing.T) {
	gin.SetMode(gin.TestMode)
	until := time.Date(2026, 10, 31, 9, 30, 0, 0, time.UTC)
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{&service.UploadsPausedError{Until: until}, http.StatusForbidden, CodeUploadsPaused},
		{service.ErrCopyrightReviewTier, http.StatusConflict, CodeCopyrightReview},
	}
	for _, c := range cases {
		for name, write := range map[string]func(*gin.Context, error) bool{
			"standing": writeStandingError, "tier": writeTierGateError,
		} {
			if name == "tier" && c.code == CodeUploadsPaused {
				continue // a pause is a publication refusal, not a tier one
			}
			w := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(w)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			if !write(ctx, c.err) {
				t.Fatalf("%s did not answer %v", name, c.err)
			}
			if w.Code != c.status || !strings.Contains(w.Body.String(), `"`+c.code+`"`) {
				t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
			}
			if c.code == CodeUploadsPaused && !strings.Contains(w.Body.String(), `"until":"2026-10-31T09:30:00Z"`) {
				t.Fatalf("pause without its end: %s", w.Body.String())
			}
		}
	}
}

// Removing a tracked owner match (076): tube:videos.remove only, the acting
// admin recorded, an optional note, 404 for no such track.
func TestCopyrightTrackRemoval_Route(t *testing.T) {
	store := &routeTitleStore{}
	r, rg := copyrightRouter(t, store)
	post, title := uuid.New(), uuid.New()
	path := InternalAdminPrefix + "/copyright/tracks/" + post.String() + "/" + title.String()
	moderate := rg.mint(t, rg.admin, AudiencePost, []string{PermVideosModerate}, rg.actor.String())
	remove := rg.mint(t, rg.admin, AudiencePost, []string{PermVideosRemove}, rg.actor.String())

	if w := adminServe(r, http.MethodDelete, path, `{"note":"licence shown"}`, bearer(moderate)); w.Code != http.StatusForbidden {
		t.Fatalf("moderate only: %d %s", w.Code, w.Body.String())
	}
	if len(store.removed) != 0 {
		t.Fatal("a refused removal reached the store")
	}
	w := adminServe(r, http.MethodDelete, path, `{"note":"licence shown"}`, bearer(remove))
	if w.Code != http.StatusOK || len(store.removed) != 1 || store.removed[0] != [3]uuid.UUID{post, title, rg.actor} || store.removeNote != "licence shown" {
		t.Fatalf("remove: %d %s %+v", w.Code, w.Body.String(), store.removed)
	}
	if w := adminServe(r, http.MethodDelete, path, "", bearer(remove)); w.Code != http.StatusOK {
		t.Fatalf("no body: %d %s", w.Code, w.Body.String())
	}
	if w := adminServe(r, http.MethodDelete, path, `{"why":"x"}`, bearer(remove)); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", w.Code)
	}
	if w := adminServe(r, http.MethodDelete, InternalAdminPrefix+"/copyright/tracks/nope/"+title.String(), "", bearer(remove)); w.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d", w.Code)
	}
	store.noTrack = true
	if w := adminServe(r, http.MethodDelete, path, "", bearer(remove)); w.Code != http.StatusNotFound || adminErrorCode(t, w) != CodeCopyrightTrackNotFound {
		t.Fatalf("no track: %d %s", w.Code, w.Body.String())
	}
	// The bare internal key (what the gateway stamps) never reaches it.
	if w := adminServe(r, http.MethodDelete, path, "", withKey()); w.Code != http.StatusUnauthorized {
		t.Fatalf("key only: %d", w.Code)
	}
}

// The owner-match entry point reaches ApplyOwnerMatch with the provider's
// fields and answers the outcome; a malformed body never reaches it.
func TestCopyrightOwnerMatchRoute_ReachesTheService(t *testing.T) {
	owner := "Sun Pictures"
	store := &routeTitleStore{title: &postgres.CopyrightTitle{ID: uuid.New(), Title: "Jailer 2", Active: true, RightsOwner: &owner},
		matchPost: uuid.New()}
	r, rg := copyrightRouter(t, store)
	hdr := serviceTokenHeaders(t, rg.trustSafety, AudiencePost, OpCopyrightOwnerMatchWrite)
	body := `{"post_id":"` + store.matchPost.String() + `","title_id":"` + store.title.ID.String() + `","reference":"fp-9","policy":"BLOCK"}`
	w := adminServe(r, http.MethodPost, copyrightOwnerMatchPath, body, hdr)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"outcome":"blocked"`) {
		t.Fatalf("match: %d %s", w.Code, w.Body.String())
	}
	if len(store.matches) != 1 || store.matches[0].PostID != store.matchPost || store.matches[0].Reference != "fp-9" ||
		store.matches[0].Policy != postgres.OwnerPolicyBlock {
		t.Fatalf("matches = %+v", store.matches)
	}
	for name, bad := range map[string]string{
		"unknown policy": `{"post_id":"` + store.matchPost.String() + `","title_id":"` + store.title.ID.String() + `","policy":"delete"}`,
		"review policy":  `{"post_id":"` + store.matchPost.String() + `","title_id":"` + store.title.ID.String() + `","policy":"review"}`,
		"bad post":       `{"post_id":"x","title_id":"` + store.title.ID.String() + `","policy":"track"}`,
		"unknown field":  `{"post_id":"` + store.matchPost.String() + `","title_id":"` + store.title.ID.String() + `","policy":"track","owner":"x"}`,
	} {
		if w := adminServe(r, http.MethodPost, copyrightOwnerMatchPath, bad, hdr); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if len(store.matches) != 1 {
		t.Fatalf("a malformed match reached the store: %+v", store.matches)
	}
	missing := `{"post_id":"` + uuid.NewString() + `","title_id":"` + store.title.ID.String() + `","policy":"mute"}`
	if w := adminServe(r, http.MethodPost, copyrightOwnerMatchPath, missing, hdr); w.Code != http.StatusNotFound {
		t.Fatalf("unknown post: %d %s", w.Code, w.Body.String())
	}
}
