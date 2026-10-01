package postgres_test

// The in-memory store (internal/storetest) stands in for Postgres in every
// service and http test, so for the live surfaces it has to answer exactly
// what Postgres answers. This runs ONE scenario against both and compares
// the answers. Same guard as the other integration tests: LIVE_V2_TEST_DSN
// and a database whose name ends in _test.

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	dbschema "github.com/atpost/live-service-v2/database"
	"github.com/atpost/live-service-v2/internal/store/postgres"
	"github.com/atpost/live-service-v2/internal/storetest"
)

// surfaceStore is what migration 005 added to the store.
type surfaceStore interface {
	CreateStream(ctx context.Context, p postgres.CreateStreamParams) (*postgres.LiveStream, error)
	GetByID(ctx context.Context, id uuid.UUID) (*postgres.LiveStream, error)
	ListLive(ctx context.Context, p postgres.ListLiveParams) ([]*postgres.LiveStream, error)
	ListScheduled(ctx context.Context, p postgres.ListScheduledParams) ([]*postgres.LiveStream, error)
	ListPast(ctx context.Context, p postgres.ListPastParams) ([]*postgres.LiveStream, error)
	UpdateScheduled(ctx context.Context, id uuid.UUID, p postgres.StreamPatch) (*postgres.LiveStream, error)
	SetReminder(ctx context.Context, streamID, userID uuid.UUID, set bool) (int, error)
	ReminderStats(ctx context.Context, streamIDs []uuid.UUID, viewerID uuid.UUID) (map[uuid.UUID]postgres.ReminderStat, error)
	ListReminderUserIDs(ctx context.Context, streamID, after uuid.UUID, limit int) ([]uuid.UUID, error)
	AddHearts(ctx context.Context, streamID, userID uuid.UUID, n, perUserCap int) (postgres.HeartResult, error)
	ListSupporters(ctx context.Context, streamID uuid.UUID, limit int) ([]postgres.Supporter, error)
	BadgesFor(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID][]string, error)
	ListBadges(ctx context.Context, userID uuid.UUID) ([]postgres.Badge, error)
	AdminRevokeBadge(ctx context.Context, userID uuid.UUID, badge, reason string, audit postgres.AuditEntry) (bool, error)
	ApplyTransition(ctx context.Context, id uuid.UUID, decide postgres.TransitionFunc, events postgres.EventsFunc, audit *postgres.AuditEntry) (*postgres.TransitionResult, error)
	InsertChatMessage(ctx context.Context, streamID, userID uuid.UUID, text string) (*postgres.ChatMessage, error)
	RemoveChatMessage(ctx context.Context, streamID, messageID, by uuid.UUID) (bool, error)
	BanFromStream(ctx context.Context, streamID, userID, by uuid.UUID, reason string) error
	AdminSetPlatformBan(ctx context.Context, userID uuid.UUID, banned bool, reason string, audit postgres.AuditEntry) error
}

// placer puts rows where the scenario needs them in time; the stores
// themselves only write "now".
type placer interface {
	stream(id uuid.UUID, status string, viewers int, started, ended *time.Time)
	heartAt(stream, user uuid.UUID, at time.Time)
	messageAt(id uuid.UUID, at time.Time)
}

type pgPlacer struct {
	t    *testing.T
	pool *pgxpool.Pool
}

func (p pgPlacer) exec(q string, args ...any) {
	p.t.Helper()
	if _, err := p.pool.Exec(context.Background(), q, args...); err != nil {
		p.t.Fatalf("%s: %v", q, err)
	}
}
func (p pgPlacer) stream(id uuid.UUID, status string, viewers int, started, ended *time.Time) {
	p.exec(`UPDATE live_streams SET status = $2, viewer_count = $3, started_at = $4, ended_at = $5 WHERE id = $1`, id, status, viewers, started, ended)
}
func (p pgPlacer) heartAt(stream, user uuid.UUID, at time.Time) {
	p.exec(`UPDATE live_stream_hearts SET created_at = $3 WHERE stream_id = $1 AND user_id = $2`, stream, user, at)
}
func (p pgPlacer) messageAt(id uuid.UUID, at time.Time) {
	p.exec(`UPDATE live_chat_messages SET created_at = $2 WHERE id = $1`, id, at)
}

