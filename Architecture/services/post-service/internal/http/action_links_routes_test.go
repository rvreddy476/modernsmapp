package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/actionlinks"
	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/service/actionfake"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// The Action button on the real router (action_links.go): the labels
// route, the codes every write answers, PATCH null / omitted, the public
// redirect read and its tap count, the stats route, and the profile button
// on PATCH /v1/channels/me and GET /v1/channels/:ref.

type rigAccounts struct{ created time.Time }

func (a rigAccounts) Account(context.Context, uuid.UUID) (actionlinks.AccountInfo, error) {
	return actionlinks.AccountInfo{Found: true, Active: true, CreatedAt: a.created}, nil
}

type rigEmails struct{ ok *bool }

func (e rigEmails) EmailVerified(context.Context, uuid.UUID) (bool, error) { return *e.ok, nil }

type rigTaps struct{ seen map[string]bool }

func (d *rigTaps) FirstTap(_ context.Context, key string, _ time.Duration) (bool, error) {
	if d.seen[key] {
		return false, nil
	}
	d.seen[key] = true
	return true, nil
}

// actionChannels is esChannels whose UpdateChannel answers the channel.
type actionChannels struct{ *esChannels }

func (c actionChannels) UpdateChannel(_ context.Context, id uuid.UUID, _ postgres.ChannelPatch) (*postgres.Channel, error) {
	if ch, ok := c.byUser[id]; ok {
		cp := *ch
		return &cp, nil
	}
	return nil, postgres.ErrChannelNotFound
}

type actionRouteRig struct {
	router        *gin.Engine
	svc           *service.Service
	links         *actionfake.Store
	edits         *hubEditStore
	author, other uuid.UUID
	post          uuid.UUID
	emailOK       bool
	checker       *actionlinks.Mock
}

func newActionRouteRig(t *testing.T) *actionRouteRig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := &actionRouteRig{links: actionfake.New(), edits: newHubEditStore(), author: uuid.New(), other: uuid.New(), post: uuid.New(), emailOK: true,
		checker: &actionlinks.Mock{}}
	r.links.Posts[r.post] = r.author
	r.edits.posts[r.post] = &postgres.Post{ID: r.post, AuthorID: r.author, Visibility: "public", ContentType: "post", Text: "hiring"}
	chans := actionChannels{&esChannels{byUser: map[uuid.UUID]*postgres.Channel{
		r.author: {ID: uuid.New(), UserID: r.author, Name: "Ravi", Handle: "ravi"},
	}, subs: map[[2]uuid.UUID]bool{}}}
	deps := service.HandlerTestDeps{
		PostEdits: r.edits, Channels: chans, ActionLinks: r.links,
		CreateIdempotencyLookup: func(context.Context, uuid.UUID, string) (uuid.UUID, string, bool, error) {
			return uuid.Nil, "", false, nil
		},
		PostGate: func(_ context.Context, id uuid.UUID) (uuid.UUID, *uuid.UUID, error) {
			if author, ok := r.links.Posts[id]; ok {
				return author, nil, nil
			}
			return uuid.Nil, nil, service.ErrPostNotFound
		},
	}
	r.svc = service.NewForHandlerTests(deps)
	r.svc.SetActionLinks(service.ActionLinkConfig{
		Checker: r.checker, Accounts: rigAccounts{created: time.Now().AddDate(0, -1, 0)}, Emails: rigEmails{ok: &r.emailOK},
		TapDedupe: &rigTaps{seen: map[string]bool{}}, Store: r.links,
	})
	r.router = gin.New()
	r.router.Use(gin.CustomRecovery(func(c *gin.Context, _ any) { c.AbortWithStatus(http.StatusInternalServerError) }))
	h := New(r.svc, nil)
	h.RegisterRoutes(r.router)
	return r
}

