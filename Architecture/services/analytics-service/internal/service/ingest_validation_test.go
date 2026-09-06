package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/atpost/analytics-service/internal/model"
	"github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/google/uuid"
)

var (
	testContent = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	testCreator = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	testSession = uuid.MustParse("33333333-3333-4333-8333-333333333333")
)

func testOwnership(contentType string) postgres.ContentOwnership {
	return postgres.ContentOwnership{
		ContentID:   testContent,
		CreatorID:   testCreator,
		ContentType: contentType,
		CreatedAt:   time.Now().Add(-time.Hour),
	}
}

func decode(t *testing.T, payload map[string]any) *clientEvent {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var ev clientEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatal(err)
	}
	return &ev
}

// validPayloads is a realistic, minimal-but-complete payload for each of
// the thirteen event types the model defines.
func validPayloads() map[string]map[string]any {
	base := func(extra map[string]any) map[string]any {
		p := map[string]any{
			"content_id": testContent.String(),
			"session_id": testSession.String(),
			"surface":    "posttube",
			"position":   3,
		}
		for k, v := range extra {
			p[k] = v
		}
		return p
	}
	return map[string]map[string]any{
		model.EventImpression: base(map[string]any{"visible_ms": 620}),
		model.EventPlayStart:  base(map[string]any{"content_duration_ms": 180_000, "start_method": "tap", "time_to_first_frame_ms": 420, "initial_buffer_ms": 130}),
		model.EventWatchHeartbeat: base(map[string]any{
			"watched_ms_increment": 5000, "watched_ms_total": 25_000,
			"playhead_position_ms": 25_000, "buffering_ms_increment": 0,
			"seek_count_increment": 0, "playback_speed": 1.0,
		}),
		model.EventMilestone: base(map[string]any{"milestone_type": "PCT_50", "watched_ms": 90_000}),
		model.EventPlayEnd: base(map[string]any{
			"content_duration_ms": 180_000, "watched_ms_total": 150_000,
			"max_continuous_watch_ms": 120_000, "loop_count": 0, "end_reason": "ended",
		}),
		model.EventLike:              base(nil),
		model.EventCommentCreate:     base(nil),
		model.EventShare:             base(nil),
		model.EventSave:              base(nil),
		model.EventFollowFromContent: base(nil),
		model.EventNotInterested:     base(map[string]any{"reason": "repetitive"}),
		model.EventReport:            base(map[string]any{"reason": "spam"}),
		model.EventBlockCreator:      base(map[string]any{"reason": "dislike_creator"}),
	}
}

// Every event type the model declares must be accepted, validated and
// turned into a persistable row. A type in VideoEventNames that the
// ingest path rejects is a silently dropped signal.
func TestEveryDeclaredVideoEventTypeIsAcceptedAndNormalized(t *testing.T) {
	payloads := validPayloads()
	if len(payloads) != len(model.VideoEventNames) {
		t.Fatalf("test covers %d types but the model declares %d", len(payloads), len(model.VideoEventNames))
	}

	for eventType := range model.VideoEventNames {
		payload, ok := payloads[eventType]
		if !ok {
			t.Fatalf("no payload fixture for declared event type %q", eventType)
		}
		t.Run(eventType, func(t *testing.T) {
			norm, err := normalizeEvent(eventType, decode(t, payload), testOwnership("long_video"))
			if err != nil {
				t.Fatalf("rejected valid %s: %v", eventType, err)
			}
			if norm.Type != eventType {
				t.Fatalf("type=%s want=%s", norm.Type, eventType)
			}
			if norm.ContentID != testContent {
				t.Fatalf("content_id=%s want=%s", norm.ContentID, testContent)
			}
			if norm.Attributes["creator_id"] != testCreator.String() {
				t.Fatalf("creator attribution=%v", norm.Attributes["creator_id"])
			}
			if norm.Attributes["content_type"] != "long_video" {
				t.Fatalf("content_type=%v", norm.Attributes["content_type"])
			}
			if norm.Attributes["surface"] != "posttube" {
				t.Fatalf("surface=%v", norm.Attributes["surface"])
			}
			if _, leaked := norm.Attributes["viewer_id"]; leaked {
				t.Fatalf("viewer_id leaked into the persisted payload")
			}
		})
	}
}

// Attribution is rebuilt from the ownership projection, never taken from
// the client, for every type — not just play_end.
func TestClientClaimedAttributionIsDiscardedForEveryType(t *testing.T) {
	forgedCreator := uuid.New().String()
	forgedViewer := uuid.New().String()
	for eventType, payload := range validPayloads() {
		p := map[string]any{}
		for k, v := range payload {
			p[k] = v
		}
		p["creator_id"] = forgedCreator
		p["viewer_id"] = forgedViewer
		p["content_type"] = "reel" // client claim, must be ignored

		norm, err := normalizeEvent(eventType, decode(t, p), testOwnership("long_video"))
		if err != nil {
			t.Fatalf("%s: %v", eventType, err)
		}
		if norm.Attributes["creator_id"] == forgedCreator {
			t.Fatalf("%s: forged creator survived", eventType)
		}
		if norm.Attributes["content_type"] != "long_video" {
			t.Fatalf("%s: client content_type overrode the projection", eventType)
		}
		if _, leaked := norm.Attributes["viewer_id"]; leaked {
			t.Fatalf("%s: forged viewer survived", eventType)
		}
	}
}

