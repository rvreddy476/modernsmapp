package service

import (
	"testing"
	"time"

	"github.com/atpost/analytics-service/internal/model"
	"github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/google/uuid"
)

// A heavily looped reel used to be dropped whole: watched_ms_total over
// ten times the duration, or more than twenty loops, rejected the event
// and with it the view (audit M-09). The most-watched content lost its
// most engaged sessions. Now the loop count is capped and the watch
// time clamped to what the loops can account for; only the twelve-hour
// ceiling still rejects.
func TestLoopedPlayEndIsClampedNotDropped(t *testing.T) {
	norm, err := normalizeEvent(testViewer, model.EventPlayEnd, decode(t, map[string]any{
		"content_id": testContent.String(), "session_id": testSession.String(),
		"content_duration_ms": 2_000, "watched_ms_total": 200_000,
		"max_continuous_watch_ms": 1_500, "loop_count": 60, "end_reason": "swipe_next",
	}), testOwnership("flick"))
	if err != nil {
		t.Fatalf("a 100x-looped 2s flick was rejected: %v", err)
	}
	if got := norm.Attributes["loop_count"]; got != 20 {
		t.Fatalf("loop_count=%v want capped to 20", got)
	}
	// 2s x (20 loops + 1) = 42s is the most those loops can account for.
	if got := norm.Attributes["watched_ms_total"]; got != int64(42_000) {
		t.Fatalf("watched_ms_total=%v want clamped to 42000", got)
	}
	if got := norm.Attributes["watched_ms_reported"]; got != int64(200_000) {
		t.Fatalf("watched_ms_reported=%v want the client's 200000 kept for audit", got)
	}
	if norm.Attributes["is_display_view"] != true {
		t.Fatal("a looped 2s flick must still be a display view")
	}
	if norm.Attributes["percent_viewed"] != float64(100) {
		t.Fatalf("percent_viewed=%v want 100", norm.Attributes["percent_viewed"])
	}

	// A total that needs no clamp is stored as reported, and the audit
	// column still says so.
	norm, err = normalizeEvent(testViewer, model.EventPlayEnd, decode(t, map[string]any{
		"content_id": testContent.String(), "session_id": testSession.String(),
		"content_duration_ms": 5_000, "watched_ms_total": 17_000,
		"loop_count": 3, "end_reason": "swipe_next",
	}), testOwnership("flick"))
	if err != nil {
		t.Fatal(err)
	}
	if norm.Attributes["watched_ms_total"] != int64(17_000) || norm.Attributes["watched_ms_reported"] != int64(17_000) {
		t.Fatalf("unclamped total changed: %v / %v", norm.Attributes["watched_ms_total"], norm.Attributes["watched_ms_reported"])
	}

	// The twelve-hour ceiling is the only rejection left.
	_, err = normalizeEvent(testViewer, model.EventPlayEnd, decode(t, map[string]any{
		"content_id": testContent.String(), "session_id": testSession.String(),
		"content_duration_ms": 2_000, "watched_ms_total": int64(13 * time.Hour / time.Millisecond),
		"loop_count": 5, "end_reason": "swipe_next",
	}), testOwnership("flick"))
	if err == nil {
		t.Fatal("thirteen hours of a 2s flick was accepted")
	}
	// And a negative loop count is still malformed, not clamped to zero.
	_, err = normalizeEvent(testViewer, model.EventPlayEnd, decode(t, map[string]any{
		"content_id": testContent.String(), "session_id": testSession.String(),
		"content_duration_ms": 2_000, "watched_ms_total": 1_000, "loop_count": -1,
	}), testOwnership("flick"))
	if err == nil {
		t.Fatal("negative loop_count was accepted")
	}
}

