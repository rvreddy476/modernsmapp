//go:build integration

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/atpost/analytics-service/internal/aggregation"
	"github.com/atpost/analytics-service/internal/model"
	pgstore "github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/atpost/analytics-service/internal/testsupport"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// sessionRig is one viewer on one 30-second flick, through the real
// ingest service into the package's private database.
type sessionRig struct {
	ctx     context.Context
	pool    *pgxpool.Pool
	store   *pgstore.Store
	svc     *IngestService
	creator uuid.UUID
	actor   uuid.UUID
	content uuid.UUID
	now     time.Time
	seq     int
}

const rigDurationMS = 30_000

func newSessionRig(t *testing.T) *sessionRig {
	t.Helper()
	testsupport.RequireTestDSN(t)
	ctx := context.Background()
	// Own database, own truncates: see internal/testsupport.
	pool := testsupport.Pool(t, "service")
	if _, err := pool.Exec(ctx, `TRUNCATE analytics.ingest_receipts, analytics.events_raw,
		analytics.content_ownership, analytics.playback_sessions CASCADE`); err != nil {
		t.Fatal(err)
	}
	store := pgstore.New(pool)
	r := &sessionRig{
		ctx: ctx, pool: pool, store: store, svc: New(ctx, store, nil),
		creator: uuid.New(), actor: uuid.New(), content: uuid.New(),
		now: time.Now().UTC().Truncate(time.Second),
	}
	if err := store.UpsertContentOwnership(ctx, pgstore.ContentOwnership{
		ContentID: r.content, CreatorID: r.creator, ContentType: model.ContentTypeFlick,
		CreatedAt: r.now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *sessionRig) event(eventType string, session uuid.UUID, at time.Time, extra map[string]any) EventDTO {
	r.seq++
	payload := map[string]any{
		"content_id": r.content.String(), "session_id": session.String(), "surface": "feed",
	}
	for k, v := range extra {
		payload[k] = v
	}
	raw, _ := json.Marshal(payload)
	return EventDTO{EventID: fmt.Sprintf("evt-session-rig-%016d", r.seq), Type: eventType, Payload: raw, Timestamp: at}
}

// playStartAndHeartbeats is a client that started a 30s flick and beat
// every two seconds for ten seconds, then went silent: no play_end.
func (r *sessionRig) playStartAndHeartbeats(session uuid.UUID, start time.Time) []EventDTO {
	batch := []EventDTO{
		r.event(model.EventPlayStart, session, start, map[string]any{"content_duration_ms": rigDurationMS, "start_method": "autoplay"}),
	}
	for total := int64(2_000); total <= 10_000; total += 2_000 {
		batch = append(batch, r.event(model.EventWatchHeartbeat, session, start.Add(time.Duration(total)*time.Millisecond), map[string]any{
			"watched_ms_increment": 2_000, "watched_ms_total": total,
			"playhead_position_ms": total, "playback_speed": 1.0,
		}))
	}
	return batch
}

func (r *sessionRig) ingest(t *testing.T, batch []EventDTO) IngestResult {
	t.Helper()
	result, err := r.svc.IngestEvents(r.ctx, r.actor.String(), batch)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	return result
}

func (r *sessionRig) session(t *testing.T, session uuid.UUID) *pgstore.PlaybackSession {
	t.Helper()
	row, err := r.store.GetPlaybackSession(r.ctx, r.actor, session, r.content)
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	return row
}

func (r *sessionRig) sessionCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := r.pool.QueryRow(r.ctx, `SELECT count(*) FROM analytics.playback_sessions
		WHERE actor_id = $1 AND content_id = $2`, r.actor, r.content).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// finalizerAt is the finaliser with its clock pinned, so "ten minutes of
// silence" is asserted rather than waited for.
func finalizerAt(store *pgstore.Store, now time.Time) *aggregation.SessionFinalizer {
	return aggregation.NewSessionFinalizer(store).WithClock(func() time.Time { return now })
}

// The bug this pins down (audit M-08): play_end was the sole carrier of
// a view. A viewer who watched ten seconds of a flick and then closed
// the tab before the final event flushed was never counted. Now the
// heartbeats build the session and the finaliser closes it.
func TestSessionSurvivesLostPlayEnd(t *testing.T) {
	r := newSessionRig(t)
	session := uuid.New()
	start := r.now.Add(-30 * time.Second)

	if result := r.ingest(t, r.playStartAndHeartbeats(session, start)); result.Accepted != 6 {
		t.Fatalf("accepted=%d want 6 (play_start + 5 heartbeats)", result.Accepted)
	}

	open := r.session(t, session)
	if open.FinalizedAt != nil {
		t.Fatalf("session finalised before any silence: %+v", open)
	}
	if open.WatchedMS != 10_000 || open.ContentDurationMS != rigDurationMS || open.MaxPlayheadMS != 10_000 {
		t.Fatalf("open session totals: watched=%d duration=%d playhead=%d", open.WatchedMS, open.ContentDurationMS, open.MaxPlayheadMS)
	}
	if open.CoveredMS != 10_000 || math.Abs(open.PercentCovered-33.33) > 0.1 {
		t.Fatalf("coverage: covered_ms=%d percent_covered=%.2f want 10000 / 33.33", open.CoveredMS, open.PercentCovered)
	}
	if !open.FirstSeen.Equal(start) {
		t.Fatalf("first_seen=%s want the play_start %s", open.FirstSeen, start)
	}
	if open.IsSelfView {
		t.Fatal("a stranger's session was stamped as a self-view")
	}

	// Nine minutes of silence is not yet inactivity.
	closed, err := finalizerAt(r.store, r.now.Add(9*time.Minute)).RunOnce(r.ctx)
	if err != nil || closed != 0 {
		t.Fatalf("premature finalise: closed=%d err=%v", closed, err)
	}
	// Eleven is.
	closed, err = finalizerAt(r.store, r.now.Add(11*time.Minute)).RunOnce(r.ctx)
	if err != nil || closed != 1 {
		t.Fatalf("finalise: closed=%d err=%v, want 1", closed, err)
	}

	done := r.session(t, session)
	if done.FinalizedAt == nil || done.FinalizeReason == nil || *done.FinalizeReason != pgstore.FinalizeInactivity {
		t.Fatalf("not finalised by inactivity: %+v", done)
	}
	if !done.IsDisplayView {
		t.Fatal("ten seconds of a 30s flick with no play_end was not a display view")
	}
	if math.Abs(done.ViewScore-1.0/3.0) > 0.01 {
		t.Fatalf("view_score=%.3f want 0.333 (a third of the flick covered)", done.ViewScore)
	}
	if math.Abs(done.PercentViewed-33.33) > 0.1 {
		t.Fatalf("percent_viewed=%.2f want 33.33", done.PercentViewed)
	}
	if r.sessionCount(t) != 1 {
		t.Fatalf("sessions=%d want 1", r.sessionCount(t))
	}

	// No play_end row exists anywhere: the view rests on the session.
	var playEnds int
	if err := r.pool.QueryRow(r.ctx, `SELECT count(*) FROM analytics.events_raw WHERE type = 'play_end'`).Scan(&playEnds); err != nil {
		t.Fatal(err)
	}
	if playEnds != 0 {
		t.Fatalf("play_end rows=%d want 0", playEnds)
	}
}

// A play_end that arrives after the finaliser closed the session is a
// correction to the same row, never a second view: the primary key
// guarantees one row per (viewer, session, content), and the
// aggregators recompute whole buckets from rows.
func TestLatePlayEndDoesNotCreateSecondView(t *testing.T) {
	r := newSessionRig(t)
	session := uuid.New()
	start := r.now.Add(-20 * time.Minute)

	r.ingest(t, r.playStartAndHeartbeats(session, start))
	if closed, err := finalizerAt(r.store, r.now).RunOnce(r.ctx); err != nil || closed != 1 {
		t.Fatalf("finalise: closed=%d err=%v, want 1", closed, err)
	}
	byInactivity := r.session(t, session)
	if byInactivity.FinalizeReason == nil || *byInactivity.FinalizeReason != pgstore.FinalizeInactivity {
		t.Fatalf("setup: not closed by inactivity: %+v", byInactivity)
	}

	// The client's queue finally flushes: the play_end says 12s watched,
	// more than the last heartbeat knew about.
	late := r.event(model.EventPlayEnd, session, start.Add(12*time.Second), map[string]any{
		"content_duration_ms": rigDurationMS, "watched_ms_total": 12_000,
		"max_continuous_watch_ms": 12_000, "loop_count": 0, "end_reason": "swipe_next",
	})
	if result := r.ingest(t, []EventDTO{late}); result.Accepted != 1 {
		t.Fatalf("late play_end accepted=%d want 1", result.Accepted)
	}

	if n := r.sessionCount(t); n != 1 {
		t.Fatalf("sessions=%d want 1: the late play_end made a second view", n)
	}
	corrected := r.session(t, session)
	if corrected.FinalizeReason == nil || *corrected.FinalizeReason != pgstore.FinalizePlayEnd {
		t.Fatalf("finalize_reason=%v want play_end after the correction", corrected.FinalizeReason)
	}
	if corrected.WatchedMS != 12_000 {
		t.Fatalf("watched_ms=%d want 12000 (GREATEST of heartbeats and play_end)", corrected.WatchedMS)
	}
	if corrected.EndReason == nil || *corrected.EndReason != "swipe_next" {
		t.Fatalf("end_reason=%v want swipe_next", corrected.EndReason)
	}
	if !corrected.IsDisplayView || math.Abs(corrected.PercentViewed-40) > 0.01 {
		t.Fatalf("flags after correction: display=%v percent_viewed=%.2f", corrected.IsDisplayView, corrected.PercentViewed)
	}
	// The bitmap only ever saw ten seconds; the two seconds play_end
	// added are watch time, not coverage. The score is honest about it.
	if corrected.CoveredMS != 10_000 || math.Abs(corrected.ViewScore-1.0/3.0) > 0.01 {
		t.Fatalf("covered_ms=%d view_score=%.3f want 10000 / 0.333", corrected.CoveredMS, corrected.ViewScore)
	}

	// The same play_end again, and then a stale heartbeat replayed under
	// a fresh event id with a smaller total: duplicates and reorders
	// change nothing.
	if result := r.ingest(t, []EventDTO{late}); result.Duplicate != 1 || result.Accepted != 0 {
		t.Fatalf("replayed play_end result=%+v", result)
	}
	stale := r.event(model.EventWatchHeartbeat, session, start.Add(4*time.Second), map[string]any{
		"watched_ms_increment": 2_000, "watched_ms_total": 4_000, "playhead_position_ms": 4_000,
	})
	if result := r.ingest(t, []EventDTO{stale}); result.Accepted != 1 {
		t.Fatalf("stale heartbeat accepted=%d want 1 (fresh event id)", result.Accepted)
	}
	after := r.session(t, session)
	if after.WatchedMS != 12_000 || after.FinalizeReason == nil || *after.FinalizeReason != pgstore.FinalizePlayEnd || r.sessionCount(t) != 1 {
		t.Fatalf("a replayed heartbeat moved the session backwards: %+v", after)
	}
}

// A viewer who reopens the same video gets a new client session; the
// old one, if still open, is closed as superseded so one viewer holds
// one open session per content at a time.
func TestNewPlayStartSupersedesOpenSession(t *testing.T) {
	r := newSessionRig(t)
	first, second := uuid.New(), uuid.New()
	start := r.now.Add(-2 * time.Minute)

	r.ingest(t, r.playStartAndHeartbeats(first, start))
	r.ingest(t, r.playStartAndHeartbeats(second, start.Add(time.Minute)))

	old := r.session(t, first)
	if old.FinalizedAt == nil || old.FinalizeReason == nil || *old.FinalizeReason != pgstore.FinalizeSuperseded {
		t.Fatalf("first session not superseded: %+v", old)
	}
	if !old.IsDisplayView {
		t.Fatal("the superseded session had ten seconds of watch time and is still a view")
	}
	current := r.session(t, second)
	if current.FinalizedAt != nil {
		t.Fatalf("the new session was closed: %+v", current)
	}
	if r.sessionCount(t) != 2 {
		t.Fatalf("sessions=%d want 2", r.sessionCount(t))
	}
}

// The clamp end to end: a 2s flick looped for minutes lands as one
// session with 20 loops and 42s of credited watch time, not a rejected
// batch.
func TestLoopedSessionIsClampedOnTheWire(t *testing.T) {
	r := newSessionRig(t)
	short := uuid.New()
	if err := r.store.UpsertContentOwnership(r.ctx, pgstore.ContentOwnership{
		ContentID: short, CreatorID: r.creator, ContentType: model.ContentTypeFlick, CreatedAt: r.now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	r.content = short
	session := uuid.New()
	end := r.event(model.EventPlayEnd, session, r.now, map[string]any{
		"content_duration_ms": 2_000, "watched_ms_total": 200_000,
		"max_continuous_watch_ms": 1_500, "loop_count": 60, "end_reason": "swipe_next",
	})
	if result := r.ingest(t, []EventDTO{end}); result.Accepted != 1 {
		t.Fatalf("looped play_end accepted=%d want 1", result.Accepted)
	}
	row := r.session(t, session)
	if row.LoopCount != 20 || row.WatchedMS != 42_000 || row.WatchedMSReported != 200_000 {
		t.Fatalf("clamp: loops=%d watched=%d reported=%d", row.LoopCount, row.WatchedMS, row.WatchedMSReported)
	}
	if !row.IsDisplayView || row.FinalizeReason == nil || *row.FinalizeReason != pgstore.FinalizePlayEnd {
		t.Fatalf("looped session flags: %+v", row)
	}
}

// The stamp lands on the session row, where the aggregators read it.
func TestSelfViewIsStampedOnTheSessionRow(t *testing.T) {
	r := newSessionRig(t)
	r.actor = r.creator // the creator watching their own upload
	session := uuid.New()
	r.ingest(t, r.playStartAndHeartbeats(session, r.now.Add(-30*time.Second)))
	row := r.session(t, session)
	if !row.IsSelfView {
		t.Fatal("creator's own session was not stamped is_self_view")
	}
}