// Repetition is a signal for some types and an artefact for others.
// Heartbeats and comments repeat legitimately; likes and milestone
// crossings do not, and must collapse so they cannot inflate the
// engagement rates the quality score is built from.
func TestDedupeKeyCollapsesOnlyTheRepetitionsThatAreArtefacts(t *testing.T) {
	payloads := validPayloads()

	repeatable := []string{model.EventImpression, model.EventWatchHeartbeat, model.EventCommentCreate, model.EventPlayStart}
	for _, eventType := range repeatable {
		norm, err := normalizeEvent(eventType, decode(t, payloads[eventType]), testOwnership("reel"))
		if err != nil {
			t.Fatal(err)
		}
		if norm.DedupeKey != nil {
			t.Fatalf("%s carries dedupe_key %q; repeated ones would be lost", eventType, *norm.DedupeKey)
		}
	}

	oncePer := []string{model.EventLike, model.EventShare, model.EventSave,
		model.EventFollowFromContent, model.EventNotInterested,
		model.EventReport, model.EventBlockCreator}
	for _, eventType := range oncePer {
		norm, err := normalizeEvent(eventType, decode(t, payloads[eventType]), testOwnership("reel"))
		if err != nil {
			t.Fatal(err)
		}
		if norm.DedupeKey == nil {
			t.Fatalf("%s has no dedupe_key; a double-tap would double-count", eventType)
		}
	}

	// A milestone dedupes per threshold, so PCT_25 and PCT_50 in the
	// same session are two rows but PCT_50 twice is one.
	first, _ := normalizeEvent(model.EventMilestone, decode(t, payloads[model.EventMilestone]), testOwnership("reel"))
	other := map[string]any{}
	for k, v := range payloads[model.EventMilestone] {
		other[k] = v
	}
	other["milestone_type"] = "PCT_25"
	second, _ := normalizeEvent(model.EventMilestone, decode(t, other), testOwnership("reel"))
	if first.DedupeKey == nil || second.DedupeKey == nil {
		t.Fatal("milestones must carry a dedupe key")
	}
	if *first.DedupeKey == *second.DedupeKey {
		t.Fatalf("PCT_50 and PCT_25 share dedupe key %q", *first.DedupeKey)
	}
}

