package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/actionlinks"
	"github.com/atpost/post-service/internal/notetest"
	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/standing"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/store/scylla"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Echo routes through the real router (notes_handler.go) over the
// in-memory note store, and the golden fixtures the clients copy
// (testdata/contracts/notes at the module root, as the contract names it).
//
//	UPDATE_CONTRACTS=1 go test ./internal/http -run TestNoteContracts
//
// regenerates the fixtures; review the diff before committing.

var (
	ntTime   = time.Date(2026, 11, 12, 9, 30, 0, 0, time.UTC)
	ntNow    = ntTime.Add(10 * time.Minute)
	ntAlice  = uuid.MustParse("a1111111-1111-4111-8111-111111111111") // premium
	ntBob    = uuid.MustParse("b2222222-2222-4222-8222-222222222222") // free, old account
	ntCarol  = uuid.MustParse("c3333333-3333-4333-8333-333333333333") // 2 days old
	ntTop    = uuid.MustParse("f1111111-1111-4111-8111-111111111111")
	ntReply1 = uuid.MustParse("f2222222-2222-4222-8222-222222222222")
	ntReply2 = uuid.MustParse("f3333333-3333-4333-8333-333333333333")
	ntQuoted = uuid.MustParse("f4444444-4444-4444-8444-444444444444")
	ntImage  = uuid.MustParse("e1111111-1111-4111-8111-111111111111")
)

type ntAccounts map[uuid.UUID]time.Time

func (a ntAccounts) Account(_ context.Context, id uuid.UUID) (actionlinks.AccountInfo, error) {
	at, ok := a[id]
	return actionlinks.AccountInfo{Found: ok, Active: ok, CreatedAt: at}, nil
}

type ntStanding struct{}

func (ntStanding) Check(context.Context, uuid.UUID) (*standing.Result, error) {
	return &standing.Result{Standing: standing.StandingOK}, nil
}

type noteRouter struct {
	router  *gin.Engine
	store   *notetest.Store
	premium *notetest.Premium
	cfg     service.NoteConfig
	svc     *service.Service
}

func newNoteRouter(t *testing.T, enabled bool) *noteRouter {
	t.Helper()
	gin.SetMode(gin.TestMode)
	nr := &noteRouter{store: notetest.New(), premium: &notetest.Premium{Users: map[uuid.UUID]bool{ntAlice: true}}}
	nr.cfg = service.NoteConfig{Enabled: enabled, Premium: nr.premium, Accounts: ntAccounts{
		ntAlice: ntTime.AddDate(-1, 0, 0), ntBob: ntTime.AddDate(0, -3, 0), ntCarol: ntTime.AddDate(0, 0, -2),
	}}
	nr.svc = service.NewForHandlerTests(service.HandlerTestDeps{
		Now: func() time.Time { return ntNow },
		BatchPosts: func(_ context.Context, ids []uuid.UUID, viewer *uuid.UUID) (map[uuid.UUID]*service.PostDetail, error) {
			out := map[uuid.UUID]*service.PostDetail{}
			for _, id := range ids {
				if p, ok := nr.store.Visible(id, viewer); ok {
					out[id] = &service.PostDetail{Post: p, Counts: &scylla.Counts{Likes: 4}, RepostCount: 1,
						HasReacted: viewer != nil && *viewer == ntBob}
				}
			}
			return out, nil
		},
		BulkDelete: func(_ context.Context, id, _ uuid.UUID) error { nr.store.Delete(id); return nil },
	}).WithNoteTestDeps(service.NoteTestDeps{
		Config:  nr.cfg,
		Store:   nr.store,
		Limiter: notetest.NewLimiter(),
		Relations: &notetest.Relations{Follows: map[uuid.UUID]map[uuid.UUID]bool{},
			Usernames: map[string]uuid.UUID{}},
		Standing: ntStanding{},
		Authors: func(_ context.Context, _ *uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]*service.NoteAuthor, error) {
			out := map[uuid.UUID]*service.NoteAuthor{}
			names := map[uuid.UUID][2]string{ntAlice: {"alice", "Alice Rao"}, ntBob: {"bob", "Bob Das"}}
			for _, id := range ids {
				if n, ok := names[id]; ok {
					out[id] = &service.NoteAuthor{ID: id, Username: n[0], DisplayName: n[1],
						AvatarURL:  "https://cdn.example.test/avatars/" + n[0] + ".jpg",
						IsVerified: id == ntAlice, VerificationLevel: map[bool]string{true: "person"}[id == ntAlice]}
				}
			}
			return out, nil
		},
		EmitEvents: true,
	})
	nr.router = gin.New()
	New(nr.svc, nil).RegisterRoutes(nr.router)
	return nr
}