type memPlacer struct{ m *storetest.MemStore }

func (p memPlacer) stream(id uuid.UUID, status string, viewers int, started, ended *time.Time) {
	st := p.m.Streams[id]
	st.Status, st.ViewerCount, st.StartedAt, st.EndedAt = status, viewers, started, ended
}
func (p memPlacer) heartAt(stream, user uuid.UUID, at time.Time) {
	p.m.Hearts[stream][user].CreatedAt = at
}
func (p memPlacer) messageAt(id uuid.UUID, at time.Time) { p.m.Messages[id].CreatedAt = at }

// surfaceScenario drives s and returns everything it answered, with ids
// replaced by the scenario's own names so two stores can be compared.
func surfaceScenario(t *testing.T, s surfaceStore, place placer) map[string]any {
	t.Helper()
	ctx := context.Background()
	out := map[string]any{}
	names := map[uuid.UUID]string{}
	name := func(id uuid.UUID) string {
		if n, ok := names[id]; ok {
			return n
		}
		return "?" + id.String()
	}
	user := func(n string) uuid.UUID { id := uuid.New(); names[id] = n; return id }
	errName := func(err error) string {
		switch {
		case err == nil:
			return "ok"
		case errors.Is(err, postgres.ErrNotFound):
			return "not_found"
		case errors.Is(err, postgres.ErrStateConflict):
			return "state_conflict"
		case errors.Is(err, postgres.ErrNotOnAir):
			return "not_on_air"
		}
		return "error"
	}
	tag := "parity-" + uuid.NewString()[:8]
	t0 := time.Now().UTC().Truncate(time.Second).Add(-6 * time.Hour)
	at := func(min int) *time.Time { v := t0.Add(time.Duration(min) * time.Minute); return &v }
	alice, bob := user("alice"), user("bob")
	mk := func(n string, creator uuid.UUID, orientation string, sched *time.Time) *postgres.LiveStream {
		st, err := s.CreateStream(ctx, postgres.CreateStreamParams{
			CreatorUserID: creator, LiveKitRoom: "stream_" + uuid.NewString(), Title: n, Visibility: "public",
			Orientation: orientation, Category: tag, ScheduledAt: sched,
		})
		if err != nil {
			t.Fatalf("create %s: %v", n, err)
		}
		names[st.ID] = n
		return st
	}
	rows := func(list []*postgres.LiveStream, err error) []string {
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		o := []string{}
		for _, st := range list {
			o = append(o, name(st.ID))
		}
		return o
	}

	// --- listings ---
	live := map[string]*postgres.LiveStream{}
	for i, spec := range []struct {
		n           string
		creator     uuid.UUID
		orientation string
		status      string
		viewers     int
	}{
		{"wide10", alice, "landscape", "live", 10},
		{"tall50", bob, "portrait", "live", 50},
		{"tall10", bob, "portrait", "reconnecting", 10},
		{"wide30", alice, "", "live", 30},
		{"starting", alice, "landscape", "starting", 99},
	} {
		st := mk(spec.n, spec.creator, spec.orientation, nil)
		place.stream(st.ID, spec.status, spec.viewers, at(i), nil)
		live[spec.n] = st
	}
	f := postgres.StreamFilter{Category: tag}
	out["live recent"] = rows(s.ListLive(ctx, postgres.ListLiveParams{Limit: 50, Filter: f}))
	out["live viewers"] = rows(s.ListLive(ctx, postgres.ListLiveParams{Limit: 50, Sort: postgres.SortViewers, Filter: f}))
	out["live portrait"] = rows(s.ListLive(ctx, postgres.ListLiveParams{Limit: 50, Sort: postgres.SortViewers, Filter: postgres.StreamFilter{Category: tag, Orientation: "portrait"}}))
	out["live by alice"] = rows(s.ListLive(ctx, postgres.ListLiveParams{Limit: 50, Filter: postgres.StreamFilter{Category: tag, CreatorIDs: []uuid.UUID{alice}}}))
	out["live by nobody"] = rows(s.ListLive(ctx, postgres.ListLiveParams{Limit: 50, Filter: postgres.StreamFilter{Category: tag, CreatorIDs: []uuid.UUID{}}}))
	out["live limit 2"] = rows(s.ListLive(ctx, postgres.ListLiveParams{Limit: 2, Sort: postgres.SortViewers, Filter: f}))
	for _, sortBy := range []string{postgres.SortRecent, postgres.SortViewers} {
		walk := []string{}
		p := postgres.ListLiveParams{Limit: 1, Sort: sortBy, Filter: f}
		for i := 0; i < 10; i++ {
			page, err := s.ListLive(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			if len(page) == 0 {
				break
			}
			walk = append(walk, name(page[0].ID))
			v := page[0].ViewerCount
			p.StartedBefore, p.IDBefore = page[0].StartedAt, &page[0].ID
			if sortBy == postgres.SortViewers {
				p.ViewersBefore = &v
			}
		}
		out["live walk "+sortBy] = walk
	}
	created, _ := s.GetByID(ctx, live["wide30"].ID)
	out["default orientation"] = created.Orientation

	now := time.Now().UTC()
	future := func(h int) *time.Time { v := now.Add(time.Duration(h) * time.Hour); return &v }
	soon := mk("soon", alice, "landscape", future(1))
	later := mk("later", bob, "portrait", future(2))
	mk("overdue", alice, "landscape", future(-1))
	mk("draft", alice, "landscape", nil)
	out["upcoming"] = rows(s.ListScheduled(ctx, postgres.ListScheduledParams{Limit: 50, Now: now, Filter: f}))
	out["upcoming portrait"] = rows(s.ListScheduled(ctx, postgres.ListScheduledParams{Limit: 50, Now: now, Filter: postgres.StreamFilter{Category: tag, Orientation: "portrait"}}))
	mk("draft", alice, "landscape", nil) // a second timeless one, under the same name: their order is by id
	own := postgres.StreamFilter{Category: tag, CreatorIDs: []uuid.UUID{alice}}
	out["upcoming, alice's own"] = rows(s.ListScheduled(ctx, postgres.ListScheduledParams{Limit: 50, Unstarted: true, Filter: own}))
	out["upcoming, alice's, as seen by others"] = rows(s.ListScheduled(ctx, postgres.ListScheduledParams{Limit: 50, Now: now, Filter: own}))
	unstarted := []string{}
	usp := postgres.ListScheduledParams{Limit: 1, Unstarted: true, Filter: own}
	for i := 0; i < 10; i++ {
		page, err := s.ListScheduled(ctx, usp)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		unstarted = append(unstarted, name(page[0].ID))
		key := postgres.ScheduledSortKey(page[0])
		usp.ScheduledAfter, usp.IDAfter = &key, &page[0].ID
	}
	out["upcoming, alice's own, walked"] = unstarted
	out["upcoming after soon"] = rows(s.ListScheduled(ctx, postgres.ListScheduledParams{Limit: 50, Now: now, Filter: f, ScheduledAfter: soon.ScheduledAt, IDAfter: &soon.ID}))

	for i, spec := range []struct {
		n       string
		status  string
		started bool
	}{{"past old", "ended", true}, {"past new", "ended", true}, {"past failed", "failed", true}, {"past never live", "ended", false}} {
		st := mk(spec.n, alice, "landscape", nil)
		var started *time.Time
		if spec.started {
			started = at(100 + 10*i)
		}
		place.stream(st.ID, spec.status, 0, started, at(105+10*i))
		live[spec.n] = st
	}
	out["past"] = rows(s.ListPast(ctx, postgres.ListPastParams{Limit: 50, Filter: f}))
	pastNew, _ := s.GetByID(ctx, live["past new"].ID)
	out["past after the newest"] = rows(s.ListPast(ctx, postgres.ListPastParams{Limit: 50, Filter: f, EndedBefore: pastNew.EndedAt, IDBefore: &pastNew.ID}))

	// --- editing ---
	str := func(v string) *string { return &v }
	edited, err := s.UpdateScheduled(ctx, soon.ID, postgres.StreamPatch{Title: str("renamed"), Orientation: str("portrait"), SetScheduledAt: true})
	out["edit"] = errName(err)
	if err == nil {
		out["edit result"] = fmt.Sprint(edited.Title, " ", edited.Orientation, " ", edited.ScheduledAt == nil, " ", edited.Category == tag, " ", edited.Visibility)
	}
	_, err = s.UpdateScheduled(ctx, live["wide10"].ID, postgres.StreamPatch{Title: str("x")})
	out["edit a live stream"] = errName(err)
	_, err = s.UpdateScheduled(ctx, uuid.New(), postgres.StreamPatch{Title: str("x")})
	out["edit nothing"] = errName(err)

	// --- reminders ---
	r1, r2, r3 := user("r1"), user("r2"), user("r3")
	counts := []int{}
	for _, step := range []struct {
		u   uuid.UUID
		set bool
	}{{r1, true}, {r2, true}, {r1, true}, {r3, true}, {r2, false}, {r2, false}} {
		n, err := s.SetReminder(ctx, later.ID, step.u, step.set)
		if err != nil {
			t.Fatal(err)
		}
		counts = append(counts, n)
	}
	out["reminder counts"] = counts
	_, err = s.SetReminder(ctx, live["wide10"].ID, r1, true)
	out["reminder on a live stream"] = errName(err)
	_, err = s.SetReminder(ctx, uuid.New(), r1, true)
	out["reminder on nothing"] = errName(err)
	stats, err := s.ReminderStats(ctx, []uuid.UUID{later.ID, soon.ID}, r1)
	if err != nil {
		t.Fatal(err)
	}
	_, soonListed := stats[soon.ID]
	out["reminder stats"] = fmt.Sprint(stats[later.ID], " soon listed: ", soonListed)
	statsNobody, _ := s.ReminderStats(ctx, []uuid.UUID{later.ID}, uuid.Nil)
	out["reminder stats, nobody"] = fmt.Sprint(statsNobody[later.ID])
	all, _ := s.ListReminderUserIDs(ctx, later.ID, uuid.Nil, 100)
	sorted := len(all) == 2 && all[0].String() < all[1].String()
	out["reminder ids"] = fmt.Sprint(len(all), " sorted: ", sorted)
	if len(all) == 2 {
		rest, _ := s.ListReminderUserIDs(ctx, later.ID, all[0], 100)
		first, _ := s.ListReminderUserIDs(ctx, later.ID, uuid.Nil, 1)
		out["reminder paging"] = fmt.Sprint(len(rest) == 1 && rest[0] == all[1], " ", len(first) == 1 && first[0] == all[0])
	}

	// --- hearts and supporters ---
	room := live["wide10"].ID
	hearts := []string{}
	top, chatty, early, late, quiet, sBanned, pBanned := user("top"), user("chatty"), user("early"), user("late"), user("quiet"), user("streamBanned"), user("platformBanned")
	give := func(u uuid.UUID, n, perUserCap, minute int) {
		res, err := s.AddHearts(ctx, room, u, n, perUserCap)
		hearts = append(hearts, fmt.Sprintf("%s +%d/%d %s", name(u), res.Added, res.Total, errName(err)))
		if err == nil && res.Added > 0 && minute >= 0 {
			place.heartAt(room, u, *at(minute))
		}
	}
	say := func(u uuid.UUID, minute int) uuid.UUID {
		m, err := s.InsertChatMessage(ctx, room, u, "hello")
		if err != nil {
			t.Fatal(err)
		}
		place.messageAt(m.ID, *at(minute))
		return m.ID
	}
	give(top, 15, 20, 30)
	give(top, 15, 20, -1) // crosses the cap: 5 count
	give(top, 15, 20, -1) // past the cap: none
	give(chatty, 5, 100, 20)
	give(late, 5, 100, 10)
	give(early, 5, 100, 40)
	give(sBanned, 19, 100, 2)
	give(pBanned, 18, 100, 3)
	give(alice, 9, 100, 1) // the host
	say(chatty, 21)
	say(chatty, 22)
	removed := say(chatty, 23)
	say(late, 11)
	say(early, 5)
	say(quiet, 50)
	say(alice, 0)
	if _, err := s.RemoveChatMessage(ctx, room, removed, alice); err != nil {
		t.Fatal(err)
	}
	if err := s.BanFromStream(ctx, room, sBanned, alice, ""); err != nil {
		t.Fatal(err)
	}
	ban := postgres.AuditEntry{ActorID: uuid.New(), Action: "user.live_ban", TargetType: "user", TargetID: pBanned.String(), Reason: "parity"}
	if err := s.AdminSetPlatformBan(ctx, pBanned, true, "parity", ban); err != nil {
		t.Fatal(err)
	}
	res, err := s.AddHearts(ctx, soon.ID, top, 1, 100)
	hearts = append(hearts, fmt.Sprintf("scheduled stream +%d %s", res.Added, errName(err)))
	_, err = s.AddHearts(ctx, uuid.New(), top, 1, 100)
	hearts = append(hearts, "unknown stream "+errName(err))
	out["hearts"] = hearts
	total, _ := s.GetByID(ctx, room)
	out["heart_count"] = total.HeartCount
	sup := func(limit int) []string {
		list, err := s.ListSupporters(ctx, room, limit)
		if err != nil {
			t.Fatal(err)
		}
		o := []string{}
		for _, r := range list {
			o = append(o, fmt.Sprintf("%s %d/%d first at minute %d", name(r.UserID), r.Hearts, r.Messages, int(r.FirstAt.Sub(t0).Minutes())))
		}
		return o
	}
	out["supporters"] = sup(0)
	out["supporters limit 2"] = sup(2)
	if err := s.AdminSetPlatformBan(ctx, pBanned, false, "", ban); err != nil {
		t.Fatal(err)
	}
	out["supporters after the unban"] = sup(50)

	// --- the founding creator badge ---
	end := func(*postgres.LiveStream, time.Duration) (postgres.Decision, bool) {
		return postgres.Decision{To: postgres.StatusEnded, Reason: "host_ended"}, true
	}
	founder, brief := user("founder"), user("brief")
	long := mk("long", founder, "landscape", nil)
	started := time.Now().Add(-10 * time.Minute)
	place.stream(long.ID, "live", 0, &started, nil)
	short := mk("short", brief, "landscape", nil)
	recently := time.Now().Add(-time.Minute)
	place.stream(short.ID, "live", 0, &recently, nil)
	for _, st := range []*postgres.LiveStream{long, short} {
		if _, err := s.ApplyTransition(ctx, st.ID, end, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	badges := func(ids ...uuid.UUID) string {
		m, err := s.BadgesFor(ctx, ids)
		if err != nil {
			t.Fatal(err)
		}
		o := []string{}
		for _, id := range ids {
			if b, ok := m[id]; ok {
				o = append(o, name(id)+":"+strings.Join(b, "+"))
			}
		}
		return strings.Join(o, " ")
	}
	out["badges"] = badges(founder, brief, alice)
	list, _ := s.ListBadges(ctx, founder)
	endedLong, _ := s.GetByID(ctx, long.ID)
	out["badge list"] = fmt.Sprint(len(list), " ", len(list) == 1 && list[0].Badge == "founding_creator" && list[0].GrantedAt.Equal(*endedLong.EndedAt))
	none, _ := s.ListBadges(ctx, brief)
	out["badge list, none"] = fmt.Sprint(none != nil, " ", len(none))
	fban := postgres.AuditEntry{ActorID: uuid.New(), Action: "user.live_ban", TargetType: "user", TargetID: founder.String(), Reason: "parity"}
	_ = s.AdminSetPlatformBan(ctx, founder, true, "parity", fban)
	out["badges while banned"] = badges(founder)
	_ = s.AdminSetPlatformBan(ctx, founder, false, "", fban)
	out["badges after the unban"] = badges(founder)
	rev := postgres.AuditEntry{ActorID: uuid.New(), Action: "user.badge_revoke", TargetType: "user", TargetID: founder.String(), Reason: "parity"}
	steps := []string{}
	for _, target := range []uuid.UUID{brief, founder, founder} {
		revoked, err := s.AdminRevokeBadge(ctx, target, "founding_creator", "parity", rev)
		steps = append(steps, fmt.Sprintf("%s %v %s", name(target), revoked, errName(err)))
	}
	out["revoke"] = steps
	again := mk("again", founder, "landscape", nil)
	earlier := time.Now().Add(-time.Hour)
	place.stream(again.ID, "live", 0, &earlier, nil)
	if _, err := s.ApplyTransition(ctx, again.ID, end, nil, nil); err != nil {
		t.Fatal(err)
	}
	out["badges after a revoke and another stream"] = badges(founder)
	return out
}

func TestIntegrationMemStoreMirrorsPostgres(t *testing.T) {
	dsn := os.Getenv("LIVE_V2_TEST_DSN")
	if dsn == "" {
		t.Skip("LIVE_V2_TEST_DSN not set")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("LIVE_V2_TEST_DSN does not parse")
	}
	if name := strings.TrimPrefix(u.Path, "/"); !strings.HasSuffix(name, "_test") {
		t.Fatalf("refusing to run against database %q: the name must end in _test", name)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := postgres.BootstrapSchema(ctx, pool, dbschema.SetupSQL, dbschema.Migrations); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	mem := storetest.New()
	want := surfaceScenario(t, postgres.New(pool), pgPlacer{t: t, pool: pool})
	got := surfaceScenario(t, mem, memPlacer{m: mem})

	if len(want) < 30 {
		t.Fatalf("the scenario answered only %d things", len(want))
	}
	for k, w := range want {
		if g, ok := got[k]; !ok || !reflect.DeepEqual(g, w) {
			t.Errorf("%s\n  postgres: %v\n  memstore: %v", k, w, got[k])
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("%s: answered by the memory store only", k)
		}
	}
	// And the answers are the expected ones, not merely equal.
	expect := map[string]any{
		"live viewers":                         []string{"tall50", "wide30", "tall10", "wide10"},
		"live recent":                          []string{"wide30", "tall10", "tall50", "wide10"},
		"upcoming, alice's own":                []string{"overdue", "soon", "draft", "draft"},
		"upcoming, alice's own, walked":        []string{"overdue", "soon", "draft", "draft"},
		"upcoming, alice's, as seen by others": []string{"soon"},
		"past":                                 []string{"past new", "past old"},
		"reminder counts":                      []int{1, 2, 2, 3, 2, 2},
		"heart_count":                          int64(81),
		"default orientation":                  "landscape",
		"edit a live stream":                   "state_conflict",
		"badges":                               "founder:founding_creator",
		"badges while banned":                  "",
		"badges after a revoke and another stream": "",
		"revoke": []string{"brief false not_found", "founder true ok", "founder false ok"},
	}
	for k, e := range expect {
		if !reflect.DeepEqual(want[k], e) {
			t.Errorf("%s = %v, want %v", k, want[k], e)
		}
	}
	sup, _ := want["supporters"].([]string)
	if len(sup) != 5 || !strings.HasPrefix(sup[0], "top 20/0") || !strings.HasPrefix(sup[1], "chatty 5/2") ||
		!strings.HasPrefix(sup[2], "early 5/1") || !strings.HasPrefix(sup[3], "late 5/1") || !strings.HasPrefix(sup[4], "quiet 0/1") {
		t.Errorf("supporters = %v", sup)
	}
}