func (r *actionRouteRig) do(method, path string, caller *uuid.UUID, body string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", uuid.NewString())
	if caller != nil {
		req.Header.Set("X-User-Id", caller.String())
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	r.router.ServeHTTP(w, req)
	return w
}

func dataOf(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return env.Data
}

func detailsOf(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var env struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	return env.Error.Details
}

// GET /v1/posts/action-labels answers the enum (not GetPost), cacheably,
// and is registered before /v1/posts/:postId.
func TestActionLabelsRoute(t *testing.T) {
	r := newActionRouteRig(t)
	w := r.do(http.MethodGet, "/v1/posts/action-labels", nil, "")
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Cache-Control"), "public") {
		t.Fatalf("labels: %d %q %s", w.Code, w.Header().Get("Cache-Control"), w.Body.String())
	}
	var env struct {
		Data []actionlinks.Label `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil || !reflect.DeepEqual(env.Data, actionlinks.Labels) {
		t.Fatalf("labels body = %s", w.Body.String())
	}
	if env.Data[0].Value != "apply_now" || env.Data[0].Label != "Apply now" {
		t.Fatalf("first label = %+v", env.Data[0])
	}

	src, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatal(err)
	}
	labels := strings.Index(string(src), `v1.GET("/action-labels", h.ListActionLabels)`)
	detail := strings.Index(string(src), `v1.GET("/:postId", h.GetPost)`)
	if labels < 0 || detail < 0 || labels > detail {
		t.Fatalf("GET /action-labels (at %d) must be registered before GET /:postId (at %d)", labels, detail)
	}
}

// POST /v1/posts: every refusal code, before anything is written.
func TestCreatePostActionRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, action, code string
		status             int
		setup              func(*actionRouteRig)
	}{
		{"label", `{"label":"buy_now","url":"https://example.com"}`, "INVALID_ACTION_LABEL", 400, nil},
		{"http", `{"label":"apply_now","url":"http://example.com"}`, "INVALID_ACTION_URL", 400, nil},
		{"ip", `{"label":"apply_now","url":"https://10.0.0.1/"}`, "INVALID_ACTION_URL", 400, nil},
		{"shortener", `{"label":"apply_now","url":"https://bit.ly/x"}`, "ACTION_LINK_SHORTENER", 400, nil},
		{"group invite", `{"label":"apply_now","url":"https://t.me/joinchat/x"}`, "ACTION_LINK_GROUP_INVITE", 400, nil},
		{"unsafe", `{"label":"apply_now","url":"https://testsafebrowsing.appspot.com/s/phishing.html"}`, "ACTION_LINK_UNSAFE", 400, nil},
		{"check down", `{"label":"apply_now","url":"https://example.com"}`, "ACTION_LINK_CHECK_UNAVAILABLE", 503,
			func(r *actionRouteRig) { r.checker.Err = actionlinks.ErrCheckUnavailable }},
		{"not eligible", `{"label":"apply_now","url":"https://example.com"}`, "ACTION_NOT_ELIGIBLE", 403,
			func(r *actionRouteRig) { r.emailOK = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newActionRouteRig(t)
			if tc.setup != nil {
				tc.setup(r)
			}
			w := r.do(http.MethodPost, "/v1/posts", &r.author, `{"text":"hiring","visibility":"public","action":`+tc.action+`}`)
			if w.Code != tc.status || codeOf(w) != tc.code {
				t.Fatalf("%d %s; want %d %s", w.Code, w.Body.String(), tc.status, tc.code)
			}
			if tc.code == "ACTION_NOT_ELIGIBLE" {
				if got := detailsOf(t, w)["missing"]; !reflect.DeepEqual(got, []any{"email_verified"}) {
					t.Fatalf("details.missing = %v", got)
				}
			}
			if r.links.Replaces != 0 || len(r.links.Links) != 0 {
				t.Fatal("a refused create stored a link")
			}
		})
	}
}

// PATCH /v1/posts/:id: an object sets, omitted leaves, null removes; the
// author's answer carries url and status.
func TestPatchPostAction(t *testing.T) {
	r := newActionRouteRig(t)
	path := "/v1/posts/" + r.post.String()
	w := r.do(http.MethodPatch, path, &r.author, `{"action":{"label":"apply_now","url":"https://www.epfindia.gov.in/apply"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("set: %d %s", w.Code, w.Body.String())
	}
	act, _ := dataOf(t, w)["action"].(map[string]any)
	if act == nil || act["url"] != "https://www.epfindia.gov.in/apply" || act["status"] != "active" || act["domain"] != "epfindia.gov.in" ||
		act["label_text"] != "Apply now" || act["href"] != "/l/"+act["id"].(string) {
		t.Fatalf("author's action = %v", act)
	}
	id := act["id"].(string)

	w = r.do(http.MethodPatch, path, &r.author, `{"title":"Hiring now"}`)
	act, _ = dataOf(t, w)["action"].(map[string]any)
	if w.Code != http.StatusOK || act == nil || act["id"] != id || r.links.Replaces != 1 {
		t.Fatalf("omitted action changed it: %d %v (replaces %d)", w.Code, act, r.links.Replaces)
	}

	w = r.do(http.MethodPatch, path, &r.author, `{"action":"apply_now"}`)
	if w.Code != http.StatusBadRequest || codeOf(w) != "INVALID_REQUEST" {
		t.Fatalf("non-object action: %d %s", w.Code, w.Body.String())
	}
	w = r.do(http.MethodPatch, path, &r.author, `{"action":{"label":"apply_now","url":"https://cutt.ly/x"}}`)
	if w.Code != http.StatusBadRequest || codeOf(w) != "ACTION_LINK_SHORTENER" {
		t.Fatalf("shortener: %d %s", w.Code, w.Body.String())
	}

	w = r.do(http.MethodPatch, path, &r.author, `{"action":null}`)
	if w.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", w.Code, w.Body.String())
	}
	if _, present := dataOf(t, w)["action"]; present {
		t.Fatalf("removed action still answered: %s", w.Body.String())
	}
	if r.links.Get(uuid.MustParse(id)).Status != postgres.ActionStatusRemoved {
		t.Fatal("null did not remove the link")
	}
	if w := r.do(http.MethodPatch, path, &r.other, `{"action":{"label":"apply_now","url":"https://example.com"}}`); w.Code != http.StatusForbidden {
		t.Fatalf("someone else's post: %d %s", w.Code, w.Body.String())
	}
}

// GET /v1/links/:id is public, active-only, counted once per tapper an
// hour; GET /v1/posts/:id/action/stats is the author's.
func TestResolveLinkAndStatsRoutes(t *testing.T) {
	r := newActionRouteRig(t)
	v, err := r.svc.SetPostAction(context.Background(), r.author, r.post, &service.ActionInput{Label: "book_now", URL: "https://example.com/book"}, false)
	if err != nil {
		t.Fatal(err)
	}
	link := "/v1/links/" + v.ID.String()
	w := r.do(http.MethodGet, link, nil, "", "X-Real-IP", "203.0.113.7", "User-Agent", "phone-a")
	got := dataOf(t, w)
	want := map[string]any{"id": v.ID.String(), "url": "https://example.com/book", "domain": "example.com", "label": "book_now", "status": "active"}
	if w.Code != http.StatusOK || !reflect.DeepEqual(got, want) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("resolve: %d %v %q", w.Code, got, w.Header().Get("Cache-Control"))
	}
	r.do(http.MethodGet, link, nil, "", "X-Real-IP", "203.0.113.7", "User-Agent", "phone-a") // same tapper
	r.do(http.MethodGet, link, nil, "", "X-Real-IP", "203.0.113.7", "User-Agent", "phone-b") // same IP, other device
	viewer := uuid.New()
	r.do(http.MethodGet, link, &viewer, "", "X-Real-IP", "198.51.100.1")
	r.do(http.MethodGet, link, &viewer, "", "X-Real-IP", "198.51.100.2") // same viewer, other network
	if n := r.links.Get(v.ID).ClicksTotal; n != 3 {
		t.Fatalf("clicks = %d, want 3", n)
	}

	w = r.do(http.MethodGet, "/v1/posts/"+r.post.String()+"/action/stats", &r.author, "")
	if w.Code != http.StatusOK || dataOf(t, w)["clicks_total"] != float64(3) || dataOf(t, w)["clicks_7d"] != float64(3) {
		t.Fatalf("stats: %d %s", w.Code, w.Body.String())
	}
	if w := r.do(http.MethodGet, "/v1/posts/"+r.post.String()+"/action/stats", &r.other, ""); w.Code != http.StatusForbidden {
		t.Fatalf("stranger stats: %d", w.Code)
	}
	if w := r.do(http.MethodGet, "/v1/posts/"+r.post.String()+"/action/stats", nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out stats: %d", w.Code)
	}
	if w := r.do(http.MethodGet, "/v1/posts/"+uuid.NewString()+"/action/stats", &r.author, ""); w.Code != http.StatusNotFound {
		t.Fatalf("unknown post stats: %d", w.Code)
	}

	reason := postgres.ActionReasonUnsafe
	r.links.Links[v.ID].Status, r.links.Links[v.ID].StatusReason = postgres.ActionStatusBlocked, &reason
	if w := r.do(http.MethodGet, link, nil, ""); w.Code != http.StatusNotFound || codeOf(w) != "NOT_FOUND" {
		t.Fatalf("blocked link: %d %s", w.Code, w.Body.String())
	}
	if w := r.do(http.MethodGet, "/v1/links/not-a-uuid", nil, ""); w.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d", w.Code)
	}
}

