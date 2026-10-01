package http

// Live surfaces, hearts, supporters and badges over httptest (2 Oct 2026):
// status codes, error codes, and the golden fixtures the two web lanes code
// against (testdata/contracts/live).
//
//	UPDATE_CONTRACTS=1 go test ./internal/http -run 'TestSurfaceContract|TestStreamsStatus|TestEncoderContract'
//
// regenerates the fixtures; review the diff.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/service"
	"github.com/atpost/live-service-v2/internal/store/postgres"
	"github.com/atpost/live-service-v2/internal/storetest"
)

// statusStore (streams_status_test.go) has no creator badges.
func (f *statusStore) BadgesFor(context.Context, []uuid.UUID) (map[uuid.UUID][]string, error) {
	return map[uuid.UUID][]string{}, nil
}

type stubProfiles map[uuid.UUID]service.Profile

func (s stubProfiles) Profiles(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]service.Profile, error) {
	out := map[uuid.UUID]service.Profile{}
	for _, id := range ids {
		if p, ok := s[id]; ok {
			out[id] = p
		}
	}
	return out, nil
}

type stubCategories []service.Category

func (s stubCategories) Categories(context.Context) ([]service.Category, error) { return s, nil }

type stubFollowing map[uuid.UUID][]uuid.UUID

func (s stubFollowing) FollowedCreatorIDs(_ context.Context, viewer uuid.UUID) ([]uuid.UUID, error) {
	return s[viewer], nil
}

var (
	sfAt      = time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	sfAsha    = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	sfBen     = uuid.MustParse("33333333-3333-4333-8333-333333333333")
	sfViewer  = uuid.MustParse("44444444-4444-4444-8444-444444444444")
	sfFan     = uuid.MustParse("66666666-6666-4666-8666-666666666666")
	sfWide    = uuid.MustParse("a1a1a1a1-a1a1-4a1a-8a1a-a1a1a1a1a1a1")
	sfTall    = uuid.MustParse("b2b2b2b2-b2b2-4b2b-8b2b-b2b2b2b2b2b2")
	sfSoon    = uuid.MustParse("c3c3c3c3-c3c3-4c3c-8c3c-c3c3c3c3c3c3")
	sfCoverID = uuid.MustParse("55555555-5555-4555-8555-555555555555")
)

type surfaceRig struct {
	*userRig
	following stubFollowing
}

// newSurfaceRig: asha (a founding creator with a profile) and ben (no
// profile) are pilots; the taxonomy has music and gaming.
func newSurfaceRig(t *testing.T) *surfaceRig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := storetest.New()
	blocked := map[uuid.UUID]bool{}
	following := stubFollowing{}
	svc := service.New(store, stubLK{}, relGraph{blocked: blocked}, nil, service.Config{
		PilotUserIDs: []uuid.UUID{sfAsha, sfBen},
		Profiles: stubProfiles{
			sfAsha: {Name: "Asha Rao", Handle: "asha", AvatarURL: "/v1/media/77777777-7777-4777-8777-777777777777/serve/avatar"},
			sfFan:  {Name: "Kiran", Handle: "kiran", AvatarURL: "/v1/media/88888888-8888-4888-8888-888888888888/serve/avatar"},
		},
		Categories: stubCategories{{Slug: "music", Label: "Music"}, {Slug: "gaming", Label: "Gaming"}},
		Following:  following,
	})
	h := New(svc).WithInternalKey(rigKey)
	r := gin.New()
	h.RegisterRoutes(r)
	store.Badges[sfAsha] = &storetest.BadgeRow{Badge: postgres.BadgeFoundingCreator, GrantedAt: sfAt.Add(-48 * time.Hour)}
	return &surfaceRig{userRig: &userRig{r: r, store: store, pilot: sfAsha, blocked: blocked}, following: following}
}

func (s *surfaceRig) add(id, creator uuid.UUID, status string, mut func(*postgres.LiveStream)) *postgres.LiveStream {
	st := &postgres.LiveStream{
		ID: id, CreatorUserID: creator, LiveKitRoom: "stream_" + uuid.NewSHA1(uuid.Nil, id[:]).String(),
		Title: "t", Status: status, Visibility: "public", Source: postgres.SourceDevice, Orientation: postgres.OrientationLandscape,
		StatusChangedAt: sfAt, CreatedAt: sfAt.Add(-time.Hour), UpdatedAt: sfAt,
	}
	if status == postgres.StatusLive || status == postgres.StatusReconnecting {
		started := sfAt
		st.StartedAt = &started
	}
	if mut != nil {
		mut(st)
	}
	s.store.Streams[id] = st
	return st
}

