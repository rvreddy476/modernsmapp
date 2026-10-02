package http

// Going-live eligibility and chat authors over httptest (2 Oct 2026): status
// codes, error codes and the golden fixtures the web and Android lanes code
// against (testdata/contracts/live):
//
//	eligibility_open_not_eligible.json  GET /eligibility, open mode, two requirements short
//	eligibility_pilot.json              GET /eligibility, pilot mode, not on the list
//	error_live_not_eligible.json        the 403 of POST /streams (and /start, /ingress)
//	chat_list.json                      GET /streams/:id/chat with `author`
//
//	UPDATE_CONTRACTS=1 go test ./internal/http -run 'TestEligibilityContract|TestChatListContract'
//
// regenerates them; review the diff.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/livekit"
	"github.com/atpost/live-service-v2/internal/service"
	"github.com/atpost/live-service-v2/internal/store/postgres"
	"github.com/atpost/live-service-v2/internal/storetest"
)

// userFacts is what the other services say about one user.
type userFacts struct {
	email     bool // an address on file, verified
	phone     bool
	dob       string // YYYY-MM-DD; "" = none
	age       time.Duration
	inactive  bool
	posts     int
	followers int
}

// stubFacts is every eligibility source; down fails every read. A user
// without an entry does not exist.
type stubFacts struct {
	users map[uuid.UUID]userFacts
	down  bool
}

var errStubDown = errors.New("down")

func (s *stubFacts) EmailVerified(_ context.Context, id uuid.UUID) (bool, error) {
	if s.down {
		return false, errStubDown
	}
	return s.users[id].email, nil
}

func (s *stubFacts) PhoneVerified(_ context.Context, id uuid.UUID) (bool, error) {
	if s.down {
		return false, errStubDown
	}
	return s.users[id].phone, nil
}

func (s *stubFacts) BirthDate(_ context.Context, id uuid.UUID) (*time.Time, bool, error) {
	if s.down {
		return nil, false, errStubDown
	}
	u, ok := s.users[id]
	if !ok {
		return nil, false, nil
	}
	if u.dob == "" {
		return nil, true, nil
	}
	d, _ := time.Parse("2006-01-02", u.dob)
	return &d, true, nil
}

func (s *stubFacts) Account(_ context.Context, id uuid.UUID) (service.AccountInfo, error) {
	if s.down {
		return service.AccountInfo{}, errStubDown
	}
	u, ok := s.users[id]
	if !ok {
		return service.AccountInfo{}, nil
	}
	return service.AccountInfo{Found: true, Active: !u.inactive, CreatedAt: time.Now().Add(-u.age)}, nil
}

func (s *stubFacts) PostCount(_ context.Context, id uuid.UUID) (int, error) {
	if s.down {
		return 0, errStubDown
	}
	return s.users[id].posts, nil
}

func (s *stubFacts) FollowerCount(_ context.Context, id uuid.UUID) (int, error) {
	if s.down {
		return 0, errStubDown
	}
	return s.users[id].followers, nil
}

var (
	elPilot   = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	elReady   = uuid.MustParse("33333333-3333-4333-8333-333333333333") // meets everything
	elAlmost  = uuid.MustParse("44444444-4444-4444-8444-444444444444") // 2 days old, 1 post, 4 followers
	elMod     = uuid.MustParse("66666666-6666-4666-8666-666666666666")
	elStream  = uuid.MustParse("a1a1a1a1-a1a1-4a1a-8a1a-a1a1a1a1a1a1")
	elAvatar  = "/v1/media/77777777-7777-4777-8777-777777777777/serve/avatar"
	elChatAt  = time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	elMsgHost = uuid.MustParse("d1d1d1d1-d1d1-4d1d-8d1d-d1d1d1d1d1d1")
	elMsgMod  = uuid.MustParse("d2d2d2d2-d2d2-4d2d-8d2d-d2d2d2d2d2d2")
	elMsgFan  = uuid.MustParse("d3d3d3d3-d3d3-4d3d-8d3d-d3d3d3d3d3d3")
	elMsgAnon = uuid.MustParse("d4d4d4d4-d4d4-4d4d-8d4d-d4d4d4d4d4d4")
)

