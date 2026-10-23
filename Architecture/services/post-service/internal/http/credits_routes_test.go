package http

import (
	"context"
	"encoding/json"
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

// Video credits (2026-10-23) through the REAL router: the new routes, their
// identity and ownership refusals, the PATCH fields (replace, clears, the
// long-video rule, the tagging limit's meta) and the consent answers. The
// in-memory edit store runs the store's own replace planner
// (postgres.PlanCreditReplace) so status preservation and the limit are the
// production rules; the SQL around them is credits_integration_test.go.

type creditRig struct {
	owner, tagged, stranger uuid.UUID
	long, flick             *postgres.Post
	edits                   *creditEditStore
	links                   *creditLinksFake
	router                  *gin.Engine
}

// creditEditStore is a postEditStore and a creditStore over one map.
type creditEditStore struct {
	posts   map[uuid.UUID]*postgres.Post
	credits map[uuid.UUID][]postgres.PostCredit
	quota   int
	writes  int
}

func (f *creditEditStore) GetPost(_ context.Context, id uuid.UUID) (*postgres.Post, error) {
	p, ok := f.posts[id]
	if !ok {
		return nil, nil
	}
	cp := *p
	return &cp, nil
}

func (f *creditEditStore) UpdatePostFields(_ context.Context, postID, actorID uuid.UUID, patch postgres.PostEditPatch) (*postgres.Post, error) {
	p, ok := f.posts[postID]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	if p.AuthorID != actorID {
		return nil, postgres.ErrPostEditNotOwned
	}
	f.writes++
	if patch.Genres != nil {
		p.Genres = *patch.Genres
	}
	switch {
	case patch.ClearReleaseYear:
		p.ReleaseYear = nil
	case patch.ReleaseYear != nil:
		y := *patch.ReleaseYear
		p.ReleaseYear = &y
	}
	switch {
	case patch.ClearProductionHouse:
		p.ProductionHouse = nil
	case patch.ProductionHouse != nil:
		h := *patch.ProductionHouse
		p.ProductionHouse = &h
	}
	if patch.Credits != nil {
		rows, pending, skipped := postgres.PlanCreditReplace(f.credits[postID], patch.Credits.Candidates, f.quota, time.Now())
		f.quota -= len(pending)
		for i := range rows {
			rows[i].PostID = postID
		}
		f.credits[postID] = rows
		patch.Credits.Result = postgres.CreditReplaceResult{Credits: rows, NewPending: pending, Skipped: skipped}
	}
	cp := *p
	return &cp, nil
}

func (f *creditEditStore) BatchGetMediaOwnership(context.Context, []uuid.UUID) (map[uuid.UUID]postgres.MediaOwnership, error) {
	return map[uuid.UUID]postgres.MediaOwnership{}, nil
}
func (f *creditEditStore) PostAuthorsByIDs(context.Context, []uuid.UUID) (map[uuid.UUID]uuid.UUID, error) {
	return map[uuid.UUID]uuid.UUID{}, nil
}
func (f *creditEditStore) CountCreatorContent(context.Context, uuid.UUID) (postgres.CreatorCounts, error) {
	return postgres.CreatorCounts{}, nil
}
func (f *creditEditStore) UpdatePostCategory(context.Context, uuid.UUID, string) error { return nil }

func (f *creditEditStore) ListPostCredits(_ context.Context, postID uuid.UUID) ([]postgres.PostCredit, error) {
	return f.credits[postID], nil
}

func (f *creditEditStore) RespondToCredit(_ context.Context, creditID, callerID uuid.UUID, action string) (string, error) {
	for postID, rows := range f.credits {
		for i, c := range rows {
			if c.ID != creditID {
				continue
			}
			if c.UserID == nil || *c.UserID != callerID {
				return "", postgres.ErrCreditNotFound
			}
			to, _, ok := postgres.NextCreditStatus(c.Status, action)
			if !ok {
				return "", postgres.ErrCreditNotFound
			}
			f.credits[postID][i].Status = to
			if to == postgres.CreditUnlinked {
				f.credits[postID][i].UserID = nil
			}
			return to, nil
		}
	}
	return "", postgres.ErrCreditNotFound
}

func (f *creditEditStore) ListCreditRequests(_ context.Context, userID uuid.UUID, _ *postgres.CreditCursor, _ int) ([]postgres.CreditRequestRow, *postgres.CreditCursor, error) {
	var out []postgres.CreditRequestRow
	for postID, rows := range f.credits {
		for _, c := range rows {
			if c.UserID != nil && *c.UserID == userID && c.Status == postgres.CreditPending {
				out = append(out, postgres.CreditRequestRow{ID: c.ID, Name: c.Name, Role: c.Role, CreatedAt: c.CreatedAt,
					PostID: postID, PostTitle: f.posts[postID].Title, UploaderID: f.posts[postID].AuthorID})
			}
		}
	}
	return out, nil, nil
}

func (f *creditEditStore) ListAppearancePostIDs(_ context.Context, userID uuid.UUID, _ *postgres.CreditCursor, _ int) ([]uuid.UUID, *postgres.CreditCursor, error) {
	var out []uuid.UUID
	for postID, rows := range f.credits {
		for _, c := range rows {
			if c.UserID != nil && *c.UserID == userID && c.Status == postgres.CreditAccepted {
				out = append(out, postID)
			}
		}
	}
	return out, nil, nil
}

type creditLinksFake struct {
	blocked map[uuid.UUID]bool
	calls   int
}

func (f *creditLinksFake) Linkable(_ context.Context, _ uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]bool, string, error) {
	f.calls++
	out := map[uuid.UUID]bool{}
	for _, id := range ids {
		out[id] = !f.blocked[id]
	}
	return out, "Owner", nil
}