// seed is the fixture world: asha live and wide in music, ben live and tall
// in gaming, and asha's scheduled stream with two reminders.
func (s *surfaceRig) seed() {
	s.add(sfWide, sfAsha, postgres.StatusLive, func(st *postgres.LiveStream) {
		st.Title, st.Description, st.Category = "Friday jam, live", "Requests in chat", "music"
		st.ViewerCount, st.ViewerPeak, st.HeartCount, st.CoverMediaID = 128, 140, 512, &sfCoverID
	})
	s.add(sfTall, sfBen, postgres.StatusLive, func(st *postgres.LiveStream) {
		st.Title, st.Category, st.Orientation = "Ranked climb", "gaming", postgres.OrientationPortrait
		st.ViewerCount, st.ViewerPeak = 40, 55
		started := sfAt.Add(10 * time.Minute)
		st.StartedAt = &started
	})
	when := time.Date(2099, 1, 1, 18, 30, 0, 0, time.UTC)
	s.add(sfSoon, sfAsha, postgres.StatusScheduled, func(st *postgres.LiveStream) {
		st.Title, st.Description, st.Category, st.ScheduledAt = "New year special", "Countdown stream", "music", &when
		st.CoverMediaID = &sfCoverID
	})
	s.store.Reminders[sfSoon] = map[uuid.UUID]bool{sfViewer: true, sfFan: true}
}

func list(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Data == nil {
		t.Fatalf("not a list envelope: %s", rec.Body.String())
	}
	return env.Data
}

// TestSurfaceContract pins the wire of every new answer.
func TestSurfaceContract(t *testing.T) {
	s := newSurfaceRig(t)
	s.seed()
	get := func(path string, user uuid.UUID) []byte {
		t.Helper()
		rec := s.call(http.MethodGet, path, user, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body.String())
		}
		return rec.Body.Bytes()
	}
	matchFixture(t, "live_list.json", get("/v1/livestream/streams?status=live", uuid.Nil))
	matchFixture(t, "upcoming.json", get("/v1/livestream/streams/upcoming", sfViewer))
	matchFixture(t, "categories_live.json", get("/v1/livestream/categories/live", uuid.Nil))
	matchFixture(t, "creators_live.json", get("/v1/livestream/creators/live", uuid.Nil))
	matchFixture(t, "badges.json", get("/v1/livestream/users/"+sfAsha.String()+"/badges", uuid.Nil))
	// The detail row (GET /streams/:id): a waiting page and a watch page.
	matchFixture(t, "stream_detail_scheduled.json", get("/v1/livestream/streams/"+sfSoon.String(), sfViewer))
	matchFixture(t, "stream_detail_live.json", get("/v1/livestream/streams/"+sfWide.String(), sfViewer))

	rec := s.call(http.MethodPut, "/v1/livestream/streams/"+sfSoon.String()+"/reminder", sfBen, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reminder: %d %s", rec.Code, rec.Body.String())
	}
	matchFixture(t, "reminder.json", rec.Body.Bytes())

	rec = s.call(http.MethodPost, "/v1/livestream/streams/"+sfWide.String()+"/hearts", sfViewer, map[string]int{"count": 12}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("hearts: %d %s", rec.Code, rec.Body.String())
	}
	matchFixture(t, "hearts.json", rec.Body.Bytes())

	// Supporters: a founding creator with a profile on top, then a viewer
	// with no profile, then one who only chatted.
	s.store.Badges[sfFan] = &storetest.BadgeRow{Badge: postgres.BadgeFoundingCreator, GrantedAt: sfAt}
	s.store.Hearts[sfWide] = map[uuid.UUID]*storetest.HeartRow{
		sfFan:    {Hearts: 240, CreatedAt: sfAt},
		sfViewer: {Hearts: 12, CreatedAt: sfAt.Add(time.Minute)},
	}
	for i, m := range []struct {
		user uuid.UUID
		text string
	}{{sfFan, "first!"}, {sfFan, "play the new one"}, {sfBen, "hello from ben"}} {
		id := uuid.NewSHA1(uuid.Nil, []byte{byte(i)})
		s.store.Messages[id] = &postgres.ChatMessage{ID: id, StreamID: sfWide, UserID: m.user, Text: m.text, CreatedAt: sfAt.Add(time.Duration(i) * time.Minute)}
	}
	matchFixture(t, "supporters.json", get("/v1/livestream/streams/"+sfWide.String()+"/supporters", uuid.Nil))
}

func TestCreateOrientationAndCategoryOnTheWire(t *testing.T) {
	s := newSurfaceRig(t)
	rec := s.call(http.MethodPost, "/v1/livestream/streams", sfAsha, map[string]string{"title": "x"}, nil)
	d := data(t, rec)
	if rec.Code != http.StatusCreated || d["orientation"] != "landscape" || d["category"] != "" || d["heart_count"] != float64(0) {
		t.Fatalf("defaults: %d %s", rec.Code, rec.Body.String())
	}
	creator, _ := d["creator"].(map[string]any)
	if creator["user_id"] != sfAsha.String() || creator["name"] != "Asha Rao" || creator["handle"] != "asha" {
		t.Fatalf("creator card on create: %v", d["creator"])
	}
	rec = s.call(http.MethodPost, "/v1/livestream/streams", sfAsha, map[string]string{"title": "x", "orientation": "portrait", "category": "music"}, nil)
	if d := data(t, rec); rec.Code != http.StatusCreated || d["orientation"] != "portrait" || d["category"] != "music" {
		t.Fatalf("portrait + music: %d %s", rec.Code, rec.Body.String())
	}
	n := len(s.store.Streams)
	rec = s.call(http.MethodPost, "/v1/livestream/streams", sfAsha, map[string]string{"title": "x", "orientation": "square"}, nil)
	if rec.Code != http.StatusUnprocessableEntity || errCode(rec) != "VALIDATION_ERROR" {
		t.Fatalf("unknown orientation: %d %s", rec.Code, rec.Body.String())
	}
	rec = s.call(http.MethodPost, "/v1/livestream/streams", sfAsha, map[string]string{"title": "x", "category": "knitting"}, nil)
	if rec.Code != http.StatusUnprocessableEntity || errCode(rec) != "INVALID_CATEGORY" {
		t.Fatalf("unknown category: %d %s", rec.Code, rec.Body.String())
	}
	if len(s.store.Streams) != n {
		t.Fatal("a refused create stored a stream")
	}
}

