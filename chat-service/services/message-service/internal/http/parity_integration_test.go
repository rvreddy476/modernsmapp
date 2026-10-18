//go:build integration

package http

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/database"
	chatservice "github.com/atpost/chat-message-service/internal/service"
	pgstore "github.com/atpost/chat-message-service/internal/store/postgres"
	scyllastore "github.com/atpost/chat-message-service/internal/store/scylla"
	"github.com/gin-gonic/gin"
	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Chat parity end to end (lane B1): the real routes, service, Postgres
// (a *_test database) and Scylla (a *_test keyspace), with httptest fakes
// for identity, graph, media, user directory, search and trust-safety.
// It also writes / asserts the golden contract fixtures under
// testdata/contracts/chat/ (go test -tags integration -run Parity
// -update-contracts to rewrite them).

var updateContracts = flag.Bool("update-contracts", false, "rewrite testdata/contracts/chat fixtures")

const parityKey = "parity-internal-key"

// Fixed people so fixtures are stable.
var (
	pA = uuid.MustParse("0a000000-0000-4000-8000-00000000000a") // Asha
	pB = uuid.MustParse("0b000000-0000-4000-8000-00000000000b") // Bilal
	pC = uuid.MustParse("0c000000-0000-4000-8000-00000000000c") // Chen (new account)
	pD = uuid.MustParse("0d000000-0000-4000-8000-00000000000d") // Dev (group member)
)

var people = map[uuid.UUID]struct {
	name, username string
	created        time.Time
}{
	pA: {"Asha", "asha", time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)},
	pB: {"Bilal", "bilal", time.Date(2025, 2, 2, 3, 4, 5, 0, time.UTC)},
	pC: {"Chen", "chen", time.Now().Add(-48 * time.Hour).UTC()},
	pD: {"Dev", "dev", time.Date(2025, 3, 2, 3, 4, 5, 0, time.UTC)},
}

type parityFakes struct {
	mu        sync.Mutex
	reports   []map[string]string
	mediaKind map[string]string
	keywords  map[uuid.UUID][]string
	// Fix pass 2: media-service moderation verdicts (absent key: the field
	// is not sent), accounts the user directory no longer knows, and the
	// trust-safety conversation-report route (404 until switched on).
	mediaSensitive map[string]*bool
	goneUsers      map[uuid.UUID]bool
	convRouteOn    bool
	convReports    []map[string]any
}

func newParityFakes(t *testing.T) (*parityFakes, *httptest.Server) {
	f := &parityFakes{mediaKind: map[string]string{}, keywords: map[uuid.UUID][]string{},
		mediaSensitive: map[string]*bool{}, goneUsers: map[uuid.UUID]bool{}}
	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	// identity settings (policy authority)
	mux.HandleFunc("/identity/v1/users/", func(w http.ResponseWriter, r *http.Request) {
		id := uuid.MustParse(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/identity/v1/users/"), "/settings"))
		f.mu.Lock()
		kw := f.keywords[id]
		f.mu.Unlock()
		if kw == nil {
			kw = []string{}
		}
		writeJSON(w, map[string]any{"data": map[string]any{
			"chat_availability": "enabled", "send_typing_indicators": true, "who_can_see_read_receipts": "everyone",
			"privacy_version": 1, "dm_safe_mode": true, "dm_filtered_keywords": kw, "chat_bubble_style": "ocean",
		}})
	})
	// user directory
	mux.HandleFunc("/users/v1/users/", func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(strings.TrimPrefix(r.URL.Path, "/users/v1/users/"))
		p, ok := people[id]
		f.mu.Lock()
		if f.goneUsers[id] {
			ok = false
		}
		f.mu.Unlock()
		if err != nil || !ok {
			http.NotFound(w, r)
			return
		}
		avatar := "a" + id.String()[1:]
		writeJSON(w, map[string]any{"data": map[string]any{"display_name": p.name, "username": p.username, "created_at": p.created, "avatar_media_id": avatar}})
	})
	// graph
	mux.HandleFunc("/graph/v1/permissions/check", func(w http.ResponseWriter, r *http.Request) {
		actor := uuid.MustParse(r.Header.Get("X-User-Id"))
		action := r.URL.Query().Get("actions")
		decision := map[string]any{"allowed": true}
		if action == "message" && actor == pC {
			decision = map[string]any{"allowed": false, "fallback": "message_request"}
		}
		writeJSON(w, map[string]any{"data": map[string]any{"decisions": map[string]any{action: decision}}})
	})
	mux.HandleFunc("/graph/v1/graph/relationships/batch", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ViewerID  string   `json:"viewer_id"`
			TargetIDs []string `json:"target_ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		out := map[string]any{}
		for _, t := range req.TargetIDs {
			// A and B are mutual follows; everyone else only "follows".
			mutual := (req.ViewerID == pA.String() && t == pB.String()) || (req.ViewerID == pB.String() && t == pA.String())
			out[t] = map[string]any{"follows": true, "followed_by": mutual, "is_connection": false}
		}
		writeJSON(w, out)
	})
	mux.HandleFunc("/graph/v1/graph/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/following-ids") {
			writeJSON(w, map[string]any{"data": map[string]any{"items": []string{pD.String()}, "count": 1}})
			return
		}
		writeJSON(w, map[string]any{"data": map[string]any{}})
	})
	mux.HandleFunc("/graph/v1/internal/graph/blocked-any", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"blocked_user_ids": []string{}})
	})
	mux.HandleFunc("/graph/v1/internal/graph/connections/ensure", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"data": map[string]any{"ok": true}})
	})
	// media
	mux.HandleFunc("/media/v1/media/internal/chat-attachment/reserve", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/media/v1/media/batch", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			IDs []string `json:"ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		out := map[string]any{}
		for _, id := range req.IDs {
			out[id] = map[string]any{"variants": map[string]string{"thumb_300": "https://cdn.atpost.test/" + id + "/thumb_300.webp"}}
		}
		writeJSON(w, map[string]any{"data": out})
	})
	mux.HandleFunc("/media/v1/media/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/media/v1/media/")
		f.mu.Lock()
		kind := f.mediaKind[id]
		sensitive, hasVerdict := f.mediaSensitive[id]
		f.mu.Unlock()
		if kind == "" {
			http.NotFound(w, r)
			return
		}
		rec := map[string]any{"id": id, "file_type": kind}
		switch kind {
		case "image":
			rec["width"], rec["height"] = 1080, 1350
		case "audio":
			rec["duration_ms"] = 4200
		}
		if hasVerdict {
			rec["sensitive"] = sensitive
		}
		writeJSON(w, map[string]any{"data": rec})
	})
	// search
	mux.HandleFunc("/search/v1/search/users", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"data": map[string]any{"items": []any{
			map[string]any{"id": pA.String(), "username": "asha", "display_name": "Asha"},
		}}})
	})
	mux.HandleFunc("/search/internal/v1/search/chat-messages", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		hits := searchHits
		f.mu.Unlock()
		writeJSON(w, map[string]any{"data": hits, "next_cursor": ""})
	})
	// trust-safety
	mux.HandleFunc("/ts/v1/internal/reports/conversations", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		on := f.convRouteOn
		f.mu.Unlock()
		if !on {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-Internal-Service-Key") != parityKey || r.Header.Get("X-User-Id") != "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.convReports = append(f.convReports, body)
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, map[string]any{"data": map[string]any{"created": true}})
	})
	mux.HandleFunc("/ts/v1/internal/reports/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Service-Key") != parityKey || r.Header.Get("X-User-Id") != "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var raw map[string]any
		_ = json.NewDecoder(r.Body).Decode(&raw)
		body := map[string]string{}
		for k, v := range raw {
			if s, ok := v.(string); ok {
				body[k] = s
			}
		}
		f.mu.Lock()
		f.reports = append(f.reports, body)
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, map[string]any{"data": map[string]any{"id": "r1"}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return f, srv
}