// The clamp that binds the play_end figure binds every heartbeat's
// running total too (M-26): watched <= duration x (loops + 1), the
// client's figure kept in watched_ms_reported. A heartbeat that carries
// a duration is clamped here; one that carries none (the web contract)
// passes through and meets the clamp against the session's snapshot in
// the upsert, which the playback contract fixtures pin.
func TestHeartbeatTotalIsClampedLikePlayEnd(t *testing.T) {
	norm, err := normalizeEvent(testViewer, model.EventWatchHeartbeat, decode(t, map[string]any{
		"content_id": testContent.String(), "session_id": testSession.String(),
		"content_duration_ms": 5_000, "watched_ms_total": 130_000, "watched_ms_increment": 5_000,
		"playhead_position_ms": 5_000, "loop_count": 25,
	}), testOwnership("flick"))
	if err != nil {
		t.Fatalf("a looped heartbeat carrying its duration was rejected: %v", err)
	}
	// 5s x (20 capped loops + 1) = 105s is the most those loops can account for.
	if got := norm.Attributes["watched_ms_total"]; got != int64(105_000) {
		t.Fatalf("watched_ms_total=%v want clamped to 105000", got)
	}
	if got := norm.Attributes["watched_ms_reported"]; got != int64(130_000) {
		t.Fatalf("watched_ms_reported=%v want the client's 130000 kept for audit", got)
	}
	if norm.Session.WatchedMS != 105_000 || norm.Session.WatchedMSReported != 130_000 {
		t.Fatalf("session update carries %d/%d, want 105000/130000", norm.Session.WatchedMS, norm.Session.WatchedMSReported)
	}
	if norm.Session.LoopCount != 20 || norm.Session.ContentDurationMS != 5_000 {
		t.Fatalf("session update loops=%d duration=%d, want 20 and 5000 so the row's GREATEST sees them", norm.Session.LoopCount, norm.Session.ContentDurationMS)
	}

	// No loop count on the heartbeat: one pass is the ceiling.
	norm, err = normalizeEvent(testViewer, model.EventWatchHeartbeat, decode(t, map[string]any{
		"content_id": testContent.String(), "session_id": testSession.String(),
		"content_duration_ms": 30_000, "watched_ms_total": 40_000, "watched_ms_increment": 5_000,
		"playhead_position_ms": 30_000,
	}), testOwnership("flick"))
	if err != nil {
		t.Fatal(err)
	}
	if norm.Attributes["watched_ms_total"] != int64(30_000) || norm.Attributes["watched_ms_reported"] != int64(40_000) {
		t.Fatalf("rewatch heartbeat: %v / %v, want 30000 / 40000", norm.Attributes["watched_ms_total"], norm.Attributes["watched_ms_reported"])
	}

	// No duration on the heartbeat (the web contract): nothing to clamp
	// against here, the total passes through unchanged and the session
	// upsert applies the clamp against the row's snapshotted duration.
	norm, err = normalizeEvent(testViewer, model.EventWatchHeartbeat, decode(t, map[string]any{
		"content_id": testContent.String(), "session_id": testSession.String(),
		"watched_ms_total": 130_000, "watched_ms_increment": 5_000, "playhead_position_ms": 5_000,
	}), testOwnership("flick"))
	if err != nil {
		t.Fatal(err)
	}
	if norm.Attributes["watched_ms_total"] != int64(130_000) || norm.Attributes["watched_ms_reported"] != int64(130_000) {
		t.Fatalf("duration-less heartbeat changed: %v / %v", norm.Attributes["watched_ms_total"], norm.Attributes["watched_ms_reported"])
	}
	if norm.Session.ContentDurationMS != 0 {
		t.Fatalf("a duration-less heartbeat must not invent a duration, got %d", norm.Session.ContentDurationMS)
	}

	// A heartbeat that does carry a duration is held to the same
	// validity as play_start, and a negative loop count is malformed.
	if _, err := normalizeEvent(testViewer, model.EventWatchHeartbeat, decode(t, map[string]any{
		"content_id": testContent.String(), "session_id": testSession.String(),
		"content_duration_ms": -5, "watched_ms_total": 1_000, "watched_ms_increment": 1_000,
	}), testOwnership("flick")); err == nil {
		t.Fatal("negative content_duration_ms on a heartbeat was accepted")
	}
	if _, err := normalizeEvent(testViewer, model.EventWatchHeartbeat, decode(t, map[string]any{
		"content_id": testContent.String(), "session_id": testSession.String(),
		"watched_ms_total": 1_000, "watched_ms_increment": 1_000, "loop_count": -1,
	}), testOwnership("flick")); err == nil {
		t.Fatal("negative loop_count on a heartbeat was accepted")
	}
}