func TestPatchStreamCodes(t *testing.T) {
	s := newSurfaceRig(t)
	s.seed()
	path := "/v1/livestream/streams/" + sfSoon.String()
	patch := func(user uuid.UUID, p string, raw string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPatch, p, strings.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Service-Key", rigKey)
		if user != uuid.Nil {
			req.Header.Set("X-User-Id", user.String())
		}
		rec := httptest.NewRecorder()
		s.r.ServeHTTP(rec, req)
		return rec
	}
	refusals := []struct {
		name   string
		user   uuid.UUID
		path   string
		body   string
		status int
		code   string
	}{
		{"signed out", uuid.Nil, path, `{"title":"x"}`, 401, "UNAUTHORIZED"},
		{"not the host", sfBen, path, `{"title":"x"}`, 403, "FORBIDDEN"},
		{"unknown stream", sfAsha, "/v1/livestream/streams/" + uuid.NewString(), `{"title":"x"}`, 404, "NOT_FOUND"},
		{"bad id", sfAsha, "/v1/livestream/streams/nope", `{"title":"x"}`, 400, "INVALID_ID"},
		{"live stream", sfAsha, "/v1/livestream/streams/" + sfWide.String(), `{"title":"x"}`, 409, "STREAM_STATE_CONFLICT"},
		{"not an object", sfAsha, path, `["title"]`, 400, "INVALID_REQUEST"},
		{"null body", sfAsha, path, `null`, 400, "INVALID_REQUEST"},
		{"title of the wrong type", sfAsha, path, `{"title":7}`, 400, "INVALID_REQUEST"},
		{"cover not a uuid", sfAsha, path, `{"cover_media_id":"abc"}`, 400, "INVALID_REQUEST"},
		{"scheduled_at not a time", sfAsha, path, `{"scheduled_at":"tomorrow"}`, 400, "INVALID_REQUEST"},
		{"paid", sfAsha, path, `{"visibility":"paid"}`, 422, "VALIDATION_ERROR"},
		{"unknown visibility", sfAsha, path, `{"visibility":"friends"}`, 422, "VALIDATION_ERROR"},
		{"unknown orientation", sfAsha, path, `{"orientation":"square"}`, 422, "VALIDATION_ERROR"},
		{"empty title", sfAsha, path, `{"title":" "}`, 422, "VALIDATION_ERROR"},
		{"unknown category", sfAsha, path, `{"category":"knitting"}`, 422, "INVALID_CATEGORY"},
	}
	before := *s.store.Streams[sfSoon]
	for _, tc := range refusals {
		rec := patch(tc.user, tc.path, tc.body)
		if rec.Code != tc.status || errCode(rec) != tc.code {
			t.Fatalf("%s: %d %s, want %d %s — %s", tc.name, rec.Code, errCode(rec), tc.status, tc.code, rec.Body.String())
		}
	}
	if after := *s.store.Streams[sfSoon]; after.Title != before.Title || after.Visibility != before.Visibility || after.Orientation != before.Orientation || after.CoverMediaID == nil {
		t.Fatalf("a refused PATCH changed the row: %+v", after)
	}

	rec := patch(sfAsha, path, `{"title":"Renamed","description":"d2","category":"gaming","visibility":"followers","orientation":"portrait","scheduled_at":"2099-02-02T10:00:00Z","cover_media_id":null}`)
	d := data(t, rec)
	if rec.Code != http.StatusOK || d["title"] != "Renamed" || d["description"] != "d2" || d["category"] != "gaming" ||
		d["visibility"] != "followers" || d["orientation"] != "portrait" || d["scheduled_at"] != "2099-02-02T10:00:00Z" {
		t.Fatalf("PATCH: %d %s", rec.Code, rec.Body.String())
	}
	if _, has := d["cover_media_id"]; has {
		t.Fatalf("cover_media_id: null did not clear it: %s", rec.Body.String())
	}
	if d["reminder_count"] != float64(2) || d["creator"] == nil {
		t.Fatalf("the PATCH answer is not the full row: %s", rec.Body.String())
	}
	// Absent fields are left alone; scheduled_at: null clears.
	rec = patch(sfAsha, path, `{"scheduled_at":null}`)
	d = data(t, rec)
	if rec.Code != http.StatusOK || d["title"] != "Renamed" || d["category"] != "gaming" || d["orientation"] != "portrait" {
		t.Fatalf("a one-field PATCH changed others: %s", rec.Body.String())
	}
	if _, has := d["scheduled_at"]; has {
		t.Fatalf("scheduled_at: null did not clear it: %s", rec.Body.String())
	}
	// A text field sent as null is left alone, like an absent one.
	rec = patch(sfAsha, path, `{"title":null,"visibility":null}`)
	if d := data(t, rec); rec.Code != http.StatusOK || d["title"] != "Renamed" || d["visibility"] != "followers" {
		t.Fatalf("null text fields: %d %s", rec.Code, rec.Body.String())
	}

	// "" clears: the topic, the description, the cover and the time. The
	// title cannot be emptied.
	rec = patch(sfAsha, path, `{"cover_media_id":"`+sfCoverID.String()+`","scheduled_at":"2099-03-03T10:00:00Z"}`)
	if d := data(t, rec); rec.Code != http.StatusOK || d["cover_media_id"] != sfCoverID.String() || d["scheduled_at"] != "2099-03-03T10:00:00Z" || d["description"] != "d2" || d["category"] != "gaming" {
		t.Fatalf("setting cover and time again: %d %s", rec.Code, rec.Body.String())
	}
	rec = patch(sfAsha, path, `{"category":"","description":"","cover_media_id":"","scheduled_at":""}`)
	d = data(t, rec)
	if rec.Code != http.StatusOK || d["category"] != "" || d["description"] != "" || d["title"] != "Renamed" {
		t.Fatalf(`clearing with "": %d %s`, rec.Code, rec.Body.String())
	}
	for _, gone := range []string{"cover_media_id", "scheduled_at"} {
		if _, has := d[gone]; has {
			t.Fatalf(`%s: "" did not clear it: %s`, gone, rec.Body.String())
		}
	}
	if rec := patch(sfAsha, path, `{"title":""}`); rec.Code != http.StatusUnprocessableEntity || errCode(rec) != "VALIDATION_ERROR" {
		t.Fatalf("emptying the title: %d %s", rec.Code, rec.Body.String())
	}
	if got := s.store.Streams[sfSoon].Title; got != "Renamed" {
		t.Fatalf("the title was emptied: %q", got)
	}
}