// The profile button: PATCH /v1/channels/me sets / removes it; the owner
// reads url and status; a viewer reads the public shape; none is null.
func TestChannelProfileAction(t *testing.T) {
	r := newActionRouteRig(t)
	viewer := uuid.New()
	w := r.do(http.MethodGet, "/v1/channels/"+r.author.String(), &viewer, "")
	if w.Code != http.StatusOK {
		t.Fatalf("channel: %d %s", w.Code, w.Body.String())
	}
	if v, present := dataOf(t, w)["action"]; !present || v != nil {
		t.Fatalf("no button: action = %v (present %v), want null", v, present)
	}

	w = r.do(http.MethodPatch, "/v1/channels/me", &r.author, `{"action":{"label":"contact_us","url":"https://example.com/contact"}}`)
	act, _ := dataOf(t, w)["action"].(map[string]any)
	if w.Code != http.StatusOK || act == nil || act["url"] != "https://example.com/contact" || act["status"] != "active" {
		t.Fatalf("set: %d %s", w.Code, w.Body.String())
	}
	w = r.do(http.MethodGet, "/v1/channels/me", &r.author, "")
	if act, _ := dataOf(t, w)["action"].(map[string]any); act == nil || act["status"] != "active" || act["url"] == nil {
		t.Fatalf("owner read: %s", w.Body.String())
	}
	w = r.do(http.MethodGet, "/v1/channels/"+r.author.String(), &viewer, "")
	act, _ = dataOf(t, w)["action"].(map[string]any)
	if act == nil || act["label_text"] != "Contact us" || act["domain"] != "example.com" {
		t.Fatalf("viewer read: %s", w.Body.String())
	}
	for _, k := range []string{"url", "status"} {
		if _, ok := act[k]; ok {
			t.Errorf("viewer read carries %q", k)
		}
	}

	if w := r.do(http.MethodPatch, "/v1/channels/me", &r.author, `{"action":{"label":"contact_us","url":"https://chat.whatsapp.com/x"}}`); w.Code != 400 || codeOf(w) != "ACTION_LINK_GROUP_INVITE" {
		t.Fatalf("group invite: %d %s", w.Code, w.Body.String())
	}
	if w := r.do(http.MethodPatch, "/v1/channels/me", &r.author, `{"action":42}`); w.Code != 400 || codeOf(w) != "INVALID_REQUEST" {
		t.Fatalf("non-object: %d %s", w.Code, w.Body.String())
	}
	w = r.do(http.MethodPatch, "/v1/channels/me", &r.author, `{"action":null}`)
	if v, present := dataOf(t, w)["action"]; w.Code != 200 || !present || v != nil {
		t.Fatalf("remove: %d %s", w.Code, w.Body.String())
	}
	w = r.do(http.MethodGet, "/v1/channels/me/action/stats", &r.author, "")
	if w.Code != 200 || dataOf(t, w)["clicks_total"] != float64(0) {
		t.Fatalf("profile stats: %d %s", w.Code, w.Body.String())
	}
}