// seed stores the fixture conversation: Alice's top note quoting her
// earlier note, with two replies.
func (nr *noteRouter) seed() {
	nr.store.Put(postgres.Note{Post: postgres.Post{ID: ntQuoted, AuthorID: ntAlice, Text: "Monsoon is here.",
		PostType: "text", AppOrigin: "echo", CreatedAt: ntTime.Add(-2 * time.Hour), UpdatedAt: ntTime.Add(-2 * time.Hour)}})
	nr.store.Put(postgres.Note{
		Post: postgres.Post{ID: ntTop, AuthorID: ntAlice, Text: "Rain all week in #Bengaluru", Hashtags: []string{"bengaluru"},
			PostType: "image", AppOrigin: "echo", CreatedAt: ntTime, UpdatedAt: ntTime,
			Media: []postgres.PostMedia{{MediaID: ntImage, Kind: "image", Position: 0, ProcessingStatus: "ready", ModerationStatus: "passed"}}},
		NoteFields: postgres.NoteFields{QuoteOfID: &ntQuoted, ReplyCount: 2},
	})
	for i, r := range []struct {
		id     uuid.UUID
		author uuid.UUID
		text   string
	}{{ntReply1, ntBob, "Same here"}, {ntReply2, ntAlice, "Stay dry"}} {
		at := ntTime.Add(time.Duration(i+1) * time.Minute)
		nr.store.Put(postgres.Note{
			Post:       postgres.Post{ID: r.id, AuthorID: r.author, Text: r.text, PostType: "text", AppOrigin: "echo", CreatedAt: at, UpdatedAt: at},
			NoteFields: postgres.NoteFields{ReplyToID: &ntTop, ConversationID: &ntTop},
		})
	}
}