// TestStreamDetailRow: GET /streams/:id carries everything the watch and
// waiting pages read, in every status.
func TestStreamDetailRow(t *testing.T) {
	s := newSurfaceRig(t)
	s.seed()
	row := func(id, caller uuid.UUID) map[string]any {
		t.Helper()
		rec := s.call(http.MethodGet, "/v1/livestream/streams/"+id.String(), caller, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET: %d %s", rec.Code, rec.Body.String())
		}
		return data(t, rec)
	}
	// Scheduled, for someone who set a reminder.
	d := row(sfSoon, sfViewer)
	if d["reminder_set"] != true || d["reminder_count"] != float64(2) || d["orientation"] != "landscape" || d["category"] != "music" || d["heart_count"] != float64(0) {
		t.Fatalf("scheduled detail row: %v", d)
	}
	creator, _ := d["creator"].(map[string]any)
	badges, _ := creator["badges"].([]any)
	if creator["user_id"] != sfAsha.String() || creator["name"] != "Asha Rao" || creator["handle"] != "asha" ||
		creator["avatar_url"] != "/v1/media/77777777-7777-4777-8777-777777777777/serve/avatar" || len(badges) != 1 || badges[0] != "founding_creator" {
		t.Fatalf("creator card: %v", d["creator"])
	}
	if d := row(sfSoon, sfBen); d["reminder_set"] != false || d["reminder_count"] != float64(2) {
		t.Fatalf("for someone without a reminder: %v %v", d["reminder_set"], d["reminder_count"])
	}
	d = row(sfSoon, uuid.Nil)
	if _, has := d["reminder_set"]; has || d["reminder_count"] != float64(2) {
		t.Fatalf("signed out: reminder_set must be absent, reminder_count present: %v", d)
	}
	// Live: the same fields, and a creator without a profile is the id only.
	d = row(sfTall, sfViewer)
	if d["reminder_set"] != false || d["reminder_count"] != float64(0) || d["orientation"] != "portrait" || d["category"] != "gaming" || d["heart_count"] != float64(0) {
		t.Fatalf("live detail row: %v", d)
	}
	if c, _ := d["creator"].(map[string]any); len(c) != 1 || c["user_id"] != sfBen.String() {
		t.Fatalf("a creator without a profile: %v", d["creator"])
	}
	// recording_post_id: absent until the recording is a video, then the id.
	if _, has := d["recording_post_id"]; has {
		t.Fatalf("recording_post_id on a stream with no video: %v", d["recording_post_id"])
	}
	post := uuid.New()
	s.store.Streams[sfTall].RecordingPostID = &post
	if d := row(sfTall, uuid.Nil); d["recording_post_id"] != post.String() {
		t.Fatalf("recording_post_id: %v", d["recording_post_id"])
	}
}