type eligRig struct {
	*userRig
	facts *stubFacts
}

// newEligRig: the service in `mode` with the default requirements (email
// verified asked for, phone verified not) and the default cap (200 viewers
// until 3 completed streams); elPilot is the pilot. Nobody in the rig has a
// verified phone, so a phone requirement that crept back in would show.
func newEligRig(t *testing.T, mode string) *eligRig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := storetest.New()
	blocked := map[uuid.UUID]bool{}
	facts := &stubFacts{users: map[uuid.UUID]userFacts{
		elReady:  {email: true, dob: "1990-05-17", age: 30 * 24 * time.Hour, posts: 5},
		elAlmost: {email: true, dob: "1990-05-17", age: 2*24*time.Hour + time.Hour, posts: 1, followers: 4},
	}}
	cfg, err := service.EligibilityConfigFromEnv(func(k string) string {
		if k == "LIVE_ACCESS_MODE" {
			return mode
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Emails, cfg.Phones, cfg.BirthDates, cfg.Accounts, cfg.Posts, cfg.Followers = facts, facts, facts, facts, facts, facts
	lk := &ingressLK{held: map[string]*livekit.Ingress{}}
	svc := service.New(store, lk, relGraph{blocked: blocked}, nil, service.Config{
		PilotUserIDs: []uuid.UUID{elPilot},
		Profiles: stubProfiles{
			elPilot: {Name: "Asha Rao", Handle: "asha", AvatarURL: elAvatar},
			elMod:   {Name: "కిరణ్", Handle: "kiran"},
			elReady: {Handle: "ben"},
		},
		Eligibility: cfg,
	})
	h := New(svc).WithInternalKey(rigKey)
	r := gin.New()
	h.RegisterRoutes(r)
	return &eligRig{userRig: &userRig{r: r, store: store, pilot: elPilot, blocked: blocked}, facts: facts}
}

func errDetails(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var env struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("not an error envelope: %s", rec.Body.String())
	}
	return env.Error.Details
}

func reqStates(t *testing.T, raw any) string {
	t.Helper()
	list, ok := raw.([]any)
	if !ok {
		t.Fatalf("requirements is not a list: %v", raw)
	}
	var out []string
	for _, r := range list {
		m := r.(map[string]any)
		state := "null"
		if b, ok := m["met"].(bool); ok {
			state = map[bool]string{true: "true", false: "false"}[b]
		} else if _, present := m["met"]; !present {
			t.Fatalf("a requirement without met: %v", m)
		}
		out = append(out, m["key"].(string)+"="+state)
	}
	return strings.Join(out, ",")
}

const elPath = "/v1/livestream/eligibility"

// TestEligibilityContract pins the two eligibility answers and the 403.
func TestEligibilityContract(t *testing.T) {
	open := newEligRig(t, "open")
	rec := open.call(http.MethodGet, elPath, elAlmost, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("open: %d %s", rec.Code, rec.Body.String())
	}
	matchFixture(t, "eligibility_open_not_eligible.json", rec.Body.Bytes())

	rec = open.call(http.MethodPost, "/v1/livestream/streams", elAlmost, map[string]string{"title": "x"}, nil)
	if rec.Code != http.StatusForbidden || errCode(rec) != "LIVE_NOT_ELIGIBLE" {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	matchFixture(t, "error_live_not_eligible.json", rec.Body.Bytes())

	pilot := newEligRig(t, "")
	rec = pilot.call(http.MethodGet, elPath, elReady, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("pilot: %d %s", rec.Code, rec.Body.String())
	}
	matchFixture(t, "eligibility_pilot.json", rec.Body.Bytes())
}

func TestEligibilityRoute(t *testing.T) {
	// Signed in only.
	open := newEligRig(t, "open")
	if rec := open.call(http.MethodGet, elPath, uuid.Nil, nil, nil); rec.Code != http.StatusUnauthorized || errCode(rec) != "UNAUTHORIZED" {
		t.Fatalf("signed out: %d %s", rec.Code, rec.Body.String())
	}
	// The gateway's internal key, like every v1 route.
	req := httptest.NewRequest(http.MethodGet, elPath, nil)
	req.Header.Set("X-User-Id", elReady.String())
	rec := httptest.NewRecorder()
	open.r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("without the internal key: %d", rec.Code)
	}

	// Open mode, eligible: no pilot_only; viewer_cap while the cap applies.
	d := data(t, open.call(http.MethodGet, elPath, elReady, nil, nil))
	if d["mode"] != "open" || d["eligible"] != true || d["viewer_cap"] != float64(200) {
		t.Fatalf("eligible: %v", d)
	}
	if _, has := d["pilot_only"]; has {
		t.Fatalf("pilot_only in open mode: %v", d)
	}
	if got := reqStates(t, d["requirements"]); got != "email_verified=true,adult=true,account_age=true,activity=true,good_standing=true" {
		t.Fatalf("requirements: %s", got)
	}
	// A pilot user in open mode: eligible whatever the requirements say (no
	// account exists for elPilot in this rig).
	d = data(t, open.call(http.MethodGet, elPath, elPilot, nil, nil))
	if d["eligible"] != true || reqStates(t, d["requirements"]) != "email_verified=false,adult=false,account_age=false,activity=false,good_standing=false" {
		t.Fatalf("pilot in open mode: %v", d)
	}
	// Unknown is null on the wire, never false, and never eligible; the read
	// itself still answers 200.
	open.facts.down = true
	stranger := uuid.New()
	rec = open.call(http.MethodGet, elPath, stranger, nil, nil)
	d = data(t, rec)
	if rec.Code != http.StatusOK || d["eligible"] != false {
		t.Fatalf("sources down: %d %v", rec.Code, d)
	}
	if got := reqStates(t, d["requirements"]); got != "email_verified=null,adult=null,account_age=null,activity=null,good_standing=null" {
		t.Fatalf("sources down: %s", got)
	}
	if !strings.Contains(rec.Body.String(), `"met":null`) {
		t.Fatalf("unknown is not null on the wire: %s", rec.Body.String())
	}

	// Pilot mode (the default): the list decides, pilot_only for the others.
	pilot := newEligRig(t, "")
	d = data(t, pilot.call(http.MethodGet, elPath, elPilot, nil, nil))
	if d["mode"] != "pilot" || d["eligible"] != true {
		t.Fatalf("pilot user: %v", d)
	}
	if _, has := d["pilot_only"]; has {
		t.Fatalf("pilot_only for a pilot user: %v", d)
	}
	d = data(t, pilot.call(http.MethodGet, elPath, elAlmost, nil, nil))
	if d["eligible"] != false || d["pilot_only"] != true ||
		reqStates(t, d["requirements"]) != "email_verified=true,adult=true,account_age=false,activity=false,good_standing=true" {
		t.Fatalf("stranger in pilot mode: %v", d)
	}
}

// TestGoLiveEnforcementCodes: what create, start and ingress answer in each
// mode.
func TestGoLiveEnforcementCodes(t *testing.T) {
	type call struct {
		name, method string
		path         func(id uuid.UUID) string
		body         any
		okCode       int
	}
	calls := []call{
		{"create", http.MethodPost, func(uuid.UUID) string { return "/v1/livestream/streams" }, map[string]string{"title": "x", "source": "encoder"}, http.StatusCreated},
		{"start", http.MethodPost, func(id uuid.UUID) string { return "/v1/livestream/streams/" + id.String() + "/start" }, nil, http.StatusOK},
		{"ingress", http.MethodPost, func(id uuid.UUID) string { return "/v1/livestream/streams/" + id.String() + "/ingress" }, nil, http.StatusOK},
	}
	// A scheduled encoder stream of `owner`.
	seed := func(e *eligRig, owner uuid.UUID) uuid.UUID {
		st := e.store.AddStreamStatus(owner, postgres.StatusScheduled)
		e.store.Streams[st.ID].Source = postgres.SourceEncoder
		return st.ID
	}

	for _, c := range calls {
		t.Run("pilot/"+c.name, func(t *testing.T) {
			e := newEligRig(t, "pilot")
			// Not on the list: LIVE_NOT_ENABLED, even meeting everything.
			rec := e.call(c.method, c.path(seed(e, elReady)), elReady, c.body, nil)
			if rec.Code != http.StatusForbidden || errCode(rec) != "LIVE_NOT_ENABLED" {
				t.Fatalf("stranger: %d %s", rec.Code, rec.Body.String())
			}
			// On the list: through, with every source down.
			e.facts.down = true
			rec = e.call(c.method, c.path(seed(e, elPilot)), elPilot, c.body, nil)
			if rec.Code != c.okCode {
				t.Fatalf("pilot: %d %s", rec.Code, rec.Body.String())
			}
			e.store.PlatformBans[elPilot] = postgres.PlatformBan{UserID: elPilot}
			rec = e.call(c.method, c.path(seed(e, elPilot)), elPilot, c.body, nil)
			if rec.Code != http.StatusForbidden || errCode(rec) != "LIVE_BANNED" {
				t.Fatalf("banned pilot: %d %s", rec.Code, rec.Body.String())
			}
		})
		t.Run("open/"+c.name, func(t *testing.T) {
			e := newEligRig(t, "open")
			rec := e.call(c.method, c.path(seed(e, elReady)), elReady, c.body, nil)
			if rec.Code != c.okCode {
				t.Fatalf("eligible: %d %s", rec.Code, rec.Body.String())
			}
			// Not eligible: 403 LIVE_NOT_ELIGIBLE with what is missing.
			rec = e.call(c.method, c.path(seed(e, elAlmost)), elAlmost, c.body, nil)
			if rec.Code != http.StatusForbidden || errCode(rec) != "LIVE_NOT_ELIGIBLE" {
				t.Fatalf("not eligible: %d %s", rec.Code, rec.Body.String())
			}
			if got := reqStates(t, errDetails(t, rec)["requirements"]); got != "account_age=false,activity=false" {
				t.Fatalf("details.requirements: %s", got)
			}
			// A requirement that cannot be checked: 503, fail closed.
			e.facts.down = true
			stranger := uuid.New()
			rec = e.call(c.method, c.path(seed(e, stranger)), stranger, c.body, nil)
			if rec.Code != http.StatusServiceUnavailable || errCode(rec) != "AUTHORITY_UNAVAILABLE" {
				t.Fatalf("unknown: %d %s", rec.Code, rec.Body.String())
			}
			e.facts.down = false
			// Live-banned: LIVE_BANNED, as before.
			banned := uuid.New()
			e.facts.users[banned] = e.facts.users[elReady]
			e.store.PlatformBans[banned] = postgres.PlatformBan{UserID: banned}
			rec = e.call(c.method, c.path(seed(e, banned)), banned, c.body, nil)
			if rec.Code != http.StatusForbidden || errCode(rec) != "LIVE_BANNED" {
				t.Fatalf("banned: %d %s", rec.Code, rec.Body.String())
			}
			// The pilot list passes in open mode too.
			rec = e.call(c.method, c.path(seed(e, elPilot)), elPilot, c.body, nil)
			if rec.Code != c.okCode {
				t.Fatalf("pilot in open mode: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestViewerCapOnTheWire: 403 STREAM_FULL for a new viewer of a full capped
// stream, and viewer_cap on the host's row only.
func TestViewerCapOnTheWire(t *testing.T) {
	e := newEligRig(t, "pilot")
	st := e.store.AddStream(elPilot)
	base := "/v1/livestream/streams/" + st.ID.String()
	inRoom := uuid.New()
	e.store.Presence[st.ID] = map[uuid.UUID]bool{inRoom: true}
	e.store.Mods[st.ID] = []uuid.UUID{elMod}
	token := func(u uuid.UUID) *httptest.ResponseRecorder {
		return e.call(http.MethodGet, base+"/viewer-token", u, nil, nil)
	}

	e.store.Streams[st.ID].ViewerCount = 199
	if rec := token(uuid.New()); rec.Code != http.StatusOK {
		t.Fatalf("199 of 200: %d %s", rec.Code, rec.Body.String())
	}
	e.store.Streams[st.ID].ViewerCount = 200
	rec := token(uuid.New())
	if rec.Code != http.StatusForbidden || errCode(rec) != "STREAM_FULL" {
		t.Fatalf("200 of 200: %d %s", rec.Code, rec.Body.String())
	}
	for name, u := range map[string]uuid.UUID{"host": elPilot, "moderator": elMod, "viewer in the room": inRoom} {
		if rec := token(u); rec.Code != http.StatusOK {
			t.Fatalf("%s of a full stream: %d %s", name, rec.Code, rec.Body.String())
		}
	}

	// viewer_cap: the host's row has it, a viewer's and a signed-out read do not.
	if d := data(t, e.call(http.MethodGet, base, elPilot, nil, nil)); d["viewer_cap"] != float64(200) {
		t.Fatalf("host row: %v", d["viewer_cap"])
	}
	for _, u := range []uuid.UUID{uuid.New(), uuid.Nil} {
		if d := data(t, e.call(http.MethodGet, base, u, nil, nil)); d["viewer_cap"] != nil {
			t.Fatalf("a viewer's row carries viewer_cap: %v", d["viewer_cap"])
		}
	}
	rec = e.call(http.MethodPost, "/v1/livestream/streams", elPilot, map[string]string{"title": "next"}, nil)
	if rec.Code != http.StatusCreated || data(t, rec)["viewer_cap"] != float64(200) {
		t.Fatalf("created row: %d %s", rec.Code, rec.Body.String())
	}
}

// chatSeed puts a stream on air with a moderator, a founding-creator host
// and four messages at fixed times.
func chatSeed(e *eligRig) {
	started := elChatAt
	e.store.Streams[elStream] = &postgres.LiveStream{
		ID: elStream, CreatorUserID: elPilot, LiveKitRoom: "stream_" + uuid.NewSHA1(uuid.Nil, elStream[:]).String(),
		Title: "Friday jam, live", Status: postgres.StatusLive, Visibility: "public",
		Source: postgres.SourceDevice, Orientation: postgres.OrientationLandscape,
		StartedAt: &started, StatusChangedAt: elChatAt, CreatedAt: elChatAt, UpdatedAt: elChatAt,
	}
	e.store.Mods[elStream] = []uuid.UUID{elMod}
	e.store.Badges[elPilot] = &storetest.BadgeRow{Badge: postgres.BadgeFoundingCreator, GrantedAt: elChatAt.Add(-48 * time.Hour)}
	for i, m := range []struct {
		id, user uuid.UUID
		text     string
	}{
		{elMsgHost, elPilot, "Welcome everyone 🎸"},
		{elMsgMod, elMod, "నమస్తే! Requests in chat 👨‍👩‍👧‍👦"},
		{elMsgFan, elReady, "नमस्ते 🇮🇳"},
		{elMsgAnon, elAlmost, "first time here"},
	} {
		e.store.Messages[m.id] = &postgres.ChatMessage{
			ID: m.id, StreamID: elStream, UserID: m.user, Text: m.text, CreatedAt: elChatAt.Add(time.Duration(i+1) * time.Minute),
		}
	}
}

// TestChatListContract pins GET /chat with `author`: a host with a badge, a
// moderator, a viewer with a handle only and a viewer without a profile.
func TestChatListContract(t *testing.T) {
	e := newEligRig(t, "pilot")
	chatSeed(e)
	rec := e.call(http.MethodGet, "/v1/livestream/streams/"+elStream.String()+"/chat", uuid.Nil, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat: %d %s", rec.Code, rec.Body.String())
	}
	matchFixture(t, "chat_list.json", rec.Body.Bytes())
	// The emoji are on the wire as UTF-8, not as escapes.
	for _, s := range []string{"🎸", "👨‍👩‍👧‍👦", "🇮🇳", "నమస్తే", "नमस्ते", "కిరణ్"} {
		if !strings.Contains(rec.Body.String(), s) {
			t.Fatalf("%q is not in the body as sent", s)
		}
	}
}

// TestChatPostOverHTTP: any Unicode through the JSON body — as raw UTF-8 and
// as \u escapes with surrogate pairs — comes back the same, with the author.
func TestChatPostOverHTTP(t *testing.T) {
	e := newEligRig(t, "pilot")
	chatSeed(e)
	path := "/v1/livestream/streams/" + elStream.String() + "/chat"
	post := func(rawBody string, user uuid.UUID) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(rawBody)))
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		req.Header.Set("X-Internal-Service-Key", rigKey)
		req.Header.Set("X-User-Id", user.String())
		rec := httptest.NewRecorder()
		e.r.ServeHTTP(rec, req)
		return rec
	}
	type row struct {
		Data struct {
			UserID string `json:"user_id"`
			Text   string `json:"text"`
			Author *struct {
				UserID string   `json:"user_id"`
				Name   string   `json:"name"`
				Handle string   `json:"handle"`
				Badges []string `json:"badges"`
				Role   string   `json:"role"`
			} `json:"author"`
		} `json:"data"`
	}
	cases := []struct{ body, want string }{
		{`{"text":"great stream 😀🔥"}`, "great stream 😀🔥"},
		{`{"text":"us 👨‍👩‍👧‍👦 watching"}`, "us 👨‍👩‍👧‍👦 watching"},
		{`{"text":"🇮🇳 🇯🇵"}`, "🇮🇳 🇯🇵"},
		{`{"text":"నమస్తే, ఈ లైవ్ చాలా బాగుంది"}`, "నమస్తే, ఈ లైవ్ చాలా బాగుంది"},
		{`{"text":"नमस्ते, यह लाइव बहुत अच्छा है"}`, "नमस्ते, यह लाइव बहुत अच्छा है"},
		// What a client that escapes non-ASCII sends: surrogate pairs.
		{`{"text":"😀 👨‍👩‍👧 నమస్తే"}`, "😀 👨‍👩‍👧 నమస్తే"},
	}
	for _, c := range cases {
		rec := post(c.body, elMod)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", c.body, rec.Code, rec.Body.String())
		}
		var got row
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Data.Text != c.want {
			t.Fatalf("sent %s, answered %q", c.body, got.Data.Text)
		}
		a := got.Data.Author
		if a == nil || a.UserID != elMod.String() || a.Role != "moderator" || a.Name != "కిరణ్" || a.Handle != "kiran" || a.Badges == nil {
			t.Fatalf("author: %s", rec.Body.String())
		}
		if got.Data.UserID != elMod.String() {
			t.Fatalf("top-level user_id: %s", rec.Body.String())
		}
	}
	// 500 emoji is 2000 bytes and fine; 501 is one character too many.
	if rec := post(`{"text":"`+strings.Repeat("😀", 500)+`"}`, elPilot); rec.Code != http.StatusOK {
		t.Fatalf("500 emoji: %d %s", rec.Code, rec.Body.String())
	}
	if rec := post(`{"text":"`+strings.Repeat("😀", 501)+`"}`, elPilot); rec.Code != http.StatusBadRequest || errCode(rec) != "INVALID_REQUEST" {
		t.Fatalf("501 emoji: %d %s", rec.Code, rec.Body.String())
	}
}