type noteResp struct {
	code int
	body []byte
	env  struct {
		Data  json.RawMessage `json:"data"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
}

func (nr *noteRouter) call(t *testing.T, method, path string, user *uuid.UUID, body string, key bool) noteResp {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if user != nil {
		req.Header.Set("X-User-Id", user.String())
	}
	if key {
		req.Header.Set("Idempotency-Key", uuid.NewString())
	}
	w := httptest.NewRecorder()
	nr.router.ServeHTTP(w, req)
	r := noteResp{code: w.Code, body: w.Body.Bytes()}
	_ = json.Unmarshal(r.body, &r.env)
	return r
}

func (r noteResp) want(t *testing.T, code int, errCode string) {
	t.Helper()
	got := ""
	if r.env.Error != nil {
		got = r.env.Error.Code
	}
	if r.code != code || got != errCode {
		t.Fatalf("got %d %q (%s), want %d %q", r.code, got, r.body, code, errCode)
	}
}

var noteRoutes = []struct{ method, path string }{
	{http.MethodPost, "/v1/notes"},
	{http.MethodPost, "/v1/notes/thread"},
	{http.MethodGet, "/v1/notes/me/eligibility"},
	{http.MethodGet, "/v1/notes/users/" + ntAlice.String()},
	{http.MethodGet, "/v1/notes/" + ntTop.String()},
	{http.MethodGet, "/v1/notes/" + ntTop.String() + "/replies"},
	{http.MethodGet, "/v1/notes/" + ntTop.String() + "/conversation"},
	{http.MethodPatch, "/v1/notes/" + ntTop.String()},
	{http.MethodDelete, "/v1/notes/" + ntTop.String()},
}

func TestNoteRoutes_EchoSwitchedOffIs503Everywhere(t *testing.T) {
	nr := newNoteRouter(t, false)
	nr.seed()
	for _, rt := range noteRoutes {
		r := nr.call(t, rt.method, rt.path, &ntAlice, `{"text":"hi"}`, true)
		if r.code != http.StatusServiceUnavailable || r.env.Error == nil || r.env.Error.Code != CodeEchoNotEnabled {
			t.Errorf("%s %s with ECHO_ENABLED off: %d %s", rt.method, rt.path, r.code, r.body)
		}
	}
	if len(nr.store.Writes) != 0 || nr.store.Deleted[ntTop] || nr.premium.Calls != 0 {
		t.Fatal("a switched-off route reached the service")
	}
}

func TestNoteRoutes_EveryRouteAnswers(t *testing.T) {
	nr := newNoteRouter(t, true)
	nr.seed()
	nr.call(t, http.MethodPost, "/v1/notes", &ntAlice, `{"text":"first"}`, true).want(t, http.StatusCreated, "")
	r := nr.call(t, http.MethodPost, "/v1/notes/thread", &ntAlice, `{"entries":[{"text":"1/2"},{"text":"2/2"}]}`, true)
	r.want(t, http.StatusCreated, "")
	var thread struct {
		Notes []json.RawMessage `json:"notes"`
	}
	if err := json.Unmarshal(r.env.Data, &thread); err != nil || len(thread.Notes) != 2 {
		t.Fatalf("thread answer %s", r.body)
	}
	for _, rt := range noteRoutes[2:7] {
		nr.call(t, rt.method, rt.path, &ntBob, "", false).want(t, http.StatusOK, "")
	}
	// Reads are open to signed-out callers.
	nr.call(t, http.MethodGet, "/v1/notes/"+ntTop.String(), nil, "", false).want(t, http.StatusOK, "")
	nr.call(t, http.MethodGet, "/v1/notes/users/"+ntAlice.String()+"?tab=replies", nil, "", false).want(t, http.StatusOK, "")
	nr.call(t, http.MethodPatch, "/v1/notes/"+ntTop.String(), &ntAlice, `{"reply_policy":"following"}`, false).want(t, http.StatusOK, "")
	nr.call(t, http.MethodDelete, "/v1/notes/"+ntReply1.String(), &ntBob, "", false).want(t, http.StatusNoContent, "")
	if !nr.store.Deleted[ntReply1] {
		t.Fatal("DELETE did not delete")
	}
}

func TestNoteRoutes_Refusals(t *testing.T) {
	nr := newNoteRouter(t, true)
	nr.seed()
	gif := uuid.New()
	nr.store.Media[gif] = postgres.NoteMediaRow{UploaderID: ntAlice, Kind: "image", MimeType: "image/gif", ProcessingStatus: "ready", ModerationStatus: "passed"}
	video := uuid.New()
	nr.store.Media[video] = postgres.NoteMediaRow{UploaderID: ntAlice, Kind: "video", MimeType: "video/mp4", ProcessingStatus: "ready", ModerationStatus: "passed"}
	img := func(owner uuid.UUID) string {
		id := uuid.New()
		nr.store.Media[id] = postgres.NoteMediaRow{UploaderID: owner, Kind: "image", MimeType: "image/jpeg", ProcessingStatus: "ready", ModerationStatus: "passed"}
		return `"` + id.String() + `"`
	}
	top, missing := ntTop.String(), uuid.NewString()
	cases := []struct {
		name, method, path string
		user               *uuid.UUID
		body               string
		key                bool
		status             int
		code               string
	}{
		{"signed out create", "POST", "/v1/notes", nil, `{"text":"x"}`, true, 401, "UNAUTHORIZED"},
		{"signed out eligibility", "GET", "/v1/notes/me/eligibility", nil, "", false, 401, "UNAUTHORIZED"},
		{"no idempotency key", "POST", "/v1/notes", &ntAlice, `{"text":"x"}`, false, 400, "MISSING_IDEMPOTENCY_KEY"},
		{"bad json", "POST", "/v1/notes", &ntAlice, `{"text":`, true, 400, "INVALID_REQUEST"},
		{"empty", "POST", "/v1/notes", &ntAlice, `{"text":"  "}`, true, 400, CodeEmptyNote},
		{"too long", "POST", "/v1/notes", &ntAlice, `{"text":"` + strings.Repeat("a", 501) + `"}`, true, 400, CodeNoteTooLong},
		{"five images", "POST", "/v1/notes", &ntAlice, `{"media_ids":[` + strings.Join([]string{img(ntAlice), img(ntAlice), img(ntAlice), img(ntAlice), img(ntAlice)}, ",") + `]}`, true, 400, CodeTooManyMedia},
		{"gif and image", "POST", "/v1/notes", &ntAlice, `{"media_ids":["` + gif.String() + `",` + img(ntAlice) + `]}`, true, 400, CodeTooManyMedia},
		{"video", "POST", "/v1/notes", &ntAlice, `{"media_ids":["` + video.String() + `"]}`, true, 400, CodeMediaNotAllowed},
		{"someone else's image", "POST", "/v1/notes", &ntAlice, `{"media_ids":[` + img(ntBob) + `]}`, true, 403, "MEDIA_NOT_OWNED"},
		{"reply and quote", "POST", "/v1/notes", &ntAlice, `{"text":"x","reply_to_id":"` + top + `","quote_of_id":"` + top + `"}`, true, 400, "INVALID_REQUEST"},
		{"not premium", "POST", "/v1/notes", &ntBob, `{"text":"x"}`, true, 403, CodePremiumRequired},
		{"not premium quote", "POST", "/v1/notes", &ntBob, `{"text":"x","quote_of_id":"` + top + `"}`, true, 403, CodePremiumRequired},
		{"not premium thread", "POST", "/v1/notes/thread", &ntBob, `{"entries":[{"text":"a"},{"text":"b"}]}`, true, 403, CodePremiumRequired},
		{"account too new", "POST", "/v1/notes", &ntCarol, `{"text":"x","reply_to_id":"` + top + `"}`, true, 403, CodeAccountTooNew},
		{"reply target missing", "POST", "/v1/notes", &ntBob, `{"text":"x","reply_to_id":"` + missing + `"}`, true, 404, CodeNoteNotFound},
		{"one-entry thread", "POST", "/v1/notes/thread", &ntAlice, `{"entries":[{"text":"a"}]}`, true, 400, CodeInvalidThread},
		{"get missing", "GET", "/v1/notes/" + missing, nil, "", false, 404, CodeNoteNotFound},
		{"get bad id", "GET", "/v1/notes/not-a-uuid", nil, "", false, 404, CodeNoteNotFound},
		{"bad tab", "GET", "/v1/notes/users/" + ntAlice.String() + "?tab=likes", nil, "", false, 400, "INVALID_REQUEST"},
		{"patch by stranger", "PATCH", "/v1/notes/" + top, &ntBob, `{"text":"mine now"}`, false, 403, CodeNotAuthor},
		{"delete by stranger", "DELETE", "/v1/notes/" + top, &ntBob, "", false, 403, CodeNotAuthor},
		{"bad policy", "PATCH", "/v1/notes/" + top, &ntAlice, `{"reply_policy":"friends"}`, false, 400, "INVALID_REQUEST"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			nr.call(t, c.method, c.path, c.user, c.body, c.key).want(t, c.status, c.code)
		})
	}

	// Edit window: move Alice's earlier note past 30 minutes.
	nr.store.Note(ntQuoted).Post.CreatedAt = ntNow.Add(-31 * time.Minute)
	nr.call(t, "PATCH", "/v1/notes/"+ntQuoted.String(), &ntAlice, `{"text":"late"}`, false).want(t, 403, CodeEditWindowClosed)

	// reply_policy and the premium check failing closed.
	nr.store.Note(ntTop).ReplyPolicy = service.ReplyPolicyMentioned
	nr.call(t, "POST", "/v1/notes", &ntBob, `{"text":"x","reply_to_id":"`+top+`"}`, true).want(t, 403, CodeReplyNotAllowed)
	nr.premium.Err = errors.New("auth down")
	nr.call(t, "POST", "/v1/notes", &ntAlice, `{"text":"x"}`, true).want(t, 503, CodePremiumCheckUnavailable)
	nr.call(t, "GET", "/v1/notes/me/eligibility", &ntAlice, "", false).want(t, 503, CodePremiumCheckUnavailable)
}

func TestNoteRoutes_RateLimitedAndKeyReuse(t *testing.T) {
	nr := newNoteRouter(t, true)
	cfg := nr.cfg
	cfg.Limits = service.NoteLimits{NotesPerHour: 1, NotesPerDay: 10, RepliesPerHour: 1, RepliesPerHourFree: 1}
	nr.svc.SetNoteConfig(cfg)
	key := uuid.NewString()
	send := func(body, k string) noteResp {
		req := httptest.NewRequest("POST", "/v1/notes", strings.NewReader(body))
		req.Header.Set("X-User-Id", ntAlice.String())
		req.Header.Set("Idempotency-Key", k)
		w := httptest.NewRecorder()
		nr.router.ServeHTTP(w, req)
		r := noteResp{code: w.Code, body: w.Body.Bytes()}
		_ = json.Unmarshal(r.body, &r.env)
		if r.code == 429 && w.Header().Get("Retry-After") == "" {
			t.Fatal("429 without Retry-After")
		}
		return r
	}
	send(`{"text":"one"}`, key).want(t, 201, "")
	send(`{"text":"one"}`, key).want(t, 201, "") // replay: same note, no limit spent
	send(`{"text":"two"}`, key).want(t, 409, "IDEMPOTENCY_KEY_REUSED")
	send(`{"text":"three"}`, uuid.NewString()).want(t, 429, "RATE_LIMITED")
	send(`{"text":"four"}`, "not-a-uuid").want(t, 400, "INVALID_IDEMPOTENCY_KEY")
}

// POST /v1/posts never creates a note: content_type=note is refused before
// any lookup, like every type the ordinary create does not make.
func TestCreatePostRefusesNoteContentType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/posts", strings.NewReader(`{"text":"hello","visibility":"public","content_type":"note"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("X-User-Id", uuid.NewString())
	c.Request.Header.Set("Idempotency-Key", uuid.NewString())
	(&Handler{}).CreatePost(c)
	if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "INVALID_CONTENT_TYPE" {
		t.Fatalf("content_type note: %d %s", rec.Code, rec.Body.String())
	}
}

// ── Golden fixtures ─────────────────────────────────────────────────────

func noteContracts(t *testing.T) map[string][]byte {
	nr := newNoteRouter(t, true)
	nr.seed()
	out := map[string][]byte{}
	get := func(name, method, path string, user *uuid.UUID, body string, key bool, status int) {
		r := nr.call(t, method, path, user, body, key)
		if r.code != status {
			t.Fatalf("%s: %d %s", name, r.code, r.body)
		}
		out[name] = r.body
	}
	get("note.json", "GET", "/v1/notes/"+ntTop.String(), &ntBob, "", false, 200)
	get("note_list.json", "GET", "/v1/notes/"+ntTop.String()+"/replies?limit=20", &ntBob, "", false, 200)
	get("eligibility.json", "GET", "/v1/notes/me/eligibility", &ntBob, "", false, 200)
	get("error_premium_required.json", "POST", "/v1/notes", &ntBob, `{"text":"hello"}`, true, 403)
	return out
}

func TestNoteContracts(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "contracts", "notes")
	update := os.Getenv("UPDATE_CONTRACTS") == "1"
	if update {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, raw := range noteContracts(t) {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := json.Indent(&buf, bytes.TrimSpace(raw), "", "  "); err != nil {
				t.Fatal(err)
			}
			got := append(buf.Bytes(), '\n')
			path := filepath.Join(dir, name)
			if update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read fixture: %v (UPDATE_CONTRACTS=1 to generate)", err)
			}
			if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
				t.Fatalf("fixture %s is stale.\n--- want\n%s\n--- got\n%s", name, want, got)
			}
		})
	}
}

