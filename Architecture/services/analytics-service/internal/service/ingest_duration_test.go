package service

import (
	"testing"

	"github.com/atpost/analytics-service/internal/model"
)

// Audit M6: a play_end is judged against the duration post-service
// measured, never the client's content_duration_ms. These pin the exploit
// and each half of the guard.

// The exploit: a scripted client says a two-hour long video is 2s long
// and that it watched 2s. Against the client's figure that was a "short
// long-form, 50% watched" display view at 100%. Against the server's two
// hours it is 2s of a two-hour video: not a view, and next to no score.
func TestClaimedShortDurationDoesNotBuyAPaidView(t *testing.T) {
	const twoHours = int64(2 * 60 * 60 * 1000)
	norm, err := normalizeEvent(testViewer, model.EventPlayEnd, decode(t, map[string]any{
		"content_id": testContent.String(), "session_id": testSession.String(),
		"content_duration_ms": 2_000, "watched_ms_total": 2_000,
		"max_continuous_watch_ms": 2_000, "loop_count": 0, "end_reason": "ended",
	}), measuredOwnership("long_video", twoHours))
	if err != nil {
		t.Fatal(err)
	}
	if norm.Attributes["is_display_view"] != false {
		t.Fatal("2s of a two-hour video became a display view on the client's claimed duration")
	}
	pct := norm.Attributes["percent_viewed"].(float64)
	if want := float64(2_000) / float64(twoHours) * 100; pct != want {
		t.Fatalf("percent_viewed=%v want %v (2s of two hours)", pct, want)
	}
	if norm.Attributes["content_duration_ms"] != twoHours || norm.Attributes["content_duration_reported_ms"] != int64(2_000) {
		t.Fatalf("duration attrs: judged=%v reported=%v", norm.Attributes["content_duration_ms"], norm.Attributes["content_duration_reported_ms"])
	}
	if norm.Attributes["duration_verified"] != true {
		t.Fatal("a server-judged play_end must say so")
	}
	if norm.Session.ContentDurationMS != twoHours || norm.Session.PercentViewed != pct {
		t.Fatalf("session snapshot duration=%d percent=%v, want the server's", norm.Session.ContentDurationMS, norm.Session.PercentViewed)
	}
	// The Scylla projection (retention / demographics) carries the same.
	if ws := watchSessionFor(norm); ws.DurationMS != twoHours || ws.IsDisplayView || ws.VQS != pct/100 {
		t.Fatalf("watch projection %+v", ws)
	}
}

// The server duration also decides in the other direction: a 2s flick the
// client claims is ten minutes long, watched to the end, is still a view.
func TestServerDurationDecidesTheBarNotTheClaim(t *testing.T) {
	norm, err := normalizeEvent(testViewer, model.EventPlayEnd, decode(t, map[string]any{
		"content_id": testContent.String(), "session_id": testSession.String(),
		"content_duration_ms": 600_000, "watched_ms_total": 2_000,
		"loop_count": 0, "end_reason": "ended",
	}), measuredOwnership("flick", 2_000))
	if err != nil {
		t.Fatal(err)
	}
	if norm.Attributes["is_display_view"] != true || norm.Attributes["percent_viewed"] != float64(100) {
		t.Fatalf("a fully watched 2s flick: display=%v percent=%v", norm.Attributes["is_display_view"], norm.Attributes["percent_viewed"])
	}
}

// Watch time is clamped to the server duration plus the tolerance, not to
// whatever duration the client claims: a client saying a 60s video is two
// hours long and that it watched two hours is credited 61s.
func TestWatchTimeIsClampedToTheServerDuration(t *testing.T) {
	norm, err := normalizeEvent(testViewer, model.EventPlayEnd, decode(t, map[string]any{
		"content_id": testContent.String(), "session_id": testSession.String(),
		"content_duration_ms": 7_200_000, "watched_ms_total": 7_200_000,
		"max_continuous_watch_ms": 7_200_000, "loop_count": 0, "end_reason": "ended",
	}), measuredOwnership("long_video", 60_000))
	if err != nil {
		t.Fatal(err)
	}
	if got := norm.Attributes["watched_ms_total"]; got != int64(61_000) {
		t.Fatalf("watched_ms_total=%v want 61000 (60s + 1s tolerance)", got)
	}
	if got := norm.Attributes["max_continuous_watch_ms"]; got != int64(61_000) {
		t.Fatalf("max_continuous_watch_ms=%v want bounded by the clamped total", got)
	}
	if norm.Attributes["watched_ms_reported"] != int64(7_200_000) {
		t.Fatal("the client's figure must be kept for audit")
	}
	if norm.Attributes["percent_viewed"] != float64(100) || norm.Session.WatchedMS != 61_000 {
		t.Fatalf("percent=%v session watched=%d", norm.Attributes["percent_viewed"], norm.Session.WatchedMS)
	}
}