func TestMilestonesMapToTheirViewBuckets(t *testing.T) {
	for milestone, bucket := range model.MilestoneToViewBucket {
		norm, err := normalizeEvent(model.EventMilestone, decode(t, map[string]any{
			"content_id": testContent.String(), "session_id": testSession.String(),
			"milestone_type": milestone, "watched_ms": 12_000,
		}), testOwnership("long_video"))
		if err != nil {
			t.Fatalf("%s: %v", milestone, err)
		}
		if norm.Attributes["view_bucket"] != bucket {
			t.Fatalf("%s mapped to %v, want %s", milestone, norm.Attributes["view_bucket"], bucket)
		}
	}

	// Percent milestones are valid but are not view-duration buckets.
	norm, err := normalizeEvent(model.EventMilestone, decode(t, map[string]any{
		"content_id": testContent.String(), "session_id": testSession.String(),
		"milestone_type": "PCT_75", "watched_ms": 12_000,
	}), testOwnership("long_video"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := norm.Attributes["view_bucket"]; ok {
		t.Fatal("PCT_75 was mapped to a view-duration bucket")
	}
}

func TestPlayEndDerivesPercentViewedAndDisplayViewFromTheProjection(t *testing.T) {
	// 45s of a 60s reel: 75%, comfortably a display view.
	norm, err := normalizeEvent(model.EventPlayEnd, decode(t, map[string]any{
		"content_id": testContent.String(), "session_id": testSession.String(),
		"content_duration_ms": 60_000, "watched_ms_total": 45_000,
		"loop_count": 0, "end_reason": "ended",
	}), testOwnership("reel"))
	if err != nil {
		t.Fatal(err)
	}
	if pct := norm.Attributes["percent_viewed"].(float64); pct != 75 {
		t.Fatalf("percent_viewed=%v want 75", pct)
	}
	if norm.Attributes["is_display_view"] != true {
		t.Fatal("45s of a 60s reel was not a display view")
	}

	// 2s of a 10-minute long video: not a display view.
	norm, err = normalizeEvent(model.EventPlayEnd, decode(t, map[string]any{
		"content_id": testContent.String(), "session_id": testSession.String(),
		"content_duration_ms": 600_000, "watched_ms_total": 2_000,
		"loop_count": 0, "end_reason": "swipe_next",
	}), testOwnership("long_video"))
	if err != nil {
		t.Fatal(err)
	}
	if norm.Attributes["is_display_view"] != false {
		t.Fatal("2s of a 10-minute video counted as a display view")
	}

	// Looping past 100% is clamped, not stored as 340%.
	norm, err = normalizeEvent(model.EventPlayEnd, decode(t, map[string]any{
		"content_id": testContent.String(), "session_id": testSession.String(),
		"content_duration_ms": 5_000, "watched_ms_total": 17_000,
		"loop_count": 3, "end_reason": "swipe_next",
	}), testOwnership("reel"))
	if err != nil {
		t.Fatal(err)
	}
	if pct := norm.Attributes["percent_viewed"].(float64); pct != 100 {
		t.Fatalf("looped percent_viewed=%v want clamped to 100", pct)
	}
}

func TestIngestRejectsMalformedAndAbusivePayloads(t *testing.T) {
	cases := []struct {
		name      string
		eventType string
		payload   map[string]any
	}{
		{"heartbeat claiming more than it has watched", model.EventWatchHeartbeat, map[string]any{
			"content_id": testContent.String(), "session_id": testSession.String(),
			"watched_ms_increment": 60_000, "watched_ms_total": 5_000,
		}},
		{"heartbeat with an impossible increment", model.EventWatchHeartbeat, map[string]any{
			"content_id": testContent.String(), "session_id": testSession.String(),
			"watched_ms_increment": 3_600_000, "watched_ms_total": 3_600_000,
		}},
		{"heartbeat at an impossible speed", model.EventWatchHeartbeat, map[string]any{
			"content_id": testContent.String(), "session_id": testSession.String(),
			"watched_ms_increment": 1000, "watched_ms_total": 1000, "playback_speed": 64,
		}},
		{"unknown milestone", model.EventMilestone, map[string]any{
			"content_id": testContent.String(), "session_id": testSession.String(),
			"milestone_type": "PCT_9000",
		}},
		{"play_start with no duration", model.EventPlayStart, map[string]any{
			"content_id": testContent.String(), "session_id": testSession.String(),
		}},
		{"play_end watching ten times the video", model.EventPlayEnd, map[string]any{
			"content_id": testContent.String(), "session_id": testSession.String(),
			"content_duration_ms": 1000, "watched_ms_total": 900_000,
		}},
		{"play_end with an absurd loop count", model.EventPlayEnd, map[string]any{
			"content_id": testContent.String(), "session_id": testSession.String(),
			"content_duration_ms": 10_000, "watched_ms_total": 9_000, "loop_count": 999,
		}},
		{"impression visible for an hour", model.EventImpression, map[string]any{
			"content_id": testContent.String(), "session_id": testSession.String(),
			"visible_ms": 3_600_000,
		}},
		{"playback event with no session", model.EventPlayEnd, map[string]any{
			"content_id":          testContent.String(),
			"content_duration_ms": 10_000, "watched_ms_total": 9_000,
		}},
		{"unparseable session", model.EventLike, map[string]any{
			"content_id": testContent.String(), "session_id": "not-a-uuid",
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := normalizeEvent(tc.eventType, decode(t, tc.payload), testOwnership("reel")); err == nil {
				t.Fatalf("accepted %s", tc.name)
			}
		})
	}
}

// Engagement can happen on a card the viewer never played, so those
// types are allowed a nil session; playback events are not.
func TestEngagementWithoutASessionIsAccepted(t *testing.T) {
	for _, eventType := range []string{model.EventLike, model.EventShare, model.EventSave,
		model.EventCommentCreate, model.EventFollowFromContent, model.EventImpression} {
		norm, err := normalizeEvent(eventType, decode(t, map[string]any{
			"content_id": testContent.String(),
		}), testOwnership("reel"))
		if err != nil {
			t.Fatalf("%s without a session was rejected: %v", eventType, err)
		}
		if norm.SessionID != uuid.Nil {
			t.Fatalf("%s invented a session: %s", eventType, norm.SessionID)
		}
	}
}

// Free-text from a client must never be persisted verbatim.
func TestNegativeSignalReasonsAreClosedSet(t *testing.T) {
	norm, err := normalizeEvent(model.EventReport, decode(t, map[string]any{
		"content_id": testContent.String(),
		"reason":     "<script>alert(1)</script> plus a novel",
	}), testOwnership("reel"))
	if err != nil {
		t.Fatal(err)
	}
	if norm.Attributes["reason"] != "unspecified" {
		t.Fatalf("free text survived as %v", norm.Attributes["reason"])
	}
}