func TestDiscoveryRouteCodes(t *testing.T) {
	s := newSurfaceRig(t)
	s.seed()
	titles := func(path string, user uuid.UUID) []string {
		t.Helper()
		rec := s.call(http.MethodGet, path, user, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body.String())
		}
		var out []string
		for _, row := range list(t, rec) {
			out = append(out, row["title"].(string))
		}
		return out
	}
	eq := func(what string, got []string, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: %v, want %v", what, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: %v, want %v", what, got, want)
			}
		}
	}
	eq("default: by viewers", titles("/v1/livestream/streams", uuid.Nil), "Friday jam, live", "Ranked climb")
	eq("sort=recent", titles("/v1/livestream/streams?sort=recent", uuid.Nil), "Ranked climb", "Friday jam, live")
	eq("orientation", titles("/v1/livestream/streams?status=live&orientation=portrait", uuid.Nil), "Ranked climb")
	eq("category", titles("/v1/livestream/streams?category=music", uuid.Nil), "Friday jam, live")
	eq("following, signed out", titles("/v1/livestream/streams?following=true", uuid.Nil))
	eq("following nobody", titles("/v1/livestream/streams?following=true", sfViewer))
	s.following[sfViewer] = []uuid.UUID{sfBen}
	eq("following ben", titles("/v1/livestream/streams?following=true", sfViewer), "Ranked climb")
	eq("following=1", titles("/v1/livestream/streams?following=1", sfViewer), "Ranked climb")
	eq("following=false", titles("/v1/livestream/streams?following=false", sfViewer), "Friday jam, live", "Ranked climb")
	eq("upcoming", titles("/v1/livestream/streams/upcoming", uuid.Nil), "New year special")
	eq("upcoming, following", titles("/v1/livestream/streams/upcoming?following=true", sfViewer))
	eq("a creator's live tab", titles("/v1/livestream/users/"+sfAsha.String()+"/streams?status=live", uuid.Nil), "Friday jam, live")
	eq("a creator's upcoming", titles("/v1/livestream/users/"+sfAsha.String()+"/streams?status=upcoming", uuid.Nil), "New year special")
	eq("a creator's past", titles("/v1/livestream/users/"+sfAsha.String()+"/streams?status=past", uuid.Nil))
	// The creator's own upcoming list has every unstarted stream, the
	// timeless ones last; nobody else sees those.
	s.add(uuid.New(), sfAsha, postgres.StatusScheduled, func(st *postgres.LiveStream) { st.Title = "Draft, no time" })
	eq("the creator's own unstarted", titles("/v1/livestream/users/"+sfAsha.String()+"/streams?status=upcoming", sfAsha), "New year special", "Draft, no time")
	eq("someone else's view of it", titles("/v1/livestream/users/"+sfAsha.String()+"/streams?status=upcoming", sfBen), "New year special")
	// categories/live and creators/live take ?orientation=.
	slugs := func(path string) []string {
		t.Helper()
		var out []string
		for _, row := range list(t, s.call(http.MethodGet, path, uuid.Nil, nil, nil)) {
			out = append(out, row["slug"].(string))
		}
		return out
	}
	eq("categories, any orientation", slugs("/v1/livestream/categories/live"), "music", "gaming")
	eq("categories, portrait", slugs("/v1/livestream/categories/live?orientation=portrait"), "gaming")
	eq("categories, landscape", slugs("/v1/livestream/categories/live?orientation=landscape"), "music")
	if rec := s.call(http.MethodGet, "/v1/livestream/creators/live?orientation=portrait", uuid.Nil, nil, nil); len(list(t, rec)) != 1 || list(t, rec)[0]["stream_id"] != sfTall.String() {
		t.Fatalf("portrait creators: %s", rec.Body.String())
	}
	// A viewer who blocked (or is blocked by) the hosts sees nothing.
	s.blocked[sfFan] = true
	eq("blocked viewer", titles("/v1/livestream/streams", sfFan))
	if rec := s.call(http.MethodGet, "/v1/livestream/creators/live", sfFan, nil, nil); len(list(t, rec)) != 0 {
		t.Fatalf("live creators for a blocked viewer: %s", rec.Body.String())
	}
	if rec := s.call(http.MethodGet, "/v1/livestream/categories/live", sfFan, nil, nil); len(list(t, rec)) != 0 {
		t.Fatalf("live categories for a blocked viewer: %s", rec.Body.String())
	}

	for path, want := range map[string][2]any{
		"/v1/livestream/streams?orientation=square":                             {422, "VALIDATION_ERROR"},
		"/v1/livestream/streams?sort=trending":                                  {422, "VALIDATION_ERROR"},
		"/v1/livestream/streams/upcoming?orientation=square":                    {422, "VALIDATION_ERROR"},
		"/v1/livestream/users/" + sfAsha.String() + "/streams?status=scheduled": {400, "INVALID_REQUEST"},
		"/v1/livestream/users/nope/streams":                                     {400, "INVALID_ID"},
		"/v1/livestream/users/nope/badges":                                      {400, "INVALID_ID"},
		"/v1/livestream/categories/live?orientation=square":                     {422, "VALIDATION_ERROR"},
		"/v1/livestream/creators/live?orientation=square":                       {422, "VALIDATION_ERROR"},
	} {
		rec := s.call(http.MethodGet, path, uuid.Nil, nil, nil)
		if rec.Code != want[0] || errCode(rec) != want[1] {
			t.Fatalf("GET %s: %d %s, want %v", path, rec.Code, errCode(rec), want)
		}
	}
	// /streams/upcoming is the listing, never a stream called "upcoming".
	if rec := s.call(http.MethodGet, "/v1/livestream/streams/upcoming", uuid.Nil, nil, nil); errCode(rec) == "INVALID_ID" {
		t.Fatal("/streams/upcoming was routed to /streams/:id")
	}
	// Without the internal key nothing answers.
	req := httptest.NewRequest(http.MethodGet, "/v1/livestream/categories/live", nil)
	rec := httptest.NewRecorder()
	s.r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
		t.Fatalf("a discovery route answered without the internal key: %d", rec.Code)
	}
}