func newCreditRig(t *testing.T) *creditRig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := &creditRig{owner: uuid.New(), tagged: uuid.New(), stranger: uuid.New(),
		links: &creditLinksFake{blocked: map[uuid.UUID]bool{}}}
	r.long = &postgres.Post{ID: uuid.New(), AuthorID: r.owner, ContentType: "long_video", Visibility: "public", Title: "Monsoon", ReviewStatus: "approved"}
	r.flick = &postgres.Post{ID: uuid.New(), AuthorID: r.owner, ContentType: "flick", Visibility: "public", ReviewStatus: "approved"}
	r.edits = &creditEditStore{posts: map[uuid.UUID]*postgres.Post{r.long.ID: r.long, r.flick.ID: r.flick},
		credits: map[uuid.UUID][]postgres.PostCredit{}, quota: postgres.CreditTagsPerDay}
	deps := service.HandlerTestDeps{
		PostEdits: r.edits, Credits: r.edits, CreditLinks: r.links,
		Now: func() time.Time { return time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC) },
		CreditProfiles: func(_ context.Context, _ *uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]service.CreditProfile, error) {
			out := map[uuid.UUID]service.CreditProfile{}
			for _, id := range ids {
				out[id] = service.CreditProfile{Username: "user", DisplayName: "User"}
			}
			return out, nil
		},
		AppearancePosts: func(_ context.Context, ids []uuid.UUID, _ *uuid.UUID) (map[uuid.UUID]*service.PostDetail, error) {
			out := map[uuid.UUID]*service.PostDetail{}
			for _, id := range ids {
				if p, ok := r.edits.posts[id]; ok {
					out[id] = &service.PostDetail{Post: p}
				}
			}
			return out, nil
		},
		AppearanceProfileGate: func(context.Context, *uuid.UUID, uuid.UUID) bool { return true },
	}
	r.router = gin.New()
	New(service.NewForHandlerTests(deps), nil).RegisterRoutes(r.router)
	return r
}

func (r *creditRig) do(t *testing.T, method, path string, caller *uuid.UUID, body string) *httptest.ResponseRecorder {
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

type creditDetailBody struct {
	Data struct {
		Credits         []service.CreditView `json:"credits"`
		Genres          []string             `json:"genres"`
		ReleaseYear     *int                 `json:"release_year"`
		ProductionHouse *string              `json:"production_house"`
	} `json:"data"`
	Meta struct {
		CreditTagsSkipped int `json:"credit_tags_skipped"`
	} `json:"meta"`
}

func decodeCreditDetail(t *testing.T, w *httptest.ResponseRecorder) creditDetailBody {
	t.Helper()
	var b creditDetailBody
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return b
}

func TestCreditRoutesRegisteredAndIdentityRequired(t *testing.T) {
	routes := registeredRoutes(t)
	for _, route := range []string{
		"GET /v1/posts/genres", "GET /v1/credits/requests", "POST /v1/credits/:creditId/accept",
		"POST /v1/credits/:creditId/decline", "DELETE /v1/credits/:creditId", "GET /v1/creators/:creatorId/appearances",
	} {
		if !routes[route] {
			t.Errorf("%s is not registered", route)
		}
	}
	r := newCreditRig(t)
	id := uuid.NewString()
	for _, rt := range [][2]string{{"GET", "/v1/credits/requests"}, {"POST", "/v1/credits/" + id + "/accept"},
		{"POST", "/v1/credits/" + id + "/decline"}, {"DELETE", "/v1/credits/" + id}} {
		if w := r.do(t, rt[0], rt[1], nil, ""); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without identity: %d", rt[0], rt[1], w.Code)
		}
	}
	if w := r.do(t, "POST", "/v1/credits/not-an-id/accept", &r.tagged, ""); w.Code != http.StatusBadRequest {
		t.Errorf("bad credit id: %d", w.Code)
	}
}