var searchHits []map[string]any

type parityEnv struct {
	t      *testing.T
	router *gin.Engine
	pool   *pgxpool.Pool
	svc    *chatservice.Service
	fakes  *parityFakes
	rdb    *redis.Client
}

func (e *parityEnv) do(method, path string, user uuid.UUID, body any) (int, []byte) {
	e.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if user != uuid.Nil {
		req.Header.Set("X-Test-User", user.String())
	}
	req.Header.Set("Idempotency-Key", "k-"+uuid.NewString())
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w.Code, w.Body.Bytes()
}

func (e *parityEnv) must(code int, want int, raw []byte, what string) {
	e.t.Helper()
	if code != want {
		e.t.Fatalf("%s: status %d, want %d: %s", what, code, want, raw)
	}
}

func dataOf(t *testing.T, raw []byte, v any) {
	t.Helper()
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if err := json.Unmarshal(env.Data, v); err != nil {
		t.Fatalf("decode data %s: %v", env.Data, err)
	}
}

func newParityEnv(t *testing.T) *parityEnv {
	dsn := os.Getenv("CHAT_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("CHAT_POSTGRES_DSN is required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing database %q: name must end in _test", cfg.ConnConfig.Database)
	}
	keyspace := os.Getenv("CHAT_SCYLLA_KEYSPACE")
	if !strings.HasSuffix(keyspace, "_test") {
		t.Fatalf("CHAT_SCYLLA_KEYSPACE %q must end in _test", keyspace)
	}
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS chat CASCADE`); err != nil {
		t.Fatal(err)
	}
	if err := pgstore.BootstrapSchema(ctx, pool, database.SetupSQL); err != nil {
		t.Fatal(err)
	}
	// Replay must be idempotent.
	if err := pgstore.BootstrapSchema(ctx, pool, database.SetupSQL); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}

	cluster := gocql.NewCluster(strings.Split(envOr("SCYLLA_HOST", "localhost"), ",")...)
	cluster.Keyspace = keyspace
	cluster.Consistency = gocql.One
	cluster.Timeout = 10 * time.Second
	session, err := cluster.CreateSession()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Close)
	for _, tbl := range []string{"messages", "conversations_by_user", "message_reactions"} {
		if err := session.Query("TRUNCATE " + tbl).Exec(); err != nil {
			t.Fatal(err)
		}
	}
	msgStore := scyllastore.New(session)
	if _, err := msgStore.EnsureMessageColumns(ctx, keyspace); err != nil {
		t.Fatal(err)
	}

	rdb := redis.NewClient(&redis.Options{Addr: envOr("REDIS_ADDR", "localhost:6379"), DB: 15})
	t.Cleanup(func() { _ = rdb.Close() })

	fakes, srv := newParityFakes(t)
	store := pgstore.New(pool)
	svc := chatservice.New(store, msgStore, rdb, nopProducer{}, slog.Default(), time.Second)
	svc.SetUserDirectory(srv.URL+"/users", parityKey)
	svc.SetIdentityAuthority(srv.URL + "/identity")
	svc.SetGraphService(srv.URL + "/graph")
	svc.SetMediaService(srv.URL + "/media")
	svc.SetSearchService(srv.URL + "/search")
	svc.SetTrustSafetyService(srv.URL + "/ts")
	svc.SetHiddenUserStore(store)

	h := New(svc, slog.Default()).WithInternalServiceKey(parityKey)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if u := c.GetHeader("X-Test-User"); u != "" {
			c.Set(userIDKey, u)
		}
		c.Next()
	})
	h.RegisterRoutes(router)
	return &parityEnv{t: t, router: router, pool: pool, svc: svc, fakes: fakes, rdb: rdb}
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

type nopProducer struct{}

func (nopProducer) PublishRaw(context.Context, string, string, json.RawMessage) error { return nil }
func (nopProducer) Close() error                                                      { return nil }

// --- golden fixtures ---

var (
	uuidRe = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	tsRe   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$`)
)