func TestReminderRouteCodes(t *testing.T) {
	s := newSurfaceRig(t)
	s.seed()
	path := "/v1/livestream/streams/" + sfSoon.String() + "/reminder"
	for _, m := range []string{http.MethodPut, http.MethodDelete} {
		if rec := s.call(m, path, uuid.Nil, nil, nil); rec.Code != http.StatusUnauthorized || errCode(rec) != "UNAUTHORIZED" {
			t.Fatalf("%s signed out: %d %s", m, rec.Code, rec.Body.String())
		}
		if rec := s.call(m, "/v1/livestream/streams/"+sfWide.String()+"/reminder", sfBen, nil, nil); rec.Code != http.StatusConflict || errCode(rec) != "STREAM_STATE_CONFLICT" {
			t.Fatalf("%s on a live stream: %d %s", m, rec.Code, rec.Body.String())
		}
		if rec := s.call(m, "/v1/livestream/streams/"+uuid.NewString()+"/reminder", sfBen, nil, nil); rec.Code != http.StatusNotFound {
			t.Fatalf("%s on an unknown stream: %d", m, rec.Code)
		}
	}
	if n := len(s.store.Reminders[sfSoon]); n != 2 {
		t.Fatalf("a refused call changed the reminders: %d", n)
	}
	for i := 0; i < 2; i++ { // idempotent
		rec := s.call(http.MethodPut, path, sfBen, nil, nil)
		if d := data(t, rec); rec.Code != http.StatusOK || d["reminder_set"] != true || d["reminder_count"] != float64(3) || len(d) != 2 {
			t.Fatalf("PUT %d: %d %s", i, rec.Code, rec.Body.String())
		}
	}
	for i := 0; i < 2; i++ {
		rec := s.call(http.MethodDelete, path, sfBen, nil, nil)
		if d := data(t, rec); rec.Code != http.StatusOK || d["reminder_set"] != false || d["reminder_count"] != float64(2) || len(d) != 2 {
			t.Fatalf("DELETE %d: %d %s", i, rec.Code, rec.Body.String())
		}
	}
	// A blocked viewer cannot see the stream, so cannot be reminded of it.
	s.blocked[sfFan] = true
	if rec := s.call(http.MethodDelete, path, sfFan, nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("blocked viewer: %d %s", rec.Code, rec.Body.String())
	}
}

// TestInternalRemindersRoute: internal key always, the paged shape.
func TestInternalRemindersRoute(t *testing.T) {
	s := newSurfaceRig(t)
	s.seed()
	path := "/v1/livestream/internal/streams/" + sfSoon.String() + "/reminders"
	raw := func(key, p string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		if key != "" {
			req.Header.Set("X-Internal-Service-Key", key)
		}
		rec := httptest.NewRecorder()
		s.r.ServeHTTP(rec, req)
		return rec
	}
	for _, key := range []string{"", "wrong"} {
		if rec := raw(key, path); rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), sfViewer.String()) {
			t.Fatalf("key %q: %d %s", key, rec.Code, rec.Body.String())
		}
	}
	// No key configured at all: still refused, never open.
	open := newUserRig(t, "")
	st := open.store.AddStreamStatus(open.pilot, postgres.StatusScheduled)
	req := httptest.NewRequest(http.MethodGet, "/v1/livestream/internal/streams/"+st.ID.String()+"/reminders", nil)
	rec := httptest.NewRecorder()
	open.r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no key configured: %d", rec.Code)
	}

	rec = raw(rigKey, path)
	d := data(t, rec)
	ids, _ := d["user_ids"].([]any)
	if rec.Code != http.StatusOK || len(ids) != 2 || ids[0] != sfViewer.String() || ids[1] != sfFan.String() ||
		d["has_more"] != false || d["next_after"] != sfFan.String() || len(d) != 3 {
		t.Fatalf("page: %d %s", rec.Code, rec.Body.String())
	}
	rec = raw(rigKey, path+"?limit=1")
	d = data(t, rec)
	if ids, _ := d["user_ids"].([]any); len(ids) != 1 || ids[0] != sfViewer.String() || d["has_more"] != true || d["next_after"] != sfViewer.String() {
		t.Fatalf("limit=1: %s", rec.Body.String())
	}
	rec = raw(rigKey, path+"?limit=1&after="+sfViewer.String())
	d = data(t, rec)
	if ids, _ := d["user_ids"].([]any); len(ids) != 1 || ids[0] != sfFan.String() {
		t.Fatalf("after: %s", rec.Body.String())
	}
	rec = raw(rigKey, path+"?after="+sfFan.String())
	d = data(t, rec)
	if ids, ok := d["user_ids"].([]any); !ok || len(ids) != 0 || d["has_more"] != false || d["next_after"] != "" {
		t.Fatalf("past the end: %s", rec.Body.String())
	}
	if rec := raw(rigKey, path+"?after=nope"); rec.Code != http.StatusBadRequest || errCode(rec) != "INVALID_ID" {
		t.Fatalf("bad after: %d %s", rec.Code, rec.Body.String())
	}
	if rec := raw(rigKey, "/v1/livestream/internal/streams/"+uuid.NewString()+"/reminders"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown stream: %d", rec.Code)
	}
}