func TestGenresRoute(t *testing.T) {
	w := newCreditRig(t).do(t, "GET", "/v1/posts/genres", nil, "")
	var b struct {
		Data []service.Genre `json:"data"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &b) != nil || len(b.Data) != 20 || b.Data[0].Slug != "action" {
		t.Fatalf("genres = %d %s", w.Code, w.Body.String())
	}
}

func TestCreditPatch_GuardsAndShapes(t *testing.T) {
	r := newCreditRig(t)
	path := "/v1/posts/" + r.long.ID.String()
	body := `{"credits":[{"name":"Asha Rao","role":"Director","user_id":"` + r.tagged.String() + `"},` +
		`{"name":"Me","role":"Writer","user_id":"` + r.owner.String() + `"},{"name":"Plain","role":"Music"}],` +
		`"genres":["Drama","thriller"],"release_year":2024,"production_house":"Blue Hour Films"}`

	expectCode(t, r.do(t, "PATCH", path, &r.stranger, body), http.StatusForbidden, "FORBIDDEN")
	if r.links.calls != 0 {
		t.Fatal("a stranger's PATCH reached the link checks: the owner guard must refuse first")
	}
	expectCode(t, r.do(t, "PATCH", "/v1/posts/"+r.flick.ID.String(), &r.owner, `{"credits":[{"name":"A","role":"Actor"}]}`),
		http.StatusBadRequest, "CREDITS_NOT_ALLOWED")
	expectCode(t, r.do(t, "PATCH", path, &r.owner, `{"genres":["western-noir"]}`), http.StatusBadRequest, "INVALID_GENRE")
	expectCode(t, r.do(t, "PATCH", path, &r.owner, `{"release_year":"soon"}`), http.StatusBadRequest, "INVALID_RELEASE_YEAR")
	expectCode(t, r.do(t, "PATCH", path, &r.owner, `{"credits":[{"name":"","role":"Actor"}]}`), http.StatusBadRequest, "INVALID_CREDIT")
	if r.edits.writes != 0 {
		t.Fatalf("%d refused PATCHes wrote", r.edits.writes)
	}

	w := r.do(t, "PATCH", path, &r.owner, body)
	if w.Code != 200 {
		t.Fatalf("owner PATCH = %d %s", w.Code, w.Body.String())
	}
	b := decodeCreditDetail(t, w)
	c := b.Data.Credits
	if len(c) != 3 || c[0].Status != "pending" || c[0].UserID == nil || *c[0].UserID != r.tagged || c[0].User != nil {
		t.Fatalf("owner's pending row = %+v", c)
	}
	if c[1].Status != "accepted" || c[1].User == nil {
		t.Fatalf("self-credit row = %+v, want accepted with the account", c[1])
	}
	if c[2].Status != "unlinked" || c[2].UserID != nil {
		t.Fatalf("plain row = %+v", c[2])
	}
	if strings.Join(b.Data.Genres, ",") != "drama,thriller" || b.Data.ReleaseYear == nil || *b.Data.ReleaseYear != 2024 ||
		b.Data.ProductionHouse == nil || b.Meta.CreditTagsSkipped != 0 || strings.Contains(w.Body.String(), `"meta"`) {
		t.Fatalf("detail = %s", w.Body.String())
	}

	// The same tag again (unrelated edit): status kept, nobody re-asked.
	pendingID := c[0].ID
	w = r.do(t, "PATCH", path, &r.owner, body)
	if got := decodeCreditDetail(t, w).Data.Credits[0]; got.ID != pendingID || got.Status != "pending" {
		t.Fatalf("re-sent tag = %+v, want the same pending row", got)
	}

	// Clears: null year, null house, [] genres, [] credits.
	w = r.do(t, "PATCH", path, &r.owner, `{"credits":[],"genres":[],"release_year":null,"production_house":null}`)
	b = decodeCreditDetail(t, w)
	if w.Code != 200 || len(b.Data.Credits) != 0 || b.Data.Credits == nil || len(b.Data.Genres) != 0 || b.Data.ReleaseYear != nil || b.Data.ProductionHouse != nil {
		t.Fatalf("clear = %d %s", w.Code, w.Body.String())
	}
}

func TestCreditPatch_BlockedIsSilentAndLimitIsInMeta(t *testing.T) {
	r := newCreditRig(t)
	blocked := uuid.New()
	r.links.blocked[blocked] = true
	r.edits.quota = 1
	other := uuid.New()
	body := `{"credits":[{"name":"B","role":"Actor","user_id":"` + blocked.String() + `"},` +
		`{"name":"T","role":"Actor","user_id":"` + r.tagged.String() + `"},` +
		`{"name":"O","role":"Actor","user_id":"` + other.String() + `"}]}`
	w := r.do(t, "PATCH", "/v1/posts/"+r.long.ID.String(), &r.owner, body)
	b := decodeCreditDetail(t, w)
	if w.Code != 200 {
		t.Fatalf("PATCH = %d %s", w.Code, w.Body.String())
	}
	c := b.Data.Credits
	if c[0].Status != "unlinked" || c[0].UserID != nil || c[0].Name != "B" {
		t.Fatalf("blocked tag = %+v, want plain text", c[0])
	}
	if c[1].Status != "pending" || c[2].Status != "unlinked" {
		t.Fatalf("limit: %+v %+v", c[1], c[2])
	}
	// Only the limit is reported, never the block.
	if b.Meta.CreditTagsSkipped != 1 {
		t.Fatalf("meta.credit_tags_skipped = %d, want 1 (the block must not count)", b.Meta.CreditTagsSkipped)
	}
}

func TestCreditConsentRoutes(t *testing.T) {
	r := newCreditRig(t)
	w := r.do(t, "PATCH", "/v1/posts/"+r.long.ID.String(), &r.owner,
		`{"credits":[{"name":"T","role":"Actor","user_id":"`+r.tagged.String()+`"}]}`)
	creditID := decodeCreditDetail(t, w).Data.Credits[0].ID.String()

	// My requests.
	w = r.do(t, "GET", "/v1/credits/requests", &r.tagged, "")
	var reqs struct {
		Data []service.CreditRequest `json:"data"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &reqs) != nil || len(reqs.Data) != 1 || reqs.Data[0].Post.Title != "Monsoon" || reqs.Data[0].From.ID != r.owner {
		t.Fatalf("requests = %d %s", w.Code, w.Body.String())
	}
	if w := r.do(t, "GET", "/v1/credits/requests", &r.stranger, ""); !strings.Contains(w.Body.String(), `"data":[]`) {
		t.Fatalf("a stranger's requests list = %s", w.Body.String())
	}

	// Nobody else may answer — not the uploader, not a stranger.
	for _, who := range []uuid.UUID{r.owner, r.stranger} {
		expectCode(t, r.do(t, "POST", "/v1/credits/"+creditID+"/accept", &who, ""), http.StatusNotFound, "NOT_FOUND")
	}
	expectCode(t, r.do(t, "POST", "/v1/credits/"+uuid.NewString()+"/accept", &r.tagged, ""), http.StatusNotFound, "NOT_FOUND")

	// Accept, idempotently; appears in the person's list.
	for i := 0; i < 2; i++ {
		w = r.do(t, "POST", "/v1/credits/"+creditID+"/accept", &r.tagged, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"accepted"`) {
			t.Fatalf("accept #%d = %d %s", i+1, w.Code, w.Body.String())
		}
	}
	w = r.do(t, "GET", "/v1/creators/"+r.tagged.String()+"/appearances", nil, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), r.long.ID.String()) {
		t.Fatalf("appearances = %d %s", w.Code, w.Body.String())
	}
	if w := r.do(t, "GET", "/v1/creators/"+r.tagged.String()+"/appearances?cursor=junk", nil, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("bad cursor = %d", w.Code)
	}

	// Remove me: the link goes, the name stays, and a second remove is 404.
	w = r.do(t, "DELETE", "/v1/credits/"+creditID, &r.tagged, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"unlinked"`) {
		t.Fatalf("remove = %d %s", w.Code, w.Body.String())
	}
	if rows := r.edits.credits[r.long.ID]; rows[0].UserID != nil || rows[0].Name != "T" {
		t.Fatalf("after remove = %+v", rows[0])
	}
	expectCode(t, r.do(t, "DELETE", "/v1/credits/"+creditID, &r.tagged, ""), http.StatusNotFound, "NOT_FOUND")
	if w := r.do(t, "GET", "/v1/creators/"+r.tagged.String()+"/appearances", nil, ""); strings.Contains(w.Body.String(), r.long.ID.String()) {
		t.Fatal("a removed credit still lists the video")
	}
}