// The fixtures carry what the contract says a Note always has.
func TestNoteFixturesMeanWhatTheContractSays(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "contracts", "notes")
	read := func(name string) map[string]any {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		var env map[string]any
		if err := json.Unmarshal(b, &env); err != nil {
			t.Fatal(err)
		}
		return env
	}
	note := read("note.json")["data"].(map[string]any)
	for _, k := range []string{"id", "author_id", "content_type", "text", "media", "created_at", "edited_at",
		"reply_to_id", "conversation_id", "quote_of_id", "reply_policy", "reply_count", "like_count",
		"repost_count", "viewer_has_liked", "viewer_has_reposted", "viewer_can_reply", "author", "quoted_note"} {
		if _, ok := note[k]; !ok {
			t.Errorf("note.json lacks %q", k)
		}
	}
	if note["content_type"] != "note" || note["conversation_id"] != note["id"] {
		t.Fatalf("note.json: content_type %v conversation_id %v", note["content_type"], note["conversation_id"])
	}
	quoted, _ := note["quoted_note"].(map[string]any)
	if quoted == nil || quoted["quoted_note"] != nil {
		t.Fatalf("quoted_note must be one level deep: %v", note["quoted_note"])
	}
	list := read("note_list.json")["data"].(map[string]any)
	if items, _ := list["items"].([]any); len(items) != 2 {
		t.Fatalf("note_list.json items: %v", list["items"])
	}
	if _, ok := list["next_cursor"]; !ok {
		t.Fatal("note_list.json lacks next_cursor")
	}
	el := read("eligibility.json")["data"].(map[string]any)
	if el["can_post"] != false || el["can_reply"] != true || el["reason"] != "premium_required" {
		t.Fatalf("eligibility.json: %v", el)
	}
	if e := read("error_premium_required.json")["error"].(map[string]any); e["code"] != CodePremiumRequired {
		t.Fatalf("error fixture: %v", e)
	}
}