func TestHeartsRouteCodes(t *testing.T) {
	s := newSurfaceRig(t)
	s.seed()
	path := "/v1/livestream/streams/" + sfWide.String() + "/hearts"
	send := func(p string, user uuid.UUID, body any) *httptest.ResponseRecorder {
		return s.call(http.MethodPost, p, user, body, nil)
	}
	banned, liveBanned, blocked := uuid.New(), uuid.New(), uuid.New()
	_ = s.store.BanFromStream(context.Background(), sfWide, banned, sfAsha, "")
	_ = s.store.AdminSetPlatformBan(context.Background(), liveBanned, true, "x", postgres.AuditEntry{ActorID: uuid.New()})
	s.blocked[blocked] = true

	refusals := []struct {
		name   string
		path   string
		user   uuid.UUID
		body   any
		status int
		code   string
	}{
		{"signed out", path, uuid.Nil, map[string]int{"count": 1}, 401, "UNAUTHORIZED"},
		{"count 0", path, sfViewer, map[string]int{"count": 0}, 422, "VALIDATION_ERROR"},
		{"count 21", path, sfViewer, map[string]int{"count": 21}, 422, "VALIDATION_ERROR"},
		{"negative", path, sfViewer, map[string]int{"count": -5}, 422, "VALIDATION_ERROR"},
		{"no count", path, sfViewer, map[string]int{}, 422, "VALIDATION_ERROR"},
		{"count not a number", path, sfViewer, map[string]string{"count": "many"}, 400, "INVALID_BODY"},
		{"scheduled stream", "/v1/livestream/streams/" + sfSoon.String() + "/hearts", sfViewer, map[string]int{"count": 1}, 409, "STREAM_NOT_LIVE"},
		{"unknown stream", "/v1/livestream/streams/" + uuid.NewString() + "/hearts", sfViewer, map[string]int{"count": 1}, 404, "NOT_FOUND"},
		{"banned from the stream", path, banned, map[string]int{"count": 1}, 403, "BANNED_FROM_STREAM"},
		{"platform live-banned", path, liveBanned, map[string]int{"count": 1}, 403, "LIVE_BANNED"},
		{"blocked", path, blocked, map[string]int{"count": 1}, 404, "NOT_FOUND"},
	}
	for _, tc := range refusals {
		rec := send(tc.path, tc.user, tc.body)
		if rec.Code != tc.status || errCode(rec) != tc.code {
			t.Fatalf("%s: %d %s, want %d %s — %s", tc.name, rec.Code, errCode(rec), tc.status, tc.code, rec.Body.String())
		}
	}
	if got := s.store.Streams[sfWide].HeartCount; got != 512 {
		t.Fatalf("a refused batch counted: %d", got)
	}
	// 60 per 10 s, then 429 with Retry-After.
	for i := 1; i <= 3; i++ {
		rec := send(path, sfViewer, map[string]int{"count": 20})
		if d := data(t, rec); rec.Code != http.StatusOK || d["heart_count"] != float64(512+20*i) || len(d) != 1 {
			t.Fatalf("batch %d: %d %s", i, rec.Code, rec.Body.String())
		}
	}
	rec := send(path, sfViewer, map[string]int{"count": 1})
	if rec.Code != http.StatusTooManyRequests || errCode(rec) != "RATE_LIMITED" || rec.Header().Get("Retry-After") != "10" {
		t.Fatalf("over the limit: %d %s retry-after=%q", rec.Code, rec.Body.String(), rec.Header().Get("Retry-After"))
	}
	// The host may send hearts.
	if rec := send(path, sfAsha, map[string]int{"count": 2}); rec.Code != http.StatusOK || data(t, rec)["heart_count"] != float64(574) {
		t.Fatalf("host hearts: %d %s", rec.Code, rec.Body.String())
	}
	// heart_count is on the row.
	if d := data(t, s.call(http.MethodGet, "/v1/livestream/streams/"+sfWide.String(), uuid.Nil, nil, nil)); d["heart_count"] != float64(574) {
		t.Fatalf("heart_count on the row: %v", d["heart_count"])
	}
}

