//go:build integration

package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// End screens, cards and the audit's leak list (2026-09-29) against a real
// Postgres: the gates are the real ones (visibility incl. private shares,
// soft delete, processing, the privacy graph, the age gate), only
// graph-service and the date-of-birth source are fakes.
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/service/ -run EndScreen -v
//
// Refuses any database whose name does not end in _test (openHubMediaDB).

type esIT struct {
	t      *testing.T
	ctx    context.Context
	pool   *pgxpool.Pool
	store  *postgres.Store
	svc    *Service
	users  []uuid.UUID
	posts  []uuid.UUID
	media  []uuid.UUID
	denied map[[2]uuid.UUID]bool // viewer -> author the graph refuses
}

func newESIT(t *testing.T) *esIT {
	t.Helper()
	pool := openHubMediaDB(t)
	x := &esIT{t: t, ctx: context.Background(), pool: pool, store: postgres.New(pool), denied: map[[2]uuid.UUID]bool{}}
	// media_assets is media-service's table; on a fresh test database it
	// lacks the columns the media-state overlay reads.
	x.exec(`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS duration_ms INTEGER`)
	x.exec(`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS hls_master_key TEXT`)
	x.exec(`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS alt_text TEXT`)
	x.exec(`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS alt_decorative BOOLEAN`)
	x.exec(`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS storage_key TEXT NOT NULL DEFAULT ''`)
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ViewerID  string   `json:"viewer_id"`
			TargetIDs []string `json:"target_ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		viewer, _ := uuid.Parse(body.ViewerID)
		out := map[string]bool{}
		for _, id := range body.TargetIDs {
			out[id] = !x.denied[[2]uuid.UUID{viewer, uuid.MustParse(id)}]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": out})
	}))
	t.Cleanup(graph.Close)
	x.svc = New(x.store, nil, nil)
	x.svc.WithStoryAudience(NewStoryAudience(noRelationships{}))
	x.svc.now = func() time.Time { return hubNow }
	x.svc.SetGraphServiceURL(graph.URL)
	t.Cleanup(x.cleanup)
	return x
}

func (x *esIT) exec(q string, args ...any) {
	x.t.Helper()
	if _, err := x.pool.Exec(x.ctx, q, args...); err != nil {
		x.t.Fatalf("%v (%.60s)", err, q)
	}
}

func (x *esIT) user() uuid.UUID {
	id := uuid.New()
	x.exec(`INSERT INTO users (id) VALUES ($1) ON CONFLICT DO NOTHING`, id)
	x.users = append(x.users, id)
	return id
}

func (x *esIT) channel(owner uuid.UUID, name string) {
	x.exec(`INSERT INTO channels (user_id, handle, name) VALUES ($1, $2, $3)`, owner, "h"+owner.String()[:8], name)
}

type esPostOpt struct {
	visibility string
	age        bool
	processing bool
	created    time.Time
	noMedia    bool
	durationMs int
}

// post inserts a long video with one video asset (60 s, ready and passed
// unless processing).
func (x *esIT) post(author uuid.UUID, o esPostOpt) uuid.UUID {
	x.t.Helper()
	id, media := uuid.New(), uuid.New()
	if o.visibility == "" {
		o.visibility = "public"
	}
	if o.created.IsZero() {
		o.created = hubNow.Add(-time.Hour)
	}
	if o.durationMs == 0 {
		o.durationMs = 60000
	}
	x.exec(`INSERT INTO posts (id, author_id, text, title, visibility, content_type, review_status, age_restricted, created_at, updated_at)
		VALUES ($1, $2, 'v', $6, $3, 'long_video', 'approved', $4, $5, $5)`, id, author, o.visibility, o.age, o.created, "Video "+id.String()[:4])
	x.posts = append(x.posts, id)
	if o.noMedia {
		return id
	}
	status := "ready"
	if o.processing {
		status = "processing"
	}
	x.exec(`INSERT INTO media_assets (id, uploader_id, file_type, processing_status, moderation_status, duration_ms)
		VALUES ($1, $2, 'video', $3, 'passed', $4)`, media, author, status, o.durationMs)
	x.media = append(x.media, media)
	x.exec(`INSERT INTO post_media (post_id, media_id, kind) VALUES ($1, $2, 'video')`, id, media)
	return id
}

func (x *esIT) cleanup() {
	bg := context.Background()
	for _, q := range []string{
		`DELETE FROM post_product_tags WHERE post_id = ANY($1)`,
		`DELETE FROM comments WHERE post_id = ANY($1)`,
		`DELETE FROM poll_options WHERE post_id = ANY($1)`,
		`DELETE FROM polls WHERE post_id = ANY($1)`,
		`DELETE FROM video_series_episodes WHERE post_id = ANY($1)`,
		`DELETE FROM playlist_items WHERE post_id = ANY($1)`,
		`DELETE FROM video_metadata WHERE post_id = ANY($1)`,
		`DELETE FROM video_end_screens WHERE post_id = ANY($1)`,
		`DELETE FROM video_cards WHERE post_id = ANY($1)`,
		`DELETE FROM post_private_shares WHERE post_id = ANY($1)`,
		`DELETE FROM post_media WHERE post_id = ANY($1)`,
		`DELETE FROM post_engagement_counts WHERE post_id = ANY($1)`,
		`DELETE FROM outbox_events WHERE aggregate_id = ANY($1::uuid[])::text[]`,
	} {
		_, _ = x.pool.Exec(bg, q, x.posts)
	}
	_, _ = x.pool.Exec(bg, `DELETE FROM posts WHERE id = ANY($1)`, x.posts)
	_, _ = x.pool.Exec(bg, `DELETE FROM media_assets WHERE id = ANY($1)`, x.media)
	for _, q := range []string{
		`DELETE FROM video_series WHERE creator_id = ANY($1)`,
		`DELETE FROM playlists WHERE creator_id = ANY($1)`,
		`DELETE FROM channels WHERE user_id = ANY($1)`,
		`DELETE FROM users WHERE id = ANY($1)`,
	} {
		_, _ = x.pool.Exec(bg, q, x.users)
	}
}

func esRaw(p EndScreenPosition) json.RawMessage { b, _ := json.Marshal(p); return b }

// elementsByID reads the viewer's (or owner's) copy as maps keyed by id.
func (x *esIT) read(post uuid.UUID, viewer *uuid.UUID) (map[uuid.UUID]EndScreenElement, bool) {
	x.t.Helper()
	out, err := x.svc.GetEndScreensFor(x.ctx, post, viewer)
	if err != nil {
		x.t.Fatalf("read as %v: %v", viewer, err)
	}
	m := map[uuid.UUID]EndScreenElement{}
	switch v := out.(type) {
	case []EndScreenElement:
		for _, el := range v {
			m[el.ID] = el
		}
		return m, false
	case []EndScreenOwnerElement:
		for _, el := range v {
			m[el.ID] = el.EndScreenElement
		}
		return m, true
	}
	x.t.Fatalf("unexpected read type %T", out)
	return nil, false
}

// Every element whose target the viewer may not open is dropped: private
// (unless shared with them), soft-deleted, still processing, an author who
// blocked them, 18+ for a minor or an anonymous viewer, a collection made
// private. latest resolves past what they may not open.
func TestEndScreenReadDropsWhatTheViewerMayNotOpen(t *testing.T) {
	x := newESIT(t)
	author, blockedOwner, other, viewer, minor, shared := x.user(), x.user(), x.user(), x.user(), x.user(), x.user()
	x.channel(author, "Author Channel")
	x.channel(blockedOwner, "Blocked Channel")
	x.channel(other, "Other Channel")
	x.denied[[2]uuid.UUID{viewer, blockedOwner}] = true
	x.svc.SetBirthDateSource(&fakeBirthDates{dob: map[uuid.UUID]*time.Time{viewer: dayp(1990, 1, 1), minor: dayp(2012, 1, 1), shared: dayp(1990, 1, 1)}})

	subject := x.post(author, esPostOpt{created: hubNow.Add(-10 * time.Hour)})
	pub := x.post(author, esPostOpt{created: hubNow.Add(-5 * time.Hour)})
	unl := x.post(author, esPostOpt{visibility: "unlisted", created: hubNow.Add(-4 * time.Hour)})
	priv := x.post(author, esPostOpt{visibility: "private", created: hubNow.Add(-3 * time.Hour)})
	del := x.post(author, esPostOpt{created: hubNow.Add(-2*time.Hour - 30*time.Minute)})
	proc := x.post(author, esPostOpt{processing: true, created: hubNow.Add(-2 * time.Hour)})
	age := x.post(author, esPostOpt{age: true, created: hubNow.Add(-time.Hour)})
	x.exec(`INSERT INTO post_private_shares (post_id, user_id) VALUES ($1, $2)`, priv, shared)
	x.exec(`UPDATE posts SET deleted_at = NOW() WHERE id = $1`, del)
	var publicList, privateList uuid.UUID
	if err := x.pool.QueryRow(x.ctx, `INSERT INTO playlists (creator_id, title, visibility) VALUES ($1, 'Builds', 'public') RETURNING id`, author).Scan(&publicList); err != nil {
		t.Fatal(err)
	}
	if err := x.pool.QueryRow(x.ctx, `INSERT INTO playlists (creator_id, title, visibility) VALUES ($1, 'Drafts', 'private') RETURNING id`, author).Scan(&privateList); err != nil {
		t.Fatal(err)
	}
	x.exec(`INSERT INTO playlist_items (playlist_id, post_id, position) VALUES ($1, $2, 0), ($1, $3, 1)`, publicList, priv, unl)
	x.exec(`UPDATE playlists SET item_count = 2 WHERE id = $1`, publicList)
	x.exec(`INSERT INTO video_metadata (post_id, thumbnail_url, upload_status) VALUES ($1, '/thumb/priv', 'ready'), ($2, '/thumb/unl', 'ready')`, priv, unl)

	p := esRaw(EndScreenPosition{X: 0.05, Y: 0.1, W: 0.3})
	rows := []postgres.EndScreen{}
	add := func(kind, mode string, target *uuid.UUID) uuid.UUID {
		rows = append(rows, postgres.EndScreen{Type: kind, VideoMode: mode, TargetID: target, Position: p, StartMs: 45000, EndMs: 60000})
		return uuid.Nil
	}
	for _, id := range []uuid.UUID{pub, unl, priv, del, proc, age} {
		id := id
		add("video", "specific", &id)
	}
	add("video", "latest", nil)
	add("channel", "specific", &blockedOwner)
	add("channel", "specific", &other)
	add("playlist", "specific", &publicList)
	add("playlist", "specific", &privateList)
	add("channel_subscribe", "specific", nil)
	if err := x.store.SaveEndScreens(x.ctx, subject, rows); err != nil {
		t.Fatal(err)
	}
	ids := map[string]uuid.UUID{}
	for i, name := range []string{"pub", "unl", "priv", "del", "proc", "age", "latest", "blocked", "other", "publist", "privlist", "subscribe"} {
		ids[name] = rows[i].ID
	}
	keptFor := func(viewer *uuid.UUID) map[string]bool {
		got, owner := x.read(subject, viewer)
		if owner {
			t.Fatalf("%v got the owner's copy", viewer)
		}
		out := map[string]bool{}
		for name, id := range ids {
			if _, ok := got[id]; ok {
				out[name] = true
			}
		}
		return out
	}
	want := func(who string, got map[string]bool, names ...string) {
		t.Helper()
		if len(got) != len(names) {
			t.Fatalf("%s kept %v want %v", who, got, names)
		}
		for _, n := range names {
			if !got[n] {
				t.Fatalf("%s kept %v want %v", who, got, names)
			}
		}
	}
	want("adult viewer", keptFor(&viewer), "pub", "unl", "age", "latest", "other", "publist", "subscribe")
	want("minor", keptFor(&minor), "pub", "unl", "latest", "blocked", "other", "publist", "subscribe")
	want("anonymous", keptFor(nil), "pub", "unl", "latest", "blocked", "other", "publist", "subscribe")
	want("shared user", keptFor(&shared), "pub", "unl", "priv", "age", "latest", "blocked", "other", "publist", "subscribe")

	// latest: the newest public video the viewer may open — the 18+ one for
	// an adult, the next one down for a minor (proc is still processing).
	got, _ := x.read(subject, &viewer)
	if v := got[ids["latest"]].Video; v == nil || v.ID != age {
		t.Fatalf("latest for an adult: %+v want %s", v, age)
	}
	got, _ = x.read(subject, &minor)
	if v := got[ids["latest"]].Video; v == nil || v.ID != pub {
		t.Fatalf("latest for a minor: %+v want %s", v, pub)
	}
	// The collection's thumbnail never comes from an item the viewer may
	// not open (the private first item is skipped).
	if pl := got[ids["publist"]].Playlist; pl == nil || pl.ItemCount != 2 || pl.Title != "Builds" || pl.ThumbnailURL != "/thumb/unl" {
		t.Fatalf("collection block %+v", pl)
	}
	if ch := got[ids["subscribe"]].Channel; ch == nil || ch.UserID != author || ch.Name != "Author Channel" {
		t.Fatalf("subscribe block %+v", ch)
	}

	// The owner sees every element; a deleted target resolves to null.
	own, owner := x.read(subject, &author)
	if !owner || len(own) != len(ids) || own[ids["del"]].Video != nil || own[ids["priv"]].Video == nil {
		t.Fatalf("owner copy (owner=%v): %d elements, del=%+v priv=%+v", owner, len(own), own[ids["del"]].Video, own[ids["priv"]].Video)
	}

	// The subject itself: private -> 404 for a stranger, 18+ -> age codes.
	x.exec(`UPDATE posts SET visibility = 'private' WHERE id = $1`, subject)
	x.svc.InvalidatePostBodyCache(x.ctx, subject)
	if _, err := x.svc.GetEndScreensFor(x.ctx, subject, &viewer); !errors.Is(err, ErrPostNotVisible) {
		t.Fatalf("private subject: %v", err)
	}
	if _, err := x.svc.GetVideoCardsFor(x.ctx, subject, &viewer); !errors.Is(err, ErrPostNotVisible) {
		t.Fatalf("private subject cards: %v", err)
	}
	x.exec(`UPDATE posts SET visibility = 'public', age_restricted = true WHERE id = $1`, subject)
	x.svc.InvalidatePostBodyCache(x.ctx, subject)
	if _, err := x.svc.GetEndScreensFor(x.ctx, subject, &minor); !errors.Is(err, ErrAgeRestricted) {
		t.Fatalf("18+ subject for a minor: %v", err)
	}
}

// The save rules against the real store: duration from the asset, targets
// from the real rows, an echoed id keeping its stats.
func TestEndScreenSaveAgainstTheStore(t *testing.T) {
	x := newESIT(t)
	author, other := x.user(), x.user()
	x.channel(author, "Author Channel")
	subject := x.post(author, esPostOpt{})
	pub := x.post(author, esPostOpt{})
	priv := x.post(author, esPostOpt{visibility: "private"})
	foreign := x.post(other, esPostOpt{})
	noMedia := x.post(author, esPostOpt{noMedia: true})
	short := x.post(author, esPostOpt{durationMs: 24000})
	var systemList uuid.UUID
	if err := x.pool.QueryRow(x.ctx, `INSERT INTO playlists (creator_id, title, visibility, kind) VALUES ($1, 'Loved', 'public', 'liked') RETURNING id`, author).Scan(&systemList); err != nil {
		t.Fatal(err)
	}
	link := "https://example.com/parts"
	valid := []EndScreenInput{
		{Type: "video", TargetID: &pub, Position: EndScreenPosition{X: 0.05, Y: 0.1, W: 0.3}, StartMs: 45000, EndMs: 60000},
		{Type: "video", VideoMode: "latest", Position: EndScreenPosition{X: 0.65, Y: 0.1, W: 0.3}, StartMs: 45000, EndMs: 60000},
		{Type: "channel_subscribe", Position: EndScreenPosition{X: 0.05, Y: 0.5, W: 0.2}, StartMs: 45000, EndMs: 60000},
		{Type: "external_link", TargetURL: &link, Position: EndScreenPosition{X: 0.65, Y: 0.6, W: 0.3}, StartMs: 50000, EndMs: 60000},
	}
	saved, err := x.svc.SaveEndScreens(x.ctx, author, subject, valid)
	if err != nil || len(saved) != 4 {
		t.Fatalf("valid save: %v %v", saved, err)
	}
	code := func(err error) string {
		var re *AuthoringRuleError
		if errors.As(err, &re) {
			return re.Code
		}
		return "err: " + errorString(err)
	}
	one := func(el EndScreenInput) []EndScreenInput { return []EndScreenInput{el} }
	for _, tc := range []struct {
		name string
		post uuid.UUID
		in   []EndScreenInput
		want string
	}{
		{"no asset, no metadata", noMedia, one(valid[3]), CodeEndScreenNotEligible},
		{"a 24 s video", short, one(valid[3]), CodeEndScreenNotEligible},
		{"a private target", subject, one(EndScreenInput{Type: "video", TargetID: &priv, Position: valid[0].Position, StartMs: 45000, EndMs: 60000}), CodeEndScreenTarget},
		{"a foreign target", subject, one(EndScreenInput{Type: "video", TargetID: &foreign, Position: valid[0].Position, StartMs: 45000, EndMs: 60000}), CodeEndScreenTarget},
		{"a system collection", subject, one(EndScreenInput{Type: "playlist", TargetID: &systemList, Position: valid[0].Position, StartMs: 45000, EndMs: 60000}), CodeEndScreenTarget},
		{"an unknown channel", subject, one(EndScreenInput{Type: "channel", TargetID: &other, Position: valid[2].Position, StartMs: 45000, EndMs: 60000}), CodeEndScreenTarget},
	} {
		if _, err := x.svc.SaveEndScreens(x.ctx, author, tc.post, tc.in); code(err) != tc.want {
			t.Fatalf("%s: %v want %s", tc.name, err, tc.want)
		}
	}
	// A metadata-only duration counts.
	x.exec(`INSERT INTO video_metadata (post_id, duration_seconds, upload_status) VALUES ($1, 61, 'ready')`, noMedia)
	if sub, err := x.store.GetEndScreenSubject(x.ctx, noMedia); err != nil || sub.DurationMs != 61000 {
		t.Fatalf("metadata duration: %+v %v", sub, err)
	}

	// Stats survive a save that echoes the element's id; a dropped element's
	// stats go with it.
	day := hubNow
	for i := 0; i < 3; i++ {
		if err := x.store.BumpEndScreenStat(x.ctx, subject, saved[0].ID, day, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := x.store.BumpEndScreenStat(x.ctx, subject, saved[0].ID, day, true); err != nil {
		t.Fatal(err)
	}
	if err := x.store.BumpEndScreenStat(x.ctx, subject, saved[3].ID, day, false); err != nil {
		t.Fatal(err)
	}
	keep := saved[0].ID
	resaved, err := x.svc.SaveEndScreens(x.ctx, author, subject, []EndScreenInput{{ID: &keep, Type: "video", TargetID: &pub,
		Position: EndScreenPosition{X: 0.1, Y: 0.1, W: 0.3}, StartMs: 46000, EndMs: 60000}})
	if err != nil || resaved[0].ID != keep {
		t.Fatalf("resave: %v %v", resaved, err)
	}
	st, err := x.store.EndScreenStatsSince(x.ctx, subject, day)
	if err != nil || st[keep].Impressions != 3 || st[keep].Clicks != 1 || len(st) != 1 {
		t.Fatalf("stats after resave: %+v %v", st, err)
	}
	// An id that is not this post's is inserted fresh, never adopted.
	stolen := saved[0].ID
	otherSubject := x.post(author, esPostOpt{})
	again, err := x.svc.SaveEndScreens(x.ctx, author, otherSubject, []EndScreenInput{{ID: &stolen, Type: "external_link", TargetURL: &link,
		Position: EndScreenPosition{X: 0.1, Y: 0.1, W: 0.3}, StartMs: 46000, EndMs: 60000}})
	if err != nil || again[0].ID == stolen {
		t.Fatalf("another post's id was adopted: %v %v", again, err)
	}
	if rows, _ := x.store.GetEndScreens(x.ctx, subject); len(rows) != 1 || rows[0].ID != keep {
		t.Fatalf("the first post's element moved: %+v", rows)
	}
	// A counter bump that names another post's element writes nothing.
	if err := x.store.BumpEndScreenStat(x.ctx, otherSubject, keep, day, true); err != nil {
		t.Fatal(err)
	}
	if st, _ := x.store.EndScreenStatsSince(x.ctx, otherSubject, day); len(st) != 0 {
		t.Fatalf("a mismatched bump was written: %+v", st)
	}
	// A legacy {slot:n} row reads in the new shape.
	x.exec(`UPDATE video_end_screens SET position = '{"slot":3}' WHERE id = $1`, keep)
	own, _ := x.read(subject, &author)
	if p := own[keep].Position; p.X < 0.649 || p.X > 0.651 || p.Y < 0.599 || p.Y > 0.601 || p.W != 0.3 {
		t.Fatalf("legacy slot 3: %+v", p)
	}
}

func errorString(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}

// The owner's stats are the last 28 days; the viewer's events count once a
// day each and never the owner's own.
func TestEndScreenStatsWindowAndOwner(t *testing.T) {
	x := newESIT(t)
	author, viewer := x.user(), x.user()
	subject := x.post(author, esPostOpt{})
	link := "https://example.com"
	rows := []postgres.EndScreen{{Type: "external_link", TargetURL: &link, Position: esRaw(EndScreenPosition{X: 0.1, Y: 0.1, W: 0.3}), StartMs: 45000, EndMs: 60000}}
	if err := x.store.SaveEndScreens(x.ctx, subject, rows); err != nil {
		t.Fatal(err)
	}
	cards := []postgres.VideoCard{{Type: "external_link", TargetURL: &link, Title: "Docs", AppearAtMs: 1000}}
	if err := x.store.SaveVideoCards(x.ctx, subject, cards); err != nil {
		t.Fatal(err)
	}
	el, card := rows[0].ID, cards[0].ID
	x.exec(`INSERT INTO end_screen_stats (post_id, element_id, day, impressions, clicks) VALUES
		($1, $2, ($3::date - 27), 10, 2), ($1, $2, ($3::date - 28), 1000, 1000)`, subject, el, hubNow.Format("2006-01-02"))
	for i := 0; i < 2; i++ {
		if err := x.svc.RecordEndScreenEvent(x.ctx, subject, el, StatViewer{UserID: &viewer}, false); err != nil {
			t.Fatal(err)
		}
		if err := x.svc.RecordCardEvent(x.ctx, subject, card, StatViewer{AnonID: "abc"}, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := x.svc.RecordEndScreenEvent(x.ctx, subject, el, StatViewer{UserID: &author}, true); err != nil {
		t.Fatal(err)
	}
	out, err := x.svc.GetEndScreensFor(x.ctx, subject, &author)
	if err != nil {
		t.Fatal(err)
	}
	// No dedupe store here (no Redis): both viewer impressions count; the
	// owner's click does not. The 28-days-ago row is outside the window.
	st := out.([]EndScreenOwnerElement)[0].Stats
	if st.Impressions != 12 || st.Clicks != 2 || st.ClickRate != 0.1667 {
		t.Fatalf("owner stats %+v", st)
	}
	cout, err := x.svc.GetVideoCardsFor(x.ctx, subject, &author)
	if err != nil || cout.([]VideoCardOwnerView)[0].Stats.Clicks != 2 {
		t.Fatalf("card stats %+v %v", cout, err)
	}
	if err := x.svc.RecordEndScreenEvent(x.ctx, subject, card, StatViewer{UserID: &viewer}, false); !errors.Is(err, ErrEndScreenElementNotFound) {
		t.Fatalf("a card id on the end-screen route: %v", err)
	}
}

// The Redis dedupe itself, against a live Redis when REDIS_ADDR is set (the
// keys are this test's own random ids and are deleted afterwards).
func TestEndScreenRedisDedupe(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("REDIS_ADDR not set")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	d := redisStatDeduper{rdb: rdb}
	key := "es_stat:test:" + uuid.NewString()
	t.Cleanup(func() { rdb.Del(context.Background(), key) })
	first, err := d.FirstToday(context.Background(), key)
	if err != nil || !first {
		t.Fatalf("first: %v %v", first, err)
	}
	if again, err := d.FirstToday(context.Background(), key); err != nil || again {
		t.Fatalf("again: %v %v", again, err)
	}
	if ttl := rdb.TTL(context.Background(), key).Val(); ttl < 25*time.Hour {
		t.Fatalf("ttl %v", ttl)
	}
}

// POST /v1/videos/:id/publish keeps the chosen visibility.
func TestPublishVideoKeepsVisibility(t *testing.T) {
	x := newESIT(t)
	author := x.user()
	scheduled := x.post(author, esPostOpt{visibility: "private"})
	x.exec(`UPDATE posts SET publish_at = $2 WHERE id = $1`, scheduled, hubNow.Add(48*time.Hour))
	live := x.post(author, esPostOpt{visibility: "unlisted"})
	for _, id := range []uuid.UUID{scheduled, live} {
		x.exec(`INSERT INTO video_metadata (post_id, duration_seconds, upload_status) VALUES ($1, 60, 'ready')`, id)
		if err := x.svc.PublishVideo(x.ctx, id, author); err != nil {
			t.Fatalf("publish %s: %v", id, err)
		}
	}
	for id, want := range map[uuid.UUID]string{scheduled: "private", live: "unlisted"} {
		var vis string
		var publishAt *time.Time
		if err := x.pool.QueryRow(x.ctx, `SELECT visibility, publish_at FROM posts WHERE id = $1`, id).Scan(&vis, &publishAt); err != nil {
			t.Fatal(err)
		}
		if vis != want || publishAt != nil {
			t.Fatalf("%s after publish: visibility=%s publish_at=%v want %s and live", id, vis, publishAt, want)
		}
	}
}

// The engagement gate is the detail's visibility rule: unlisted and a
// private post's share list are let in; a stranger to a private post is not.
func TestEngagementGateFollowsTheDetail(t *testing.T) {
	x := newESIT(t)
	author, shared, stranger := x.user(), x.user(), x.user()
	priv := x.post(author, esPostOpt{visibility: "private"})
	unl := x.post(author, esPostOpt{visibility: "unlisted"})
	x.exec(`INSERT INTO post_private_shares (post_id, user_id) VALUES ($1, $2)`, priv, shared)
	if _, err := x.svc.loadPostForEngagement(x.ctx, priv, shared); err != nil {
		t.Fatalf("shared user: %v", err)
	}
	if _, err := x.svc.loadPostForEngagement(x.ctx, unl, stranger); err != nil {
		t.Fatalf("unlisted: %v", err)
	}
	if _, err := x.svc.loadPostForEngagement(x.ctx, priv, stranger); !errors.Is(err, ErrPostNotVisible) {
		t.Fatalf("stranger on a private post: %v", err)
	}
}

// Comment dislike and product-tag counters answer for the post they sit on.
func TestCommentDislikeAndProductTagGates(t *testing.T) {
	x := newESIT(t)
	author, stranger, minor := x.user(), x.user(), x.user()
	x.svc.SetBirthDateSource(&fakeBirthDates{dob: map[uuid.UUID]*time.Time{minor: dayp(2012, 1, 1)}})
	priv := x.post(author, esPostOpt{visibility: "private"})
	adult := x.post(author, esPostOpt{age: true})
	var c1, c2 uuid.UUID
	if err := x.pool.QueryRow(x.ctx, `INSERT INTO comments (post_id, author_id, body) VALUES ($1, $2, 'hi') RETURNING id`, priv, author).Scan(&c1); err != nil {
		t.Fatal(err)
	}
	if err := x.pool.QueryRow(x.ctx, `INSERT INTO comments (post_id, author_id, body) VALUES ($1, $2, 'hi') RETURNING id`, adult, author).Scan(&c2); err != nil {
		t.Fatal(err)
	}
	if _, err := x.svc.ToggleCommentDislike(x.ctx, c1, stranger); err == nil || err.Error() != "COMMENT_NOT_FOUND" {
		t.Fatalf("dislike under a private post: %v", err)
	}
	if _, err := x.svc.ToggleCommentDislike(x.ctx, c2, minor); !errors.Is(err, ErrAgeRestricted) {
		t.Fatalf("dislike under an 18+ post by a minor: %v", err)
	}

	pub, otherPost := x.post(author, esPostOpt{}), x.post(author, esPostOpt{})
	var tag uuid.UUID
	if err := x.pool.QueryRow(x.ctx, `INSERT INTO post_product_tags (post_id, affiliate_link_id, creator_id) VALUES ($1, $2, $3) RETURNING id`,
		pub, uuid.New(), author).Scan(&tag); err != nil {
		t.Fatal(err)
	}
	if err := x.svc.RecordProductTagImpression(x.ctx, otherPost, tag, "h"); !errors.Is(err, postgres.ErrTagNotFound) {
		t.Fatalf("tag asked through another post: %v", err)
	}
	if err := x.svc.RecordProductTagClick(x.ctx, otherPost, tag, "h"); !errors.Is(err, postgres.ErrTagNotFound) {
		t.Fatalf("tag click through another post: %v", err)
	}
	if err := x.svc.RecordProductTagImpression(x.ctx, pub, tag, "h"); err != nil {
		t.Fatalf("tag through its post: %v", err)
	}
	var n int
	if err := x.pool.QueryRow(x.ctx, `SELECT impression_count FROM post_product_tags WHERE id = $1`, tag).Scan(&n); err != nil || n != 1 {
		t.Fatalf("impressions %d %v", n, err)
	}
}

// Episode lists drop, with the real gate, the episodes a viewer may not open.
func TestEpisodeListsUseTheRealGate(t *testing.T) {
	x := newESIT(t)
	author, stranger, minor := x.user(), x.user(), x.user()
	x.svc.SetBirthDateSource(&fakeBirthDates{dob: map[uuid.UUID]*time.Time{minor: dayp(2012, 1, 1), stranger: dayp(1990, 1, 1)}})
	pub := x.post(author, esPostOpt{})
	priv := x.post(author, esPostOpt{visibility: "private"})
	adult := x.post(author, esPostOpt{age: true})
	var series uuid.UUID
	if err := x.pool.QueryRow(x.ctx, `INSERT INTO video_series (creator_id, title, is_public) VALUES ($1, 'S', true) RETURNING id`, author).Scan(&series); err != nil {
		t.Fatal(err)
	}
	x.exec(`INSERT INTO video_series_episodes (series_id, post_id, episode_num) VALUES ($1, $2, 1), ($1, $3, 2), ($1, $4, 3)`, series, pub, priv, adult)
	count := func(viewer *uuid.UUID) int {
		eps, err := x.svc.GetVideoSeriesEpisodes(x.ctx, series, viewer)
		if err != nil {
			t.Fatal(err)
		}
		return len(eps)
	}
	if n := count(&author); n != 3 {
		t.Fatalf("owner sees %d", n)
	}
	if n := count(&stranger); n != 2 {
		t.Fatalf("adult stranger sees %d want 2 (no private)", n)
	}
	if n := count(&minor); n != 1 {
		t.Fatalf("minor sees %d want 1", n)
	}
	if n := count(nil); n != 1 {
		t.Fatalf("anonymous sees %d want 1", n)
	}
}

// A poll card resolves against the poll read for the owner's editor; the
// watch page draws no poll card, so a viewer's copy leaves it out even when
// the poll post is theirs to open.
func TestPollCardsAreTheOwnersOnly(t *testing.T) {
	x := newESIT(t)
	author, viewer := x.user(), x.user()
	subject := x.post(author, esPostOpt{})
	pollPost := x.post(author, esPostOpt{})
	x.exec(`INSERT INTO polls (post_id, question) VALUES ($1, 'Next build?')`, pollPost)
	x.exec(`INSERT INTO poll_options (post_id, label) VALUES ($1, 'Kafka'), ($1, 'Go')`, pollPost)
	link := "https://example.com"
	cards := []postgres.VideoCard{
		{Type: "poll", TargetID: &pollPost, Title: "Vote", AppearAtMs: 1000},
		{Type: "external_link", TargetURL: &link, Title: "Docs", AppearAtMs: 2000},
	}
	if err := x.store.SaveVideoCards(x.ctx, subject, cards); err != nil {
		t.Fatal(err)
	}
	out, err := x.svc.GetVideoCardsFor(x.ctx, subject, &viewer)
	if err != nil {
		t.Fatal(err)
	}
	if v := out.([]VideoCardView); len(v) != 1 || v[0].Type != "external_link" {
		t.Fatalf("viewer cards: %+v", v)
	}
	out, err = x.svc.GetVideoCardsFor(x.ctx, subject, &author)
	if err != nil {
		t.Fatal(err)
	}
	own := out.([]VideoCardOwnerView)
	if len(own) != 2 || own[0].Poll == nil || own[0].Poll.Question != "Next build?" || len(own[0].Poll.Options) != 2 || own[0].Poll.PostID != pollPost {
		t.Fatalf("owner poll card: %+v", own[0])
	}
}