// A creator watching their own upload is measured but never paid for
// it (audit M-06). The stamp is made here, from the gateway actor and
// the ownership projection — never from anything the client sent — and
// copied onto the session row; the aggregators exclude stamped rows
// from paid views. The display-view flag itself stays honest, so
// dashboards still see the creator's own plays.
func TestSelfViewIsNotADisplayView(t *testing.T) {
	payload := map[string]any{
		"content_id": testContent.String(), "session_id": testSession.String(),
		"content_duration_ms": 60_000, "watched_ms_total": 45_000,
		"loop_count": 0, "end_reason": "ended",
		// A client claiming to be someone else changes nothing.
		"viewer_id": uuid.New().String(),
	}

	self, err := normalizeEvent(testCreator, model.EventPlayEnd, decode(t, payload), testOwnership("flick"))
	if err != nil {
		t.Fatal(err)
	}
	if self.Attributes["is_self_view"] != true || !self.IsSelfView {
		t.Fatalf("creator's own play was not stamped is_self_view: attrs=%v norm=%v", self.Attributes["is_self_view"], self.IsSelfView)
	}
	if self.Session == nil || !self.Session.IsSelfView {
		t.Fatal("the session update did not copy is_self_view")
	}
	if self.Attributes["is_display_view"] != true {
		t.Fatal("the playback rule must still call it a display view; exclusion is the aggregator's job")
	}

	other, err := normalizeEvent(testViewer, model.EventPlayEnd, decode(t, payload), testOwnership("flick"))
	if err != nil {
		t.Fatal(err)
	}
	if other.Attributes["is_self_view"] != false || other.IsSelfView || other.Session.IsSelfView {
		t.Fatal("a stranger's play was stamped as a self-view")
	}

	// Every type is stamped, not just play_end: a creator liking their
	// own post is a self-signal too.
	like, err := normalizeEvent(testCreator, model.EventLike, decode(t, map[string]any{
		"content_id": testContent.String(),
	}), testOwnership("flick"))
	if err != nil {
		t.Fatal(err)
	}
	if like.Attributes["is_self_view"] != true {
		t.Fatal("creator's own like was not stamped is_self_view")
	}
}

// A like is state, not an event. Whatever session an old client wraps
// it in, it collapses on (actor, content) with the nil session and the
// 'content' key — the same key the Kafka engagement consumer writes, so
// the HTTP and Kafka copies of one like land on one receipt row.
func TestLikeIsNormalisedToNilSessionAndContentKey(t *testing.T) {
	like, err := normalizeEvent(testViewer, model.EventLike, decode(t, map[string]any{
		"content_id": testContent.String(), "session_id": testSession.String(),
	}), testOwnership("flick"))
	if err != nil {
		t.Fatal(err)
	}
	if like.SessionID != uuid.Nil {
		t.Fatalf("like kept session %s; it must dedupe regardless of session", like.SessionID)
	}
	if like.Attributes["session_id"] != uuid.Nil.String() {
		t.Fatalf("persisted session_id=%v want nil", like.Attributes["session_id"])
	}
	if like.DedupeKey == nil || *like.DedupeKey != postgres.LikeDedupeKey {
		t.Fatalf("like dedupe key=%v want %q", like.DedupeKey, postgres.LikeDedupeKey)
	}
	if like.Session != nil {
		t.Fatal("a like is not a playback event and must not touch the session")
	}

	// The other once-per-session types are unchanged: a share in a
	// second session is a second share.
	share, err := normalizeEvent(testViewer, model.EventShare, decode(t, map[string]any{
		"content_id": testContent.String(), "session_id": testSession.String(),
	}), testOwnership("flick"))
	if err != nil {
		t.Fatal(err)
	}
	if share.SessionID != testSession || share.DedupeKey == nil || *share.DedupeKey != "session" {
		t.Fatalf("share changed: session=%s key=%v", share.SessionID, share.DedupeKey)
	}
}