func TestSupportersRouteCodes(t *testing.T) {
	s := newSurfaceRig(t)
	s.seed()
	path := "/v1/livestream/streams/" + sfWide.String() + "/supporters"
	rec := s.call(http.MethodGet, path, uuid.Nil, nil, nil)
	if rec.Code != http.StatusOK || len(list(t, rec)) != 0 {
		t.Fatalf("no supporters yet: %d %s", rec.Code, rec.Body.String())
	}
	banned := uuid.New()
	_ = s.store.BanFromStream(context.Background(), sfWide, banned, sfAsha, "")
	if rec := s.call(http.MethodGet, path, banned, nil, nil); rec.Code != http.StatusForbidden || errCode(rec) != "BANNED_FROM_STREAM" {
		t.Fatalf("banned viewer: %d %s", rec.Code, rec.Body.String())
	}
	if rec := s.call(http.MethodGet, "/v1/livestream/streams/"+uuid.NewString()+"/supporters", uuid.Nil, nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown stream: %d", rec.Code)
	}
	s.store.Streams[sfWide].Visibility = "followers"
	if rec := s.call(http.MethodGet, path, uuid.Nil, nil, nil); rec.Code != http.StatusForbidden || errCode(rec) != "NOT_FOLLOWER" {
		t.Fatalf("signed out on a followers-only stream: %d %s", rec.Code, rec.Body.String())
	}
}

// TestAdminRevokeBadgeRoute: admin token with live:users.ban, a reason, an
// audit row.
func TestAdminRevokeBadgeRoute(t *testing.T) {
	a := newAdminRig(t)
	a.store.Badges[a.host] = &storetest.BadgeRow{Badge: postgres.BadgeFoundingCreator, GrantedAt: sfAt}
	path := InternalAdminPrefix + "/users/" + a.host.String() + "/badges/founding_creator"
	body := map[string]string{"reason": "bought viewers"}

	if rec := a.do(http.MethodDelete, path, "", body); rec.Code != http.StatusUnauthorized || errCode(rec) != CodeAdminTokenRequired {
		t.Fatalf("no token: %d %s", rec.Code, rec.Body.String())
	}
	var others []string
	for _, p := range AdminPermissions {
		if p != PermUsersBan {
			others = append(others, p)
		}
	}
	if rec := a.do(http.MethodDelete, path, a.token(t, a.admin, others...), body); rec.Code != http.StatusForbidden || errCode(rec) != CodeAdminPermissionScope {
		t.Fatalf("without live:users.ban: %d %s", rec.Code, rec.Body.String())
	}
	if rec := a.do(http.MethodDelete, path, a.token(t, a.payments, PermUsersBan), body); rec.Code != http.StatusForbidden {
		t.Fatalf("from payments-service: %d", rec.Code)
	}
	tok := a.token(t, a.admin, PermUsersBan)
	if rec := a.do(http.MethodDelete, path, tok, nil); rec.Code != http.StatusUnprocessableEntity || errCode(rec) != "VALIDATION_ERROR" {
		t.Fatalf("no reason: %d %s", rec.Code, rec.Body.String())
	}
	if rec := a.do(http.MethodDelete, InternalAdminPrefix+"/users/"+a.host.String()+"/badges/top_fan", tok, body); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown badge: %d %s", rec.Code, rec.Body.String())
	}
	if rec := a.do(http.MethodDelete, InternalAdminPrefix+"/users/"+uuid.NewString()+"/badges/founding_creator", tok, body); rec.Code != http.StatusNotFound || errCode(rec) != "NOT_FOUND" {
		t.Fatalf("a user without the badge: %d %s", rec.Code, rec.Body.String())
	}
	if len(a.store.Audits) != 0 || a.store.Badges[a.host].RevokedAt != nil {
		t.Fatalf("a refused revoke changed something: audits=%d", len(a.store.Audits))
	}

	rec := a.do(http.MethodDelete, path, tok, body)
	d := data(t, rec)
	if rec.Code != http.StatusOK || d["revoked"] != true || d["badge"] != "founding_creator" || d["user_id"] != a.host.String() {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body.String())
	}
	if len(a.store.Audits) != 1 {
		t.Fatalf("audit rows: %d", len(a.store.Audits))
	}
	if got := a.store.Audits[0]; got.Action != service.AuditUserBadgeRevoke || got.ActorID != a.actor || got.Reason != "bought viewers" || got.TargetID != a.host.String() {
		t.Fatalf("audit: %+v (actor must be the token's act)", got)
	}
	// Public read: gone.
	req := httptest.NewRequest(http.MethodGet, "/v1/livestream/users/"+a.host.String()+"/badges", nil)
	req.Header.Set("X-Internal-Service-Key", "k")
	out := httptest.NewRecorder()
	a.r.ServeHTTP(out, req)
	var env struct {
		Data struct {
			Badges []any `json:"badges"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Body.Bytes(), &env); err != nil || out.Code != http.StatusOK || env.Data.Badges == nil || len(env.Data.Badges) != 0 {
		t.Fatalf("badges after a revoke: %d %s", out.Code, out.Body.String())
	}
}
