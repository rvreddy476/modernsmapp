package postgres

// Integration tests for migration 005 (live surfaces, hearts, supporters,
// the founding creator badge) against a real Postgres. Same guard as
// integration_test.go: LIVE_V2_TEST_DSN, and only a database whose name ends
// in _test.

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	dbschema "github.com/atpost/live-service-v2/database"
)

// itSet runs an UPDATE of one stream row (the tests place rows in time; the
// store itself only ever writes NOW()).
func itSet(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, set string, args ...any) {
	t.Helper()
	tag, err := pool.Exec(context.Background(), `UPDATE live_streams SET `+set+` WHERE id = $1`, append([]any{id}, args...)...)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("set %q: %v (%d rows)", set, err, tag.RowsAffected())
	}
}

// itTag is a category no other test or run uses, so listings can be
// filtered down to this test's rows in a shared database.
func itTag() string { return "it-" + uuid.NewString()[:13] }

func itIDs(rows []*LiveStream) []uuid.UUID {
	out := make([]uuid.UUID, len(rows))
	for i, st := range rows {
		out[i] = st.ID
	}
	return out
}

func itSame(t *testing.T, what string, got []*LiveStream, want ...*LiveStream) {
	t.Helper()
	g, w := itIDs(got), itIDs(want)
	if len(g) != len(w) {
		t.Fatalf("%s: %d rows %v, want %d %v", what, len(g), g, len(w), w)
	}
	for i := range w {
		if g[i] != w[i] {
			t.Fatalf("%s: row %d is %s, want %s (%v vs %v)", what, i, g[i], w[i], g, w)
		}
	}
}