// fixed identities keep their real ids in fixtures; every other uuid is
// mapped to a placeholder by first appearance.
type normalizer struct {
	ids map[string]string
}

func (n *normalizer) str(s string) string {
	if tsRe.MatchString(s) {
		return "2026-10-18T09:30:00Z"
	}
	return uuidRe.ReplaceAllStringFunc(s, func(id string) string {
		for _, p := range []uuid.UUID{pA, pB, pC, pD} {
			if id == p.String() {
				return id
			}
		}
		if m, ok := n.ids[id]; ok {
			return m
		}
		m := fmt.Sprintf("00000000-0000-4000-8000-%012d", len(n.ids)+1)
		n.ids[id] = m
		return m
	})
}

func (n *normalizer) walk(v any, key string) any {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := map[string]any{}
		for _, k := range keys {
			out[k] = n.walk(x[k], k)
		}
		return out
	case []any:
		if key == "members" || key == "user_ids" {
			sort.SliceStable(x, func(i, j int) bool { return fmt.Sprint(x[i]) < fmt.Sprint(x[j]) })
		}
		out := make([]any, len(x))
		for i := range x {
			out[i] = n.walk(x[i], key)
		}
		return out
	case string:
		if key == "next_cursor" && x != "" {
			return "CURSOR"
		}
		if key == "bucket" {
			return "202610"
		}
		return n.str(x)
	default:
		return v
	}
}

func normalizeFixture(t *testing.T, raw []byte) []byte {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("fixture body is not json: %s", raw)
	}
	n := &normalizer{ids: map[string]string{}}
	out, err := json.MarshalIndent(n.walk(v, ""), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(out, '\n')
}