// With no server duration the view is counted, unqualified: no display
// view, no percent (so no score), no duration on the session. The watch
// time is still bounded by the client's own claim.
func TestUnknownServerDurationIsNeverAQualifiedView(t *testing.T) {
	before := counterValue(t, unverifiedDurationViews)
	norm, err := normalizeEvent(testViewer, model.EventPlayEnd, decode(t, map[string]any{
		"content_id": testContent.String(), "session_id": testSession.String(),
		"content_duration_ms": 60_000, "watched_ms_total": 90_000,
		"loop_count": 0, "end_reason": "ended",
	}), testOwnership("long_video"))
	if err != nil {
		t.Fatalf("an unknown duration must not reject the event: %v", err)
	}
	if norm.Attributes["is_display_view"] != false {
		t.Fatal("a view was qualified on the client's duration alone")
	}
	if norm.Attributes["percent_viewed"] != float64(0) || norm.Session.PercentViewed != 0 {
		t.Fatalf("percent_viewed=%v session=%v, want 0 with no server duration", norm.Attributes["percent_viewed"], norm.Session.PercentViewed)
	}
	if norm.Session.ContentDurationMS != 0 || norm.Attributes["content_duration_ms"] != int64(0) {
		t.Fatalf("the client's duration reached the session: %d / %v", norm.Session.ContentDurationMS, norm.Attributes["content_duration_ms"])
	}
	if norm.Attributes["duration_verified"] != false || norm.Attributes["content_duration_reported_ms"] != int64(60_000) {
		t.Fatalf("verified=%v reported=%v", norm.Attributes["duration_verified"], norm.Attributes["content_duration_reported_ms"])
	}
	if norm.Attributes["watched_ms_total"] != int64(60_000) {
		t.Fatalf("watched_ms_total=%v want bounded by the client's own 60s claim", norm.Attributes["watched_ms_total"])
	}
	if got := counterValue(t, unverifiedDurationViews) - before; got != 1 {
		t.Fatalf("unverified counter moved by %v, want 1", got)
	}
}

// play_start snapshots only the server's duration onto the session.
func TestPlayStartSnapshotsTheServerDuration(t *testing.T) {
	payload := map[string]any{
		"content_id": testContent.String(), "session_id": testSession.String(),
		"content_duration_ms": 2_000, "start_method": "tap",
	}
	norm, err := normalizeEvent(testViewer, model.EventPlayStart, decode(t, payload), measuredOwnership("long_video", 600_000))
	if err != nil {
		t.Fatal(err)
	}
	if norm.Session.ContentDurationMS != 600_000 || norm.Attributes["content_duration_ms"] != int64(600_000) {
		t.Fatalf("play_start snapshot=%d attr=%v want 600000", norm.Session.ContentDurationMS, norm.Attributes["content_duration_ms"])
	}
	norm, err = normalizeEvent(testViewer, model.EventPlayStart, decode(t, payload), testOwnership("long_video"))
	if err != nil {
		t.Fatal(err)
	}
	if norm.Session.ContentDurationMS != 0 {
		t.Fatalf("unknown server duration: snapshot=%d, want 0 (the claim never reaches the session)", norm.Session.ContentDurationMS)
	}
}

// A heartbeat is clamped against the server duration whether or not it
// carries one, and whatever one it carries.
func TestHeartbeatIsClampedToTheServerDuration(t *testing.T) {
	for name, extra := range map[string]map[string]any{
		"no client duration":    {},
		"inflated client claim": {"content_duration_ms": 7_200_000},
	} {
		t.Run(name, func(t *testing.T) {
			payload := map[string]any{
				"content_id": testContent.String(), "session_id": testSession.String(),
				"watched_ms_total": 130_000, "watched_ms_increment": 5_000, "playhead_position_ms": 5_000,
			}
			for k, v := range extra {
				payload[k] = v
			}
			norm, err := normalizeEvent(testViewer, model.EventWatchHeartbeat, decode(t, payload), measuredOwnership("flick", 5_000))
			if err != nil {
				t.Fatal(err)
			}
			if norm.Attributes["watched_ms_total"] != int64(5_250) || norm.Session.WatchedMS != 5_250 {
				t.Fatalf("watched=%v session=%d want 5250 (5s + 250ms)", norm.Attributes["watched_ms_total"], norm.Session.WatchedMS)
			}
			if norm.Session.ContentDurationMS != 5_000 {
				t.Fatalf("session duration=%d want the server's 5000", norm.Session.ContentDurationMS)
			}
		})
	}

	// Unknown server duration: the client's figure may bound its own
	// total, but never reaches the session.
	norm, err := normalizeEvent(testViewer, model.EventWatchHeartbeat, decode(t, map[string]any{
		"content_id": testContent.String(), "session_id": testSession.String(),
		"content_duration_ms": 5_000, "watched_ms_total": 130_000, "watched_ms_increment": 5_000,
		"playhead_position_ms": 5_000,
	}), testOwnership("flick"))
	if err != nil {
		t.Fatal(err)
	}
	if norm.Attributes["watched_ms_total"] != int64(5_000) {
		t.Fatalf("watched=%v want bounded by the client's own 5s", norm.Attributes["watched_ms_total"])
	}
	if norm.Session.ContentDurationMS != 0 {
		t.Fatalf("session duration=%d, want 0: an unverified claim must not reach the session", norm.Session.ContentDurationMS)
	}
}