func TestIntegrationSurfaceColumns(t *testing.T) {
	s, pool := integrationStore(t)
	ctx := context.Background()
	st := itStream(t, s, uuid.New())
	if st.Orientation != OrientationLandscape || st.Category != "" || st.HeartCount != 0 || st.RecordingPostID != nil {
		t.Fatalf("defaults: %+v", st)
	}
	p, err := s.CreateStream(ctx, CreateStreamParams{
		CreatorUserID: uuid.New(), LiveKitRoom: "stream_" + uuid.NewString(), Title: "p", Visibility: "public",
		Orientation: OrientationPortrait, Category: "music",
	})
	if err != nil || p.Orientation != OrientationPortrait || p.Category != "music" {
		t.Fatalf("portrait + category: %+v %v", p, err)
	}
	again, err := s.GetByRoom(ctx, p.LiveKitRoom)
	if err != nil || again.Orientation != OrientationPortrait || again.Category != "music" {
		t.Fatalf("read back: %+v %v", again, err)
	}
	// The CHECK constraint is the last line of defence.
	if _, err := s.CreateStream(ctx, CreateStreamParams{
		CreatorUserID: uuid.New(), LiveKitRoom: "stream_" + uuid.NewString(), Title: "x", Visibility: "public", Orientation: "square",
	}); err == nil || !strings.Contains(err.Error(), "live_streams_orientation_check") {
		t.Fatalf("orientation 'square' stored: %v", err)
	}
	// A row written before the migration reads as landscape, no category.
	var orientation, category string
	var hearts int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO live_streams (creator_user_id, livekit_room, title) VALUES ($1, $2, 'old')
		RETURNING orientation, category, heart_count`, uuid.New(), "stream_"+uuid.NewString()).Scan(&orientation, &category, &hearts); err != nil {
		t.Fatal(err)
	}
	if orientation != "landscape" || category != "" || hearts != 0 {
		t.Fatalf("column defaults: %q %q %d", orientation, category, hearts)
	}
}

func TestIntegrationDiscoveryQueries(t *testing.T) {
	s, pool := integrationStore(t)
	ctx := context.Background()
	tag := itTag()
	a, b := uuid.New(), uuid.New()
	base := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	mk := func(creator uuid.UUID, status, orientation string, viewers int, startedMin int) *LiveStream {
		st, err := s.CreateStream(ctx, CreateStreamParams{
			CreatorUserID: creator, LiveKitRoom: "stream_" + uuid.NewString(), Title: "d", Visibility: "public",
			Orientation: orientation, Category: tag,
		})
		if err != nil {
			t.Fatal(err)
		}
		itSet(t, pool, st.ID, `status = $2, viewer_count = $3, started_at = $4`, status, viewers, base.Add(time.Duration(startedMin)*time.Minute))
		got, _ := s.GetByID(ctx, st.ID)
		return got
	}
	wide10 := mk(a, StatusLive, OrientationLandscape, 10, 1)
	tall50 := mk(b, StatusLive, OrientationPortrait, 50, 2)
	tall10 := mk(b, StatusReconnecting, OrientationPortrait, 10, 3) // ties wide10 on viewers
	wide30 := mk(a, StatusLive, OrientationLandscape, 30, 4)
	mk(a, StatusStarting, OrientationLandscape, 99, 5) // not on air
	mk(a, StatusEnded, OrientationLandscape, 99, 6)

	f := StreamFilter{Category: tag}
	list := func(p ListLiveParams) []*LiveStream {
		t.Helper()
		rows, err := s.ListLive(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	itSame(t, "recent", list(ListLiveParams{Limit: 50, Filter: f}), wide30, tall10, tall50, wide10)
	itSame(t, "viewers", list(ListLiveParams{Limit: 50, Sort: SortViewers, Filter: f}), tall50, wide30, tall10, wide10)
	itSame(t, "portrait", list(ListLiveParams{Limit: 50, Sort: SortViewers, Filter: StreamFilter{Category: tag, Orientation: OrientationPortrait}}), tall50, tall10)
	itSame(t, "creator a", list(ListLiveParams{Limit: 50, Sort: SortViewers, Filter: StreamFilter{Category: tag, CreatorIDs: []uuid.UUID{a}}}), wide30, wide10)
	itSame(t, "both creators", list(ListLiveParams{Limit: 50, Filter: StreamFilter{Category: tag, CreatorIDs: []uuid.UUID{a, b}}}), wide30, tall10, tall50, wide10)
	itSame(t, "an empty creator list matches nothing", list(ListLiveParams{Limit: 50, Filter: StreamFilter{Category: tag, CreatorIDs: []uuid.UUID{}}}))
	itSame(t, "another category", list(ListLiveParams{Limit: 50, Filter: StreamFilter{Category: itTag()}}))

	// Keysets: page by one in both orders, every row exactly once.
	var walked []*LiveStream
	p := ListLiveParams{Limit: 1, Sort: SortViewers, Filter: f}
	for i := 0; i < 10; i++ {
		rows := list(p)
		if len(rows) == 0 {
			break
		}
		walked = append(walked, rows[0])
		v := rows[0].ViewerCount
		p.ViewersBefore, p.StartedBefore, p.IDBefore = &v, rows[0].StartedAt, &rows[0].ID
	}
	itSame(t, "viewers keyset walk", walked, tall50, wide30, tall10, wide10)
	walked = nil
	p = ListLiveParams{Limit: 1, Filter: f}
	for i := 0; i < 10; i++ {
		rows := list(p)
		if len(rows) == 0 {
			break
		}
		walked = append(walked, rows[0])
		p.StartedBefore, p.IDBefore = rows[0].StartedAt, &rows[0].ID
	}
	itSame(t, "recent keyset walk", walked, wide30, tall10, tall50, wide10)

	// Upcoming.
	now := time.Now().UTC()
	sched := func(creator uuid.UUID, in time.Duration, orientation string) *LiveStream {
		at := now.Add(in)
		st, err := s.CreateStream(ctx, CreateStreamParams{
			CreatorUserID: creator, LiveKitRoom: "stream_" + uuid.NewString(), Title: "u", Visibility: "public",
			ScheduledAt: &at, Orientation: orientation, Category: tag,
		})
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	soon := sched(a, time.Hour, OrientationLandscape)
	later := sched(b, 2*time.Hour, OrientationPortrait)
	overdue := sched(a, -time.Hour, OrientationLandscape)
	up, err := s.ListScheduled(ctx, ListScheduledParams{Limit: 50, Now: now, Filter: f})
	if err != nil {
		t.Fatal(err)
	}
	itSame(t, "upcoming", up, soon, later)
	up, _ = s.ListScheduled(ctx, ListScheduledParams{Limit: 50, Now: now, Filter: StreamFilter{Category: tag, Orientation: OrientationPortrait}})
	itSame(t, "upcoming portrait", up, later)
	// Unstarted: every scheduled stream of the creator — overdue ones and
	// ones with no time too — soonest first, the timeless last (by id).
	draft := func() *LiveStream {
		st, err := s.CreateStream(ctx, CreateStreamParams{CreatorUserID: a, LiveKitRoom: "stream_" + uuid.NewString(), Title: "draft", Visibility: "public", Category: tag})
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	d1, d2 := draft(), draft()
	if d2.ID.String() < d1.ID.String() {
		d1, d2 = d2, d1
	}
	mine := StreamFilter{Category: tag, CreatorIDs: []uuid.UUID{a}}
	up, err = s.ListScheduled(ctx, ListScheduledParams{Limit: 50, Unstarted: true, Filter: mine})
	if err != nil {
		t.Fatal(err)
	}
	itSame(t, "unstarted", up, overdue, soon, d1, d2)
	walked = nil
	sp := ListScheduledParams{Limit: 1, Unstarted: true, Filter: mine}
	for i := 0; i < 10; i++ {
		rows, err := s.ListScheduled(ctx, sp)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			break
		}
		walked = append(walked, rows[0])
		key := ScheduledSortKey(rows[0])
		sp.ScheduledAfter, sp.IDAfter = &key, &rows[0].ID
	}
	itSame(t, "unstarted keyset walk, through the timeless ones", walked, overdue, soon, d1, d2)
	// Without Unstarted the drafts and the overdue one are not upcoming.
	up, _ = s.ListScheduled(ctx, ListScheduledParams{Limit: 50, Now: now, Filter: mine})
	itSame(t, "upcoming for anyone else", up, soon)
	up, _ = s.ListScheduled(ctx, ListScheduledParams{Limit: 50, Now: now, Filter: f, ScheduledAfter: soon.ScheduledAt, IDAfter: &soon.ID})
	itSame(t, "upcoming after the cursor", up, later)

	// Past: ended streams that were live, newest end first.
	past := func(startedAgo, endedAgo time.Duration, status string, started bool) *LiveStream {
		st, err := s.CreateStream(ctx, CreateStreamParams{CreatorUserID: a, LiveKitRoom: "stream_" + uuid.NewString(), Title: "p", Visibility: "public", Category: tag})
		if err != nil {
			t.Fatal(err)
		}
		if started {
			itSet(t, pool, st.ID, `status = $2, started_at = $3, ended_at = $4`, status, now.Add(-startedAgo), now.Add(-endedAgo))
		} else {
			itSet(t, pool, st.ID, `status = $2, ended_at = $3`, status, now.Add(-endedAgo))
		}
		got, _ := s.GetByID(ctx, st.ID)
		return got
	}
	old := past(3*time.Hour, 2*time.Hour, StatusEnded, true)
	recent := past(time.Hour, 30*time.Minute, StatusEnded, true)
	past(time.Hour, 20*time.Minute, StatusFailed, true) // failed
	past(0, 10*time.Minute, StatusEnded, false)         // never live
	got, err := s.ListPast(ctx, ListPastParams{Limit: 50, Filter: StreamFilter{Category: tag, CreatorIDs: []uuid.UUID{a}}})
	if err != nil {
		t.Fatal(err)
	}
	itSame(t, "past", got, recent, old)
	got, _ = s.ListPast(ctx, ListPastParams{Limit: 50, Filter: StreamFilter{Category: tag}, EndedBefore: recent.EndedAt, IDBefore: &recent.ID})
	itSame(t, "past after the cursor", got, old)
}

func TestIntegrationUpdateScheduled(t *testing.T) {
	s, pool := integrationStore(t)
	ctx := context.Background()
	cover := uuid.New()
	when := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	st, err := s.CreateStream(ctx, CreateStreamParams{
		CreatorUserID: uuid.New(), LiveKitRoom: "stream_" + uuid.NewString(), Title: "before", Description: "d",
		Visibility: "public", CoverMediaID: &cover, ScheduledAt: &when, Category: "music",
	})
	if err != nil {
		t.Fatal(err)
	}
	str := func(v string) *string { return &v }

	// Nothing set: nothing changes.
	same, err := s.UpdateScheduled(ctx, st.ID, StreamPatch{})
	if err != nil || same.Title != "before" || same.Category != "music" || same.CoverMediaID == nil || !same.ScheduledAt.Equal(when) || same.Visibility != "public" {
		t.Fatalf("empty patch: %+v %v", same, err)
	}
	newCover := uuid.New()
	later := when.Add(time.Hour)
	got, err := s.UpdateScheduled(ctx, st.ID, StreamPatch{
		Title: str("after"), Description: str(""), Category: str("gaming"), Visibility: str("followers"), Orientation: str(OrientationPortrait),
		SetCoverMediaID: true, CoverMediaID: &newCover, SetScheduledAt: true, ScheduledAt: &later,
	})
	if err != nil || got.Title != "after" || got.Description != "" || got.Category != "gaming" || got.Visibility != "followers" ||
		got.Orientation != OrientationPortrait || *got.CoverMediaID != newCover || !got.ScheduledAt.Equal(later) {
		t.Fatalf("patch: %+v %v", got, err)
	}
	if !got.UpdatedAt.After(st.UpdatedAt) {
		t.Fatalf("updated_at did not move: %v -> %v", st.UpdatedAt, got.UpdatedAt)
	}
	cleared, err := s.UpdateScheduled(ctx, st.ID, StreamPatch{Category: str(""), SetCoverMediaID: true, SetScheduledAt: true})
	if err != nil || cleared.Category != "" || cleared.CoverMediaID != nil || cleared.ScheduledAt != nil || cleared.Title != "after" {
		t.Fatalf("clearing: %+v %v", cleared, err)
	}

	if _, err := s.UpdateScheduled(ctx, uuid.New(), StreamPatch{Title: str("x")}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown stream: %v", err)
	}
	for _, status := range []string{StatusStarting, StatusLive, StatusReconnecting, StatusEnded, StatusFailed} {
		itSet(t, pool, st.ID, `status = $2`, status)
		if _, err := s.UpdateScheduled(ctx, st.ID, StreamPatch{Title: str("edited while " + status)}); !errors.Is(err, ErrStateConflict) {
			t.Fatalf("edit while %s: %v", status, err)
		}
	}
	if final, _ := s.GetByID(ctx, st.ID); final.Title != "after" {
		t.Fatalf("a refused edit changed the title to %q", final.Title)
	}
}

func TestIntegrationReminders(t *testing.T) {
	s, pool := integrationStore(t)
	ctx := context.Background()
	host := uuid.New()
	st := itStream(t, s, host)
	other := itStream(t, s, host)
	users := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()}

	for i, u := range users {
		n, err := s.SetReminder(ctx, st.ID, u, true)
		if err != nil || n != i+1 {
			t.Fatalf("set %d: %d %v", i, n, err)
		}
	}
	if n, err := s.SetReminder(ctx, st.ID, users[0], true); err != nil || n != 5 {
		t.Fatalf("set twice: %d %v", n, err)
	}
	if n, err := s.SetReminder(ctx, st.ID, users[4], false); err != nil || n != 4 {
		t.Fatalf("delete: %d %v", n, err)
	}
	if n, err := s.SetReminder(ctx, st.ID, users[4], false); err != nil || n != 4 {
		t.Fatalf("delete twice: %d %v", n, err)
	}
	if _, err := s.SetReminder(ctx, uuid.New(), users[0], true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown stream: %v", err)
	}

	stats, err := s.ReminderStats(ctx, []uuid.UUID{st.ID, other.ID}, users[1])
	if err != nil || stats[st.ID] != (ReminderStat{Count: 4, Set: true}) {
		t.Fatalf("stats: %+v %v", stats, err)
	}
	if _, has := stats[other.ID]; has {
		t.Fatalf("a stream without reminders is in the map: %+v", stats)
	}
	stats, _ = s.ReminderStats(ctx, []uuid.UUID{st.ID}, users[4])
	if stats[st.ID] != (ReminderStat{Count: 4, Set: false}) {
		t.Fatalf("stats for someone who removed theirs: %+v", stats)
	}
	stats, _ = s.ReminderStats(ctx, []uuid.UUID{st.ID}, uuid.Nil)
	if stats[st.ID] != (ReminderStat{Count: 4, Set: false}) {
		t.Fatalf("stats for nobody: %+v", stats)
	}
	if empty, err := s.ReminderStats(ctx, nil, users[0]); err != nil || len(empty) != 0 {
		t.Fatalf("no streams: %+v %v", empty, err)
	}

	// Paged by user id, each once.
	seen := map[uuid.UUID]bool{}
	after := uuid.Nil
	for page := 0; page < 5; page++ {
		ids, err := s.ListReminderUserIDs(ctx, st.ID, after, 3)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			if seen[id] || id.String() <= after.String() {
				t.Fatalf("page %d repeats or is out of order: %s after %s", page, id, after)
			}
			seen[id], after = true, id
		}
		if len(ids) < 3 {
			break
		}
	}
	if len(seen) != 4 || seen[users[4]] {
		t.Fatalf("paged %d users (deleted one included: %v)", len(seen), seen[users[4]])
	}

	// Not while anything but scheduled; the rows stay for the notification
	// that goes out when it starts.
	for _, status := range []string{StatusStarting, StatusLive, StatusReconnecting, StatusEnded, StatusFailed} {
		itSet(t, pool, st.ID, `status = $2`, status)
		for _, set := range []bool{true, false} {
			if _, err := s.SetReminder(ctx, st.ID, uuid.New(), set); !errors.Is(err, ErrStateConflict) {
				t.Fatalf("reminder(set=%v) while %s: %v", set, status, err)
			}
		}
	}
	if ids, _ := s.ListReminderUserIDs(ctx, st.ID, uuid.Nil, 100); len(ids) != 4 {
		t.Fatalf("reminders after the start: %d, want 4", len(ids))
	}
}

func TestIntegrationHearts(t *testing.T) {
	s, pool := integrationStore(t)
	ctx := context.Background()
	host, viewer := uuid.New(), uuid.New()
	st := itStream(t, s, host)

	for _, status := range []string{StatusScheduled, StatusStarting, StatusEnded, StatusFailed} {
		itSet(t, pool, st.ID, `status = $2`, status)
		if _, err := s.AddHearts(ctx, st.ID, viewer, 5, 100); !errors.Is(err, ErrNotOnAir) {
			t.Fatalf("hearts while %s: %v", status, err)
		}
	}
	if _, err := s.AddHearts(ctx, uuid.New(), viewer, 5, 100); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown stream: %v", err)
	}
	itSet(t, pool, st.ID, `status = 'live'`)
	updatedBefore, _ := s.GetByID(ctx, st.ID)

	res, err := s.AddHearts(ctx, st.ID, viewer, 60, 100)
	if err != nil || res != (HeartResult{Added: 60, Total: 60}) {
		t.Fatalf("first batch: %+v %v", res, err)
	}
	res, err = s.AddHearts(ctx, st.ID, viewer, 60, 100) // crosses the cap
	if err != nil || res != (HeartResult{Added: 40, Total: 100}) {
		t.Fatalf("crossing the cap: %+v %v", res, err)
	}
	res, err = s.AddHearts(ctx, st.ID, viewer, 60, 100) // past it: accepted, ignored
	if err != nil || res != (HeartResult{Added: 0, Total: 100}) {
		t.Fatalf("past the cap: %+v %v", res, err)
	}
	itSet(t, pool, st.ID, `status = 'reconnecting'`)
	res, err = s.AddHearts(ctx, st.ID, host, 7, 100) // the host's count too
	if err != nil || res != (HeartResult{Added: 7, Total: 107}) {
		t.Fatalf("host hearts while reconnecting: %+v %v", res, err)
	}
	row, _ := s.GetByID(ctx, st.ID)
	if row.HeartCount != 107 {
		t.Fatalf("heart_count on the row: %d", row.HeartCount)
	}
	if !row.UpdatedAt.Equal(updatedBefore.UpdatedAt) && row.UpdatedAt.Sub(updatedBefore.UpdatedAt) > time.Minute {
		t.Fatalf("unexpected updated_at move")
	}
	var mine int
	if err := pool.QueryRow(ctx, `SELECT hearts FROM live_stream_hearts WHERE stream_id = $1 AND user_id = $2`, st.ID, viewer).Scan(&mine); err != nil || mine != 100 {
		t.Fatalf("the viewer's row: %d %v", mine, err)
	}

	// Concurrent batches: the total is exact and the cap holds.
	race := itStream(t, s, host)
	itSet(t, pool, race.ID, `status = 'live'`)
	capped, free := uuid.New(), []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := s.AddHearts(ctx, race.ID, capped, 20, 50); err != nil { // 240 asked, 50 allowed
				errs <- err
			}
			if _, err := s.AddHearts(ctx, race.ID, free[i%3], 5, 10000); err != nil { // 60 in all
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent hearts: %v", err)
	}
	final, _ := s.GetByID(ctx, race.ID)
	var sum, cappedRow int
	_ = pool.QueryRow(ctx, `SELECT COALESCE(SUM(hearts), 0) FROM live_stream_hearts WHERE stream_id = $1`, race.ID).Scan(&sum)
	_ = pool.QueryRow(ctx, `SELECT hearts FROM live_stream_hearts WHERE stream_id = $1 AND user_id = $2`, race.ID, capped).Scan(&cappedRow)
	if final.HeartCount != 110 || sum != 110 || cappedRow != 50 {
		t.Fatalf("after the race: heart_count=%d sum(rows)=%d capped row=%d, want 110 / 110 / 50", final.HeartCount, sum, cappedRow)
	}
}

func TestIntegrationSupporters(t *testing.T) {
	s, pool := integrationStore(t)
	ctx := context.Background()
	host := uuid.New()
	st := itStream(t, s, host)
	itSet(t, pool, st.ID, `status = 'live'`)
	u := map[string]uuid.UUID{}
	for _, n := range []string{"top", "chatty", "early", "late", "msgOnly", "removedOnly", "streamBanned", "liveBanned"} {
		u[n] = uuid.New()
	}
	t0 := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	hearts := func(name string, n, minute int) {
		t.Helper()
		if _, err := s.AddHearts(ctx, st.ID, u[name], n, 10000); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE live_stream_hearts SET created_at = $3 WHERE stream_id = $1 AND user_id = $2`,
			st.ID, u[name], t0.Add(time.Duration(minute)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	chat := func(name string, minute int) uuid.UUID {
		t.Helper()
		m, err := s.InsertChatMessage(ctx, st.ID, u[name], "hello")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE live_chat_messages SET created_at = $2 WHERE id = $1`, m.ID, t0.Add(time.Duration(minute)*time.Minute)); err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
	hearts("top", 20, 30)
	hearts("chatty", 5, 20)
	chat("chatty", 21)
	chat("chatty", 22)
	removed := chat("chatty", 23)
	hearts("late", 5, 10)
	chat("late", 11)
	// "early" hearted after "late" but chatted before: the earliest activity
	// of either kind decides.
	hearts("early", 5, 40)
	chat("early", 5)
	chat("msgOnly", 50)
	gone := chat("removedOnly", 1)
	for _, id := range []uuid.UUID{removed, gone} {
		if _, err := s.RemoveChatMessage(ctx, st.ID, id, host); err != nil {
			t.Fatal(err)
		}
	}
	hearts("streamBanned", 19, 2)
	hearts("liveBanned", 18, 3)
	if _, err := s.AddHearts(ctx, st.ID, host, 20, 10000); err != nil { // the host is never a supporter
		t.Fatal(err)
	}
	if _, err := s.InsertChatMessage(ctx, st.ID, host, "welcome"); err != nil {
		t.Fatal(err)
	}
	if err := s.BanFromStream(ctx, st.ID, u["streamBanned"], host, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.AdminSetPlatformBan(ctx, u["liveBanned"], true, "x", AuditEntry{ActorID: uuid.New(), Action: "user.live_ban", TargetType: "user", TargetID: u["liveBanned"].String(), Reason: "x"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM live_platform_bans WHERE user_id = $1`, u["liveBanned"])
	})

	rows, err := s.ListSupporters(ctx, st.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	type want struct {
		name             string
		hearts, messages int
	}
	wants := []want{{"top", 20, 0}, {"chatty", 5, 2}, {"early", 5, 1}, {"late", 5, 1}, {"msgOnly", 0, 1}}
	if len(rows) != len(wants) {
		t.Fatalf("supporters: %d rows, want %d: %+v", len(rows), len(wants), rows)
	}
	for i, w := range wants {
		if rows[i].UserID != u[w.name] || rows[i].Hearts != w.hearts || rows[i].Messages != w.messages {
			t.Fatalf("rank %d: %+v, want %s %d/%d", i+1, rows[i], w.name, w.hearts, w.messages)
		}
	}
	if !rows[2].FirstAt.Equal(t0.Add(5 * time.Minute)) {
		t.Fatalf("first activity of 'early': %v, want their first message", rows[2].FirstAt)
	}
	if top, _ := s.ListSupporters(ctx, st.ID, 2); len(top) != 2 || top[1].UserID != u["chatty"] {
		t.Fatalf("limit 2: %+v", top)
	}
	// After the stream ended.
	itSet(t, pool, st.ID, `status = 'ended'`)
	if after, err := s.ListSupporters(ctx, st.ID, 10); err != nil || len(after) != len(wants) {
		t.Fatalf("after the end: %d %v", len(after), err)
	}
	if none, err := s.ListSupporters(ctx, itStream(t, s, host).ID, 10); err != nil || none == nil || len(none) != 0 {
		t.Fatalf("no supporters: %#v %v", none, err)
	}
}