func assertFixture(t *testing.T, name string, raw []byte) {
	t.Helper()
	got := normalizeFixture(t, raw)
	path := filepath.Join("testdata", "contracts", "chat", name+".json")
	if *updateContracts {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("fixture %s missing (run with -update-contracts): %v", name, err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
		t.Errorf("fixture %s drifted from the real handler output:\n--- want\n%s\n--- got\n%s", name, want, got)
	}
}

// --- the scenario ---

func TestParityEndToEnd(t *testing.T) {
	e := newParityEnv(t)
	ctx := context.Background()

	// DM A<->B (both may message), group A(owner)+B+D.
	code, raw := e.do("POST", "/v1/chat/conversations/direct", pA, map[string]string{"other_user_id": pB.String()})
	e.must(code, 200, raw, "create dm")
	var dm struct {
		ID uuid.UUID `json:"id"`
	}
	dataOf(t, raw, &dm)
	code, raw = e.do("POST", "/v1/chat/conversations/group", pA, map[string]any{"title": "Trip", "member_ids": []string{pB.String(), pD.String()}})
	e.must(code, 201, raw, "create group")
	var grp struct {
		ID uuid.UUID `json:"id"`
	}
	dataOf(t, raw, &grp)
	dmPath := "/v1/chat/conversations/" + dm.ID.String()
	grpPath := "/v1/chat/conversations/" + grp.ID.String()

	send := func(user uuid.UUID, path string, body map[string]any) map[string]any {
		t.Helper()
		code, raw := e.do("POST", path+"/messages", user, body)
		e.must(code, 200, raw, fmt.Sprintf("send %v", body["type"]))
		var m map[string]any
		dataOf(t, raw, &m)
		return m
	}

	// --- §2.1 rich types (fixtures from the real send responses) ---
	audio, image := uuid.New(), uuid.New()
	e.fakes.mediaKind[audio.String()] = "audio"
	e.fakes.mediaKind[image.String()] = "image"

	code, raw = e.do("POST", dmPath+"/messages", pA, map[string]any{"type": "voice", "media_id": audio, "duration_ms": 4200, "waveform": []int{0, 40, 255, 12}})
	e.must(code, 200, raw, "voice")
	assertFixture(t, "message_voice", raw)
	if code, raw := e.do("POST", dmPath+"/messages", pA, map[string]any{"type": "voice", "media_id": audio, "duration_ms": 300001}); code != 400 {
		t.Fatalf("voice over 5 minutes: %d %s", code, raw)
	}
	if code, raw := e.do("POST", dmPath+"/messages", pA, map[string]any{"type": "voice", "media_id": image, "duration_ms": 1000}); code != 400 {
		t.Fatalf("voice with an image upload: %d %s", code, raw)
	}

	// Stickers: empty catalogue, admin route gates, then one sticker.
	code, raw = e.do("GET", "/v1/chat/stickers", pA, nil)
	e.must(code, 200, raw, "empty stickers")
	if !strings.Contains(string(raw), `"packs":[]`) {
		t.Fatalf("empty catalogue = %s", raw)
	}
	pack := uuid.MustParse("5a000000-0000-4000-8000-0000000000aa")
	stickerMedia := uuid.New()
	adminReq := func(path string, body any, key string, asUser bool) int {
		rawBody, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", path, bytes.NewReader(rawBody))
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("X-Internal-Service-Key", key)
		}
		if asUser {
			req.Header.Set("X-User-Id", pA.String())
		}
		w := httptest.NewRecorder()
		e.router.ServeHTTP(w, req)
		return w.Code
	}
	packBody := map[string]any{"id": pack.String(), "name": "Momentum basics"}
	if c := adminReq("/internal/v1/chat/stickers/packs", packBody, "", false); c != 401 {
		t.Fatalf("pack without key: %d", c)
	}
	if c := adminReq("/internal/v1/chat/stickers/packs", packBody, parityKey, true); c != 403 {
		t.Fatalf("pack with user identity: %d", c)
	}
	if c := adminReq("/internal/v1/chat/stickers/packs", packBody, parityKey, false); c != 200 {
		t.Fatalf("pack: %d", c)
	}
	sticker := uuid.MustParse("5b000000-0000-4000-8000-0000000000bb")
	if c := adminReq("/internal/v1/chat/stickers", map[string]any{"id": sticker.String(), "pack_id": pack.String(), "media_id": stickerMedia.String(), "width": 512, "height": 512}, parityKey, false); c != 200 {
		t.Fatalf("sticker: %d", c)
	}
	code, raw = e.do("GET", "/v1/chat/stickers", pA, nil)
	e.must(code, 200, raw, "stickers")
	assertFixture(t, "stickers", raw)
	if ok, err := e.svc.ViewerMayAccessChatMedia(ctx, pC, stickerMedia); err != nil || !ok {
		t.Fatalf("catalogue sticker media must be deliverable: %v %v", ok, err)
	}
	code, raw = e.do("POST", dmPath+"/messages", pA, map[string]any{"type": "sticker", "sticker_id": sticker})
	e.must(code, 200, raw, "sticker")
	assertFixture(t, "message_sticker", raw)
	if code, _ := e.do("POST", dmPath+"/messages", pA, map[string]any{"type": "sticker", "sticker_id": uuid.New()}); code != 400 {
		t.Fatalf("unknown sticker: %d", code)
	}

	gifURL := "https://media2.giphy.com/media/v1.Y2lkPTc5/3o7aD2saalBwwftBIY/giphy.gif?cid=79&rid=giphy.gif&ct=g"
	code, raw = e.do("POST", dmPath+"/messages", pA, map[string]any{"type": "gif", "gif": map[string]any{
		"provider": "giphy", "id": "3o7aD2saalBwwftBIY", "url": gifURL, "width": 480, "height": 270,
		"still_url": "https://media2.giphy.com/media/3o7aD2saalBwwftBIY/giphy_s.gif"}})
	e.must(code, 200, raw, "gif")
	var gifResp struct {
		GIF struct {
			URL string `json:"url"`
		} `json:"gif"`
	}
	dataOf(t, raw, &gifResp)
	if gifResp.GIF.URL != gifURL {
		t.Fatalf("gif url must be returned unmodified: %q", gifResp.GIF.URL)
	}
	assertFixture(t, "message_gif", raw)
	for name, g := range map[string]map[string]any{
		"other host":   {"provider": "giphy", "id": "abc", "url": "https://evil.example/giphy.gif", "width": 1, "height": 1},
		"http":         {"provider": "giphy", "id": "abc", "url": "http://media.giphy.com/a.gif", "width": 1, "height": 1},
		"look-alike":   {"provider": "giphy", "id": "abc", "url": "https://media.giphy.com.evil.example/a.gif", "width": 1, "height": 1},
		"bad id":       {"provider": "giphy", "id": "a/b", "url": "https://i.giphy.com/a.gif", "width": 1, "height": 1},
		"tenor":        {"provider": "tenor", "id": "abc", "url": "https://i.giphy.com/a.gif", "width": 1, "height": 1},
		"zero width":   {"provider": "giphy", "id": "abc", "url": "https://i.giphy.com/a.gif", "width": 0, "height": 1},
		"bad still":    {"provider": "giphy", "id": "abc", "url": "https://i.giphy.com/a.gif", "width": 1, "height": 1, "still_url": "https://x.test/a.gif"},
		"media10 host": {"provider": "giphy", "id": "abc", "url": "https://media10.giphy.com/a.gif", "width": 1, "height": 1},
	} {
		if code, raw := e.do("POST", dmPath+"/messages", pA, map[string]any{"type": "gif", "gif": g}); code != 400 {
			t.Errorf("gif %s accepted: %d %s", name, code, raw)
		}
	}

	code, raw = e.do("POST", dmPath+"/messages", pA, map[string]any{"type": "profile", "user_id": pD})
	e.must(code, 200, raw, "profile")
	assertFixture(t, "message_profile", raw)
	code, raw = e.do("POST", dmPath+"/messages", pA, map[string]any{"type": "sound", "sound_id": uuid.New()})
	e.must(code, 200, raw, "sound")
	assertFixture(t, "message_sound", raw)
	code, raw = e.do("POST", dmPath+"/messages", pA, map[string]any{"type": "live", "stream_id": uuid.New()})
	e.must(code, 200, raw, "live")
	assertFixture(t, "message_live", raw)
	code, raw = e.do("POST", dmPath+"/messages", pA, map[string]any{"type": "media", "media_id": image})
	e.must(code, 200, raw, "media")
	assertFixture(t, "message_media", raw)
	post := uuid.New()
	postMsg := send(pA, dmPath, map[string]any{"type": "post", "post_id": post})
	textMsg := send(pA, dmPath, map[string]any{"type": "text", "text": "see you at the station"})

	// History returns the same shapes.
	code, raw = e.do("GET", dmPath+"/messages?limit=20", pB, nil)
	e.must(code, 200, raw, "history")
	for _, want := range []string{`"voice":{"duration_ms":4200,"waveform":[0,40,255,12]}`, `"type":"sticker"`, `"forwarded":false`, `"media":{"kind":"image","width":1080,"height":1350`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("history lacks %s: %s", want, raw)
		}
	}

	// --- §1 inbox: unread, peer, streak, last_message ---
	// A streak of 5 days kept today (both sides sent).
	today := pgstore.StreakDay(time.Now())
	if _, err := e.pool.Exec(ctx, `UPDATE chat.dm_streaks SET days = 5, last_day = $2, a_last_day = $2, b_last_day = $2 WHERE conversation_id = $1`, dm.ID, today); err != nil {
		t.Fatal(err)
	}
	send(pB, grpPath, map[string]any{"type": "text", "text": "hello group"})
	code, raw = e.do("GET", "/v1/chat/conversations", pB, nil)
	e.must(code, 200, raw, "inbox")
	var rows []map[string]any
	dataOf(t, raw, &rows)
	if len(rows) != 2 {
		t.Fatalf("inbox rows = %d: %s", len(rows), raw)
	}
	dmRow := rows[1]
	if rows[0]["id"] == dm.ID.String() {
		dmRow = rows[0]
	}
	if dmRow["unread_count"].(float64) != 9 {
		t.Fatalf("unread_count = %v, want 9 (every message A sent): %s", dmRow["unread_count"], raw)
	}
	peer := dmRow["peer"].(map[string]any)
	if peer["user_id"] != pA.String() || peer["username"] != "asha" || peer["display_name"] != "Asha" {
		t.Fatalf("peer = %v", peer)
	}
	if st, ok := dmRow["streak"].(map[string]any); !ok || st["days"].(float64) != 5 {
		t.Fatalf("streak = %v", dmRow["streak"])
	}
	lm := dmRow["last_message"].(map[string]any)
	if lm["type"] != "text" || lm["preview"] != "see you at the station" || lm["own_status"] != nil {
		t.Fatalf("last_message = %v", lm)
	}
	assertFixture(t, "conversations_list", raw)

	// Reading resets the counter; the sender then sees "read".
	if code, raw := e.do("POST", dmPath+"/read", pB, map[string]string{"message_id": textMsg["msg_id"].(string)}); code != 200 {
		t.Fatalf("read: %d %s", code, raw)
	}
	var unread int
	if err := e.pool.QueryRow(ctx, `SELECT unread_count FROM chat.conversation_members WHERE conversation_id=$1 AND user_id=$2`, dm.ID, pB).Scan(&unread); err != nil || unread != 0 {
		t.Fatalf("unread after read = %d %v", unread, err)
	}
	code, raw = e.do("GET", dmPath, pA, nil)
	e.must(code, 200, raw, "detail")
	if !strings.Contains(string(raw), `"own_status":"read"`) {
		t.Fatalf("sender should see read: %s", raw)
	}
	assertFixture(t, "conversation_detail", raw)

	// --- §1.1 settings: pin first, mute expiry, mute calls, tone, archive ---
	past := time.Now().Add(-time.Hour)
	code, raw = e.do("PUT", grpPath+"/settings", pB, map[string]any{"is_pinned": true, "is_muted": true, "mute_until": past, "mute_calls": true, "alert_tone": "chime"})
	e.must(code, 200, raw, "settings")
	if !strings.Contains(string(raw), `"mute_calls":true`) || !strings.Contains(string(raw), `"alert_tone":"chime"`) {
		t.Fatalf("settings echo = %s", raw)
	}
	if code, _ := e.do("PUT", grpPath+"/settings", pB, map[string]any{"alert_tone": "siren"}); code != 400 {
		t.Fatalf("bad tone accepted")
	}
	if code, _ := e.do("PUT", grpPath+"/settings", pB, map[string]any{"label": "secret"}); code != 400 {
		t.Fatalf("bad label accepted")
	}
	var muteCalls bool
	if err := e.pool.QueryRow(ctx, `SELECT mute_calls FROM chat.conversation_settings WHERE conversation_id=$1 AND user_id=$2`, grp.ID, pB).Scan(&muteCalls); err != nil || !muteCalls {
		t.Fatalf("chat.conversation_settings.mute_calls = %v %v", muteCalls, err)
	}
	send(pA, dmPath, map[string]any{"type": "text", "text": "newest"})
	code, raw = e.do("GET", "/v1/chat/conversations", pB, nil)
	dataOf(t, raw, &rows)
	if rows[0]["id"] != grp.ID.String() || rows[0]["is_pinned"] != true {
		t.Fatalf("pinned conversation must lead: %s", raw)
	}
	if _, muted := rows[0]["is_muted"]; muted {
		t.Fatalf("an expired mute must read as unmuted: %v", rows[0])
	}
	if rows[0]["mute_calls"] != true || rows[0]["alert_tone"] != "chime" {
		t.Fatalf("row settings = %v", rows[0])
	}
	if code, _ := e.do("PUT", dmPath+"/settings", pB, map[string]any{"label": "archived"}); code != 200 {
		t.Fatal("archive")
	}
	dataOf(t, mustGet(e, "/v1/chat/conversations?lane=archived", pB), &rows)
	if len(rows) != 1 || rows[0]["id"] != dm.ID.String() {
		t.Fatalf("archived lane = %v", rows)
	}
	dataOf(t, mustGet(e, "/v1/chat/conversations", pB), &rows)
	if len(rows) != 1 {
		t.Fatalf("primary must exclude archived: %v", rows)
	}
	if code, _ := e.do("PUT", dmPath+"/settings", pB, map[string]any{"label": "none"}); code != 200 {
		t.Fatal("unarchive")
	}
	if code, _ := e.do("GET", "/v1/chat/conversations?lane=nope", pB, nil); code != 400 {
		t.Fatal("bad lane accepted")
	}

	// --- §1.4 shared ---
	assertFixture(t, "shared_posts", mustGet(e, dmPath+"/shared?kind=posts", pB))
	assertFixture(t, "shared_media", mustGet(e, dmPath+"/shared?kind=media", pB))
	if !strings.Contains(string(mustGet(e, dmPath+"/shared?kind=posts", pB)), post.String()) {
		t.Fatal("shared posts lack the post")
	}
	if code, _ := e.do("GET", dmPath+"/shared?kind=posts", pC, nil); code != 404 {
		t.Fatalf("non-member shared: %d", code)
	}

	// --- §2.5 reactions ---
	textID := textMsg["msg_id"].(string)
	textTs := textMsg["ts"].(string)
	if code, raw := e.do("PUT", dmPath+"/messages/"+textID+"/reactions", pB, map[string]any{"emoji": "🔥🔥", "bucket": textMsg["bucket"], "ts": textTs}); code != 400 {
		t.Fatalf("two emoji accepted: %d %s", code, raw)
	}
	for _, u := range []uuid.UUID{pA, pB} {
		if code, raw := e.do("PUT", dmPath+"/messages/"+textID+"/reactions", u, map[string]any{"emoji": "❤️", "bucket": textMsg["bucket"], "ts": textTs}); code != 200 {
			t.Fatalf("react: %d %s", code, raw)
		}
	}
	if code, _ := e.do("PUT", dmPath+"/messages/"+textID+"/reactions", pB, map[string]any{"emoji": "👍🏽", "bucket": textMsg["bucket"], "ts": textTs}); code != 200 {
		t.Fatal("skin-tone emoji refused")
	}
	reactions := mustGet(e, dmPath+"/messages/"+textID+"/reactions", pA)
	if !strings.Contains(string(reactions), `"emoji":"❤️","count":2`) {
		t.Fatalf("reactions = %s", reactions)
	}
	assertFixture(t, "reactions_list", reactions)

	// --- §2.4 forward ---
	code, raw = e.do("POST", dmPath+"/messages/"+textID+"/forward", pB, map[string]any{
		"conversation_ids": []string{grp.ID.String(), uuid.NewString()}, "idempotency_key": "fwd-1"})
	e.must(code, 200, raw, "forward")
	assertFixture(t, "forward_result", raw)
	var fwd chatservice.ForwardResponse
	dataOf(t, raw, &fwd)
	if fwd.Results[0].Outcome != "sent" || fwd.Results[1].Outcome != "unavailable" {
		t.Fatalf("forward outcomes = %+v", fwd.Results)
	}
	if code, raw := e.do("POST", dmPath+"/messages/"+textID+"/forward", pB, map[string]any{
		"conversation_ids": []string{grp.ID.String(), grp.ID.String()}, "idempotency_key": "fwd-2"}); code != 400 {
		t.Fatalf("duplicate targets: %d %s", code, raw)
	}
	code, raw = e.do("GET", grpPath+"/messages?limit=5", pD, nil)
	if !strings.Contains(string(raw), `"forwarded":true`) || strings.Contains(string(raw), pA.String()) {
		t.Fatalf("forwarded copy must be marked and name only the forwarder: %s", raw)
	}

	// --- §2.9 read summary (group) ---
	var grpHistory []map[string]any
	dataOf(t, raw, &grpHistory)
	fwdMsg := grpHistory[0]
	if code, raw := e.do("POST", grpPath+"/read", pD, map[string]string{"message_id": fwdMsg["msg_id"].(string)}); code != 200 {
		t.Fatalf("group read: %d %s", code, raw)
	}
	summary := mustGet(e, grpPath+"/read-summary?message_id="+fwdMsg["msg_id"].(string), pB)
	if !strings.Contains(string(summary), `"count":1`) || !strings.Contains(string(summary), pD.String()) {
		t.Fatalf("read summary = %s", summary)
	}
	assertFixture(t, "read_summary", summary)

	// --- §2.6 report ---
	if code, raw := e.do("POST", dmPath+"/messages/"+textID+"/report", pB, map[string]any{"reason": "harassment", "note": "rude"}); code != 202 {
		t.Fatalf("report: %d %s", code, raw)
	}
	if code, _ := e.do("POST", dmPath+"/messages/"+textID+"/report", pB, map[string]any{"reason": "boring"}); code != 400 {
		t.Fatal("bad reason accepted")
	}
	if code, _ := e.do("POST", dmPath+"/messages/"+textID+"/report", pA, map[string]any{"reason": "spam"}); code != 400 {
		t.Fatal("reporting your own message accepted")
	}
	var reported int
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM chat.outbox_events WHERE event_type='MessageReported'`).Scan(&reported)
	e.fakes.mu.Lock()
	tsReports := len(e.fakes.reports)
	tsEntity := ""
	if tsReports > 0 {
		tsEntity = e.fakes.reports[0]["reporter_id"] + " " + e.fakes.reports[0]["entity_id"] + " " + e.fakes.reports[0]["reason"]
	}
	e.fakes.mu.Unlock()
	if reported != 1 || tsReports != 1 || tsEntity != pB.String()+" "+dm.ID.String()+":"+textID+" harassment" {
		t.Fatalf("report outbox=%d trust-safety=%d (%s)", reported, tsReports, tsEntity)
	}

	// --- §2.3 delete scopes; owner may unsend in a group ---
	code, raw = e.do("DELETE", dmPath+"/messages/"+postMsg["msg_id"].(string), pB, map[string]any{"scope": "me"})
	e.must(code, 200, raw, "delete for me")
	if strings.Contains(string(mustGet(e, dmPath+"/messages?limit=30", pB)), postMsg["msg_id"].(string)) {
		t.Fatal("deleted-for-me message still in B's history")
	}
	if !strings.Contains(string(mustGet(e, dmPath+"/messages?limit=30", pA)), postMsg["msg_id"].(string)) {
		t.Fatal("delete for me must not touch A's history")
	}
	if strings.Contains(string(mustGet(e, dmPath+"/shared?kind=posts", pB)), post.String()) {
		t.Fatal("deleted-for-me post still in B's shared tab")
	}
	dMsg := send(pD, grpPath, map[string]any{"type": "text", "text": "delete me"})
	sub := e.rdb.Subscribe(ctx, "convroom:"+grp.ID.String())
	defer sub.Close()
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatal(err)
	}
	frames := sub.Channel()
	if code, raw := e.do("DELETE", grpPath+"/messages/"+dMsg["msg_id"].(string), pA, map[string]any{}); code != 200 {
		t.Fatalf("owner unsend (no ts — located from the intent): %d %s", code, raw)
	}
	select {
	case f := <-frames:
		if !strings.Contains(f.Payload, `"type":"message_deleted"`) || !strings.Contains(f.Payload, dMsg["msg_id"].(string)) {
			t.Fatalf("frame = %s", f.Payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no message_deleted frame on the conversation room")
	}
	if code, _ := e.do("DELETE", grpPath+"/messages/"+fwdMsg["msg_id"].(string), pD, map[string]any{}); code != 400 {
		t.Fatal("a member unsent someone else's message")
	}

	// --- §1.2 / §1.3 hide and clear for me ---
	if code, _ := e.do("POST", grpPath+"/clear", pD, nil); code != 204 {
		t.Fatal("clear")
	}
	dataOf(t, mustGet(e, grpPath+"/messages", pD), &grpHistory)
	if len(grpHistory) != 0 {
		t.Fatalf("cleared history still visible: %v", grpHistory)
	}
	if code, _ := e.do("DELETE", grpPath+"/for-me", pD, nil); code != 204 {
		t.Fatal("hide")
	}
	dataOf(t, mustGet(e, "/v1/chat/conversations", pD), &rows)
	if len(rows) != 0 {
		t.Fatalf("hidden group still listed: %v", rows)
	}
	if ok, _ := e.svc.GetConversation(ctx, pD, grp.ID); ok == nil {
		t.Fatal("hiding a group must not leave it")
	}
	time.Sleep(5 * time.Millisecond)
	send(pB, grpPath, map[string]any{"type": "text", "text": "back again"})
	dataOf(t, mustGet(e, "/v1/chat/conversations", pD), &rows)
	if len(rows) != 1 || rows[0]["unread_count"].(float64) != 1 {
		t.Fatalf("a new message must bring the group back with only newer messages: %v", rows)
	}
	dataOf(t, mustGet(e, grpPath+"/messages", pD), &grpHistory)
	if len(grpHistory) != 1 || grpHistory[0]["text"] != "back again" {
		t.Fatalf("history after hide = %v", grpHistory)
	}
	if code, _ := e.do("DELETE", grpPath+"/for-me", pC, nil); code != 404 {
		t.Fatal("non-member hide")
	}

	// --- §5 notes ---
	code, raw = e.do("PUT", "/v1/chat/notes/me", pA, map[string]any{"text": "On a train 🚆", "audience": "friends"})
	e.must(code, 200, raw, "note A")
	var noteA struct {
		ID uuid.UUID `json:"id"`
	}
	dataOf(t, raw, &noteA)
	if code, _ := e.do("PUT", "/v1/chat/notes/me", pB, map[string]any{"text": strings.Repeat("a", 61)}); code != 400 {
		t.Fatal("61-character note accepted")
	}
	if code, _ := e.do("PUT", "/v1/chat/notes/me", pB, map[string]any{"text": "dm me at spam.example.com"}); code != 422 {
		t.Fatal("note with a link accepted")
	}
	if code, _ := e.do("PUT", "/v1/chat/notes/me", pD, map[string]any{"text": "friends only", "audience": "friends"}); code != 200 {
		t.Fatal("note D")
	}
	if code, _ := e.do("PUT", "/v1/chat/notes/me", pB, map[string]any{"text": "Back at 6", "audience": "everyone"}); code != 200 {
		t.Fatal("note B")
	}
	feed := mustGet(e, "/v1/chat/notes", pB)
	if !strings.Contains(string(feed), "On a train") || strings.Contains(string(feed), "friends only") {
		t.Fatalf("B sees A's friends note (mutual) but not D's (one-way follow): %s", feed)
	}
	assertFixture(t, "notes_feed", feed)
	// Replying to a note.
	code, raw = e.do("POST", dmPath+"/messages", pB, map[string]any{"type": "text", "text": "where to?", "reply_note_id": noteA.ID})
	e.must(code, 200, raw, "note reply")
	if !strings.Contains(string(raw), `"reply_kind":"note"`) || !strings.Contains(string(raw), `"reply_to_preview":"On a train 🚆"`) {
		t.Fatalf("note reply = %s", raw)
	}
	assertFixture(t, "message_note_reply", raw)
	if code, _ := e.do("DELETE", "/v1/chat/notes/me", pA, nil); code != 204 {
		t.Fatal("delete note")
	}

	// --- §7 requests: a new account's request with a link is filtered ---
	code, raw = e.do("POST", "/v1/chat/conversations/direct", pC, map[string]string{"other_user_id": pB.String()})
	e.must(code, 200, raw, "request create")
	var req struct {
		ID uuid.UUID `json:"id"`
	}
	dataOf(t, raw, &req)
	reqPath := "/v1/chat/conversations/" + req.ID.String()
	if code, raw := e.do("POST", reqPath+"/messages", pC, map[string]any{"type": "gif", "gif": map[string]any{"provider": "giphy", "id": "abc", "url": "https://i.giphy.com/a.gif", "width": 10, "height": 10}}); code != 422 {
		t.Fatalf("a stranger's first message as a gif: %d %s", code, raw)
	}
	send(pC, reqPath, map[string]any{"type": "text", "text": "hi there"})
	dataOf(t, mustGet(e, "/v1/chat/conversations?lane=requests", pB), &rows)
	if len(rows) != 1 || rows[0]["id"] != req.ID.String() {
		t.Fatalf("requests lane = %v", rows)
	}
	dataOf(t, mustGet(e, "/v1/chat/conversations", pB), &rows)
	for _, r := range rows {
		if r["id"] == req.ID.String() {
			t.Fatal("primary must exclude an incoming request")
		}
	}
	filtered := mustGet(e, "/v1/chat/requests?filter=filtered", pB)
	if !strings.Contains(string(filtered), req.ID.String()) || !strings.Contains(string(filtered), `"filtered":true`) {
		t.Fatalf("a 2-day-old account's request must be filtered under safe mode: %s", filtered)
	}
	assertFixture(t, "requests_filtered", filtered)
	if strings.Contains(string(mustGet(e, "/v1/chat/requests", pB)), req.ID.String()) {
		t.Fatal("default requests list must exclude filtered ones")
	}

	// --- §6 search: hits are re-checked against membership / cut-off / requests ---
	e.fakes.mu.Lock()
	searchHits = []map[string]any{
		{"conversation_id": dm.ID.String(), "message_id": textID, "snippet": "see you at the <em>station</em>", "sender_id": pA.String(), "at": textTs},
		{"conversation_id": uuid.NewString(), "message_id": uuid.NewString(), "snippet": "not yours", "sender_id": pC.String(), "at": textTs},
		{"conversation_id": req.ID.String(), "message_id": uuid.NewString(), "snippet": "request", "sender_id": pC.String(), "at": textTs},
	}
	e.fakes.mu.Unlock()
	if code, _ := e.do("GET", "/v1/chat/search?q=s", pB, nil); code != 400 {
		t.Fatal("one-character query accepted")
	}
	result := mustGet(e, "/v1/chat/search?q=asha", pB)
	var sr chatservice.ChatSearchResult
	dataOf(t, result, &sr)
	if len(sr.Messages) != 1 || sr.Messages[0].MessageID.String() != textID || len(sr.People) != 1 || len(sr.Conversations) != 1 {
		t.Fatalf("search = %s", result)
	}
	assertFixture(t, "search_result", result)

	// Typing stop is accepted.
	if code, raw := e.do("POST", dmPath+"/typing", pB, map[string]any{"is_typing": false}); code != 200 {
		t.Fatalf("typing stop: %d %s", code, raw)
	}
}

func mustGet(e *parityEnv, path string, user uuid.UUID) []byte {
	e.t.Helper()
	code, raw := e.do("GET", path, user, nil)
	e.must(code, 200, raw, path)
	return raw
}

// The unread increment rides the durable delivery completion, which the
// repair worker may replay: a replay must never count a message twice.
func TestParityDeliveryReplayCountsUnreadOnce(t *testing.T) {
	e := newParityEnv(t)
	ctx := context.Background()
	code, raw := e.do("POST", "/v1/chat/conversations/direct", pA, map[string]string{"other_user_id": pB.String()})
	e.must(code, 200, raw, "create dm")
	var dm struct {
		ID uuid.UUID `json:"id"`
	}
	dataOf(t, raw, &dm)
	code, raw = e.do("POST", "/v1/chat/conversations/"+dm.ID.String()+"/messages", pA, map[string]any{"type": "text", "text": "once"})
	e.must(code, 200, raw, "send")
	var key, msgType string
	var msgID uuid.UUID
	var ts time.Time
	var members []uuid.UUID
	if err := e.pool.QueryRow(ctx, `SELECT idempotency_key, message_id, message_type, message_ts, member_ids FROM chat.message_delivery_intents WHERE conversation_id = $1`, dm.ID).
		Scan(&key, &msgID, &msgType, &ts, &members); err != nil {
		t.Fatal(err)
	}
	store := pgstore.New(e.pool)
	for i := 0; i < 3; i++ {
		if err := store.ApplyDeliveryParity(ctx, pgstore.DeliveryParity{IdempotencyKey: key, ConversationID: dm.ID, MessageID: msgID,
			SenderID: pA, MemberIDs: members, MessageType: msgType, MessageTS: ts}); err != nil {
			t.Fatal(err)
		}
	}
	var unread int
	if err := e.pool.QueryRow(ctx, `SELECT unread_count FROM chat.conversation_members WHERE conversation_id=$1 AND user_id=$2`, dm.ID, pB).Scan(&unread); err != nil || unread != 1 {
		t.Fatalf("unread after replays = %d %v, want 1", unread, err)
	}
	var senderUnread int
	_ = e.pool.QueryRow(ctx, `SELECT unread_count FROM chat.conversation_members WHERE conversation_id=$1 AND user_id=$2`, dm.ID, pA).Scan(&senderUnread)
	if senderUnread != 0 {
		t.Fatalf("the sender's own message counted as unread: %d", senderUnread)
	}
}