// itOnAir puts st on air `ago` before now and returns the stream.
func itOnAir(t *testing.T, s *Store, pool *pgxpool.Pool, creator uuid.UUID, ago time.Duration) *LiveStream {
	t.Helper()
	st := itStream(t, s, creator)
	itSet(t, pool, st.ID, `status = 'live', started_at = NOW() - make_interval(secs => $2)`, ago.Seconds())
	return st
}

func itBadge(t *testing.T, pool *pgxpool.Pool, user uuid.UUID) (streamID *uuid.UUID, revoked bool, found bool) {
	t.Helper()
	var revokedAt *time.Time
	err := pool.QueryRow(context.Background(),
		`SELECT stream_id, revoked_at FROM live_creator_badges WHERE user_id = $1 AND badge = 'founding_creator'`, user).Scan(&streamID, &revokedAt)
	if err != nil {
		return nil, false, false
	}
	return streamID, revokedAt != nil, true
}

// TestIntegrationFoundingBadge: the grant is part of the transition that
// ends the stream, by the database's own started_at and ended_at.
func TestIntegrationFoundingBadge(t *testing.T) {
	s, pool := integrationStore(t)
	ctx := context.Background()
	end := to(StatusEnded, "host_ended")

	short, long := uuid.New(), uuid.New()
	a := itOnAir(t, s, pool, short, 5*time.Minute-2*time.Second)
	if _, err := s.ApplyTransition(ctx, a.ID, end, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, found := itBadge(t, pool, short); found {
		t.Fatal("under five minutes on air earned the badge")
	}
	b := itOnAir(t, s, pool, long, 5*time.Minute+time.Second)
	res, err := s.ApplyTransition(ctx, b.ID, end, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	sid, revoked, found := itBadge(t, pool, long)
	if !found || revoked || sid == nil || *sid != b.ID {
		t.Fatalf("five minutes on air: found=%v revoked=%v stream=%v", found, revoked, sid)
	}
	list, err := s.ListBadges(ctx, long)
	if err != nil || len(list) != 1 || list[0].Badge != BadgeFoundingCreator || !list[0].GrantedAt.Equal(*res.Next.EndedAt) {
		t.Fatalf("badges: %+v %v (ended_at %v)", list, err, res.Next.EndedAt)
	}

	// An events error rolls the grant back with the status.
	rolled := uuid.New()
	c := itOnAir(t, s, pool, rolled, time.Hour)
	boom := errors.New("boom")
	if _, err := s.ApplyTransition(ctx, c.ID, end, func(_, _ *LiveStream) ([]OutboxEvent, error) { return nil, boom }, nil); !errors.Is(err, boom) {
		t.Fatalf("events error: %v", err)
	}
	if _, _, found := itBadge(t, pool, rolled); found {
		t.Fatal("the grant survived a rolled-back transition")
	}

	// failed is not ended; a stream that never went live has no started_at.
	failed, never := uuid.New(), uuid.New()
	d := itOnAir(t, s, pool, failed, time.Hour)
	if _, err := s.ApplyTransition(ctx, d.ID, to(StatusFailed, "no_media"), nil, nil); err != nil {
		t.Fatal(err)
	}
	e := itStream(t, s, never)
	itSet(t, pool, e.ID, `created_at = NOW() - INTERVAL '1 hour'`)
	if _, err := s.ApplyTransition(ctx, e.ID, end, nil, nil); err != nil {
		t.Fatal(err)
	}
	for name, user := range map[string]uuid.UUID{"failed": failed, "never live": never} {
		if _, _, found := itBadge(t, pool, user); found {
			t.Fatalf("a %s stream earned the badge", name)
		}
	}

	// Idempotent and permanent: a second qualifying stream keeps the first.
	f := itOnAir(t, s, pool, long, time.Hour)
	if _, err := s.ApplyTransition(ctx, f.ID, end, nil, nil); err != nil {
		t.Fatal(err)
	}
	if sid, _, _ := itBadge(t, pool, long); sid == nil || *sid != b.ID {
		t.Fatalf("a second stream replaced the badge's stream: %v", sid)
	}

	// The window: started_at must be before Until.
	closed := time.Now().Add(-30 * time.Minute)
	windowed := New(pool)
	windowed.SetFoundingRule(FoundingRule{Until: &closed})
	inside, outside := uuid.New(), uuid.New()
	g := itOnAir(t, windowed, pool, inside, 40*time.Minute)  // started before it closed
	h := itOnAir(t, windowed, pool, outside, 20*time.Minute) // started after
	for _, st := range []*LiveStream{g, h} {
		if _, err := windowed.ApplyTransition(ctx, st.ID, end, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, found := itBadge(t, pool, inside); !found {
		t.Fatal("a stream that started inside the window did not earn the badge")
	}
	if _, _, found := itBadge(t, pool, outside); found {
		t.Fatal("a stream that started after the window closed earned the badge")
	}
	// LIVE_FOUNDING_MIN_LIVE.
	quick := New(pool)
	quick.SetFoundingRule(FoundingRule{MinLive: 30 * time.Second})
	fast := uuid.New()
	i := itOnAir(t, quick, pool, fast, 31*time.Second)
	if _, err := quick.ApplyTransition(ctx, i.ID, end, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, found := itBadge(t, pool, fast); !found {
		t.Fatal("a 30s rule did not grant after 31s on air")
	}

	// The account-hide path ends streams by SQL and grants too.
	hidden, hiddenShort := uuid.New(), uuid.New()
	j := itOnAir(t, s, pool, hidden, 10*time.Minute)
	itOnAir(t, s, pool, hiddenShort, time.Minute)
	for _, user := range []uuid.UUID{hidden, hiddenShort} {
		if err := s.SetUserHidden(ctx, user, true, "user.deactivated"); err != nil {
			t.Fatal(err)
		}
	}
	if sid, _, found := itBadge(t, pool, hidden); !found || *sid != j.ID {
		t.Fatalf("hide path: found=%v stream=%v", found, sid)
	}
	if _, _, found := itBadge(t, pool, hiddenShort); found {
		t.Fatal("hide path granted for a one-minute stream")
	}
}

func TestIntegrationBadgeRevokeAndVisibility(t *testing.T) {
	s, pool := integrationStore(t)
	ctx := context.Background()
	end := to(StatusEnded, "host_ended")
	user, admin := uuid.New(), uuid.New()
	st := itOnAir(t, s, pool, user, 10*time.Minute)
	if _, err := s.ApplyTransition(ctx, st.ID, end, nil, nil); err != nil {
		t.Fatal(err)
	}
	audit := AuditEntry{ActorID: admin, Action: "user.badge_revoke", TargetType: "user", TargetID: user.String(), Reason: "bought viewers"}
	audits := func() int {
		var n int
		_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM live_admin_audit WHERE action = 'user.badge_revoke' AND target_id = $1`, user.String()).Scan(&n)
		return n
	}

	// Hidden while platform live-banned; back after.
	if got, _ := s.BadgesFor(ctx, []uuid.UUID{user, uuid.New()}); len(got) != 1 || len(got[user]) != 1 || got[user][0] != BadgeFoundingCreator {
		t.Fatalf("badges for: %+v", got)
	}
	ban := AuditEntry{ActorID: admin, Action: "user.live_ban", TargetType: "user", TargetID: user.String(), Reason: "x"}
	if err := s.AdminSetPlatformBan(ctx, user, true, "x", ban); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.BadgesFor(ctx, []uuid.UUID{user}); len(got) != 0 {
		t.Fatalf("a live-banned user's badge is visible: %+v", got)
	}
	if list, _ := s.ListBadges(ctx, user); list == nil || len(list) != 0 {
		t.Fatalf("a live-banned user's badge is listed: %#v", list)
	}
	if err := s.AdminSetPlatformBan(ctx, user, false, "", ban); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.BadgesFor(ctx, []uuid.UUID{user}); len(got[user]) != 1 {
		t.Fatalf("the badge did not come back after the unban: %+v", got)
	}

	if _, err := s.AdminRevokeBadge(ctx, uuid.New(), BadgeFoundingCreator, "x", audit); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoking a badge nobody has: %v", err)
	}
	if audits() != 0 {
		t.Fatal("a refused revoke wrote an audit row")
	}
	revoked, err := s.AdminRevokeBadge(ctx, user, BadgeFoundingCreator, "bought viewers", audit)
	if err != nil || !revoked || audits() != 1 {
		t.Fatalf("revoke: %v %v audits=%d", revoked, err, audits())
	}
	revoked, err = s.AdminRevokeBadge(ctx, user, BadgeFoundingCreator, "again", audit)
	if err != nil || revoked || audits() != 1 {
		t.Fatalf("second revoke: %v %v audits=%d", revoked, err, audits())
	}
	if got, _ := s.BadgesFor(ctx, []uuid.UUID{user}); len(got) != 0 {
		t.Fatalf("a revoked badge is visible: %+v", got)
	}
	var by *uuid.UUID
	var reason *string
	if err := pool.QueryRow(ctx, `SELECT revoked_by, revoked_reason FROM live_creator_badges WHERE user_id = $1`, user).Scan(&by, &reason); err != nil || by == nil || *by != admin || *reason != "bought viewers" {
		t.Fatalf("the revoked row: %v %v %v", by, reason, err)
	}
	// Never granted again.
	again := itOnAir(t, s, pool, user, time.Hour)
	if _, err := s.ApplyTransition(ctx, again.ID, end, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, isRevoked, found := itBadge(t, pool, user); !found || !isRevoked {
		t.Fatalf("after a later qualifying stream: found=%v revoked=%v", found, isRevoked)
	}
}

// TestIntegrationMigrationBackfill runs migration 005's backfill statement
// again over rows made here: it must grant exactly to creators with an
// ended stream that was on air five minutes, from the earliest such stream.
func TestIntegrationMigrationBackfill(t *testing.T) {
	s, pool := integrationStore(t)
	ctx := context.Background()
	raw, err := fs.ReadFile(dbschema.Migrations, "migrations/005_live_surfaces.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ReplaceAll(string(raw), "\r\n", "\n")
	at := strings.LastIndex(sql, "INSERT INTO live_creator_badges")
	if at < 0 {
		t.Fatal("the backfill statement is not in the migration")
	}
	backfill := sql[at:]

	mk := func(creator uuid.UUID, status string, startedAgo, endedAgo time.Duration, started bool) *LiveStream {
		st := itStream(t, s, creator)
		if started {
			itSet(t, pool, st.ID, `status = $2, started_at = NOW() - make_interval(secs => $3), ended_at = NOW() - make_interval(secs => $4)`,
				status, startedAgo.Seconds(), endedAgo.Seconds())
		} else {
			itSet(t, pool, st.ID, `status = $2, ended_at = NOW() - make_interval(secs => $3)`, status, endedAgo.Seconds())
		}
		return st
	}
	qualifies, short, failed, never, live := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	mk(qualifies, StatusEnded, 2*time.Hour, 2*time.Hour-4*time.Minute, true) // too short, and older
	first := mk(qualifies, StatusEnded, 90*time.Minute, 80*time.Minute, true)
	mk(qualifies, StatusEnded, 60*time.Minute, 10*time.Minute, true) // qualifies too, later
	mk(short, StatusEnded, 10*time.Minute, 6*time.Minute, true)
	mk(failed, StatusFailed, time.Hour, 10*time.Minute, true)
	mk(never, StatusEnded, 0, 10*time.Minute, false)
	itOnAir(t, s, pool, live, time.Hour)

	for run := 0; run < 2; run++ { // idempotent
		if _, err := pool.Exec(ctx, backfill); err != nil {
			t.Fatalf("backfill run %d: %v", run, err)
		}
	}
	sid, _, found := itBadge(t, pool, qualifies)
	if !found || sid == nil || *sid != first.ID {
		t.Fatalf("backfill: found=%v stream=%v, want the earliest qualifying stream %s", found, sid, first.ID)
	}
	for name, user := range map[string]uuid.UUID{"short": short, "failed": failed, "never live": never, "still live": live} {
		if _, _, found := itBadge(t, pool, user); found {
			t.Fatalf("backfill granted to a creator whose only stream was %s", name)
		}
	}
}

func TestIntegrationPurgeCoversSurfaceTables(t *testing.T) {
	s, pool := integrationStore(t)
	ctx := context.Background()
	host, fan := uuid.New(), uuid.New()
	mine := itOnAir(t, s, pool, host, 10*time.Minute)
	theirs := itOnAir(t, s, pool, fan, 10*time.Minute)
	sched := itStream(t, s, fan)
	if _, err := s.SetReminder(ctx, sched.ID, host, true); err != nil {
		t.Fatal(err)
	}
	mySched := itStream(t, s, host)
	if _, err := s.SetReminder(ctx, mySched.ID, fan, true); err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct{ stream, user uuid.UUID }{{theirs.ID, host}, {mine.ID, fan}, {theirs.ID, fan}} {
		if _, err := s.AddHearts(ctx, x.stream, x.user, 5, 100); err != nil {
			t.Fatal(err)
		}
	}
	for _, st := range []*LiveStream{mine, theirs} {
		if _, err := s.ApplyTransition(ctx, st.ID, to(StatusEnded, "host_ended"), nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PurgeUser(ctx, host); err != nil {
		t.Fatal(err)
	}
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	for what, n := range map[string]int{
		"the host's reminders anywhere":   count(`SELECT COUNT(*) FROM live_stream_reminders WHERE user_id = $1`, host),
		"reminders on the host's streams": count(`SELECT COUNT(*) FROM live_stream_reminders WHERE stream_id = $1`, mySched.ID),
		"the host's hearts anywhere":      count(`SELECT COUNT(*) FROM live_stream_hearts WHERE user_id = $1`, host),
		"hearts on the host's streams":    count(`SELECT COUNT(*) FROM live_stream_hearts WHERE stream_id = $1`, mine.ID),
		"the host's badges":               count(`SELECT COUNT(*) FROM live_creator_badges WHERE user_id = $1`, host),
		"the host's streams":              count(`SELECT COUNT(*) FROM live_streams WHERE creator_user_id = $1`, host),
	} {
		if n != 0 {
			t.Fatalf("purge left %d rows: %s", n, what)
		}
	}
	// Someone else's rows stay, and their stream's total is not rewritten.
	if n := count(`SELECT COUNT(*) FROM live_creator_badges WHERE user_id = $1`, fan); n != 1 {
		t.Fatalf("the purge took another user's badge: %d", n)
	}
	if n := count(`SELECT COUNT(*) FROM live_stream_hearts WHERE stream_id = $1 AND user_id = $2`, theirs.ID, fan); n != 1 {
		t.Fatalf("the purge took another user's hearts: %d", n)
	}
	if n := count(`SELECT heart_count FROM live_streams WHERE id = $1`, theirs.ID); n != 10 {
		t.Fatalf("heart_count of a stream the purged user hearted: %d, want 10", n)
	}
	if err := s.PurgeUser(ctx, host); err != nil {
		t.Fatalf("second purge: %v", err)
	}
}
