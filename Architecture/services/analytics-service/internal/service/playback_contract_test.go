//go:build integration

package service

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	pgstore "github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// The playback contract (plan Phase 5A, audit M-12)
// ---------------------------------------------------------------------------
//
// The fixtures under atpost-web/packages/analytics/fixtures/playback are
// the one contract both clients are measured against: for each viewer
// behaviour, the heartbeat stream a correct client emits and the session
// the server must produce from it. This test is the server side of that
// contract. It pushes each fixture's events through the real ingest
// service into the package's private database, ticks the session
// finaliser where the fixture says the server did, and asserts the
// fixture's expected block against the analytics.playback_sessions row.
//
// PLAYBACK_FIXTURES_DIR points at the fixture directory in the web repo;
// unset, the test skips. The README there documents the shape.

const playbackFixturesEnv = "PLAYBACK_FIXTURES_DIR"

// finalizerTickEvent is the one pseudo-event in a fixture: the server's
// SessionFinalizer ran at this wall time.
const finalizerTickEvent = "server_finalizer_tick"

type fixtureEvent struct {
	Type    string         `json:"type"`
	AtMS    int64          `json:"at_ms"`
	Payload map[string]any `json:"payload"`
}

type fixtureExpected struct {
	WatchedMS         int64    `json:"watched_ms"`
	WatchedMSReported *int64   `json:"watched_ms_reported"`
	CoveredMS         int64    `json:"covered_ms"`
	LoopCount         int      `json:"loop_count"`
	SeekCount         *int     `json:"seek_count"`
	PercentViewed     *float64 `json:"percent_viewed"`
	PercentCovered    *float64 `json:"percent_covered"`
	IsDisplayView     bool     `json:"is_display_view"`
	ViewScore         float64  `json:"view_score"`
	FinalizeReason    string   `json:"finalize_reason"`
	EndReason         *string  `json:"end_reason"`
	Sessions          *int     `json:"sessions"`
}

type playbackFixture struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     struct {
		ContentType string `json:"content_type"`
		DurationMS  int64  `json:"duration_ms"`
	} `json:"content"`
	Events   []fixtureEvent  `json:"events"`
	Expected fixtureExpected `json:"expected"`
}

// Tolerances. Percentages are stated to two decimals in the fixtures;
// the score to four.
const (
	percentTolerance = 0.05
	scoreTolerance   = 0.001
)

func loadPlaybackFixtures(t *testing.T) []playbackFixture {
	t.Helper()
	dir := os.Getenv(playbackFixturesEnv)
	if dir == "" {
		t.Skipf("%s is unset; point it at atpost-web/packages/analytics/fixtures/playback", playbackFixturesEnv)
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatalf("no *.json fixtures under %s", dir)
	}
	sort.Strings(paths)
	var out []playbackFixture
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var f playbackFixture
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if f.Name == "" || len(f.Events) == 0 || f.Content.DurationMS <= 0 || f.Content.ContentType == "" {
			t.Fatalf("%s: a fixture needs a name, a content block and events", p)
		}
		out = append(out, f)
	}
	return out
}

func TestPlaybackContractFixtures(t *testing.T) {
	fixtures := loadPlaybackFixtures(t)
	for _, f := range fixtures {
		f := f
		t.Run(f.Name, func(t *testing.T) {
			runPlaybackFixture(t, f)
		})
	}
}

func runPlaybackFixture(t *testing.T, f playbackFixture) {
	t.Helper()
	r := newSessionRig(t)
	// The rig owns a 30 s flick; the fixture names its own content.
	r.content = uuid.New()
	if err := r.store.UpsertContentOwnership(r.ctx, pgstore.ContentOwnership{
		ContentID: r.content, CreatorID: r.creator, ContentType: f.Content.ContentType,
		CreatedAt: r.now.Add(-2 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	session := uuid.New()
	// Far enough back that every at_ms stays inside the 24-hour ingest
	// window and never lands in the future.
	start := r.now.Add(-2 * time.Hour)

	for i, ev := range f.Events {
		at := start.Add(time.Duration(ev.AtMS) * time.Millisecond)
		if ev.Type == finalizerTickEvent {
			if _, err := finalizerAt(r.store, at).RunOnce(r.ctx); err != nil {
				t.Fatalf("event %d: finaliser tick at %s: %v", i, at, err)
			}
			continue
		}
		dto := r.event(ev.Type, session, at, ev.Payload)
		res, err := r.svc.IngestEvents(r.ctx, r.actor.String(), []EventDTO{dto})
		if err != nil {
			t.Fatalf("event %d (%s at %dms): ingest refused it: %v", i, ev.Type, ev.AtMS, err)
		}
		if res.Accepted != 1 {
			t.Fatalf("event %d (%s at %dms): accepted=%d duplicate=%d, want accepted 1", i, ev.Type, ev.AtMS, res.Accepted, res.Duplicate)
		}
	}

	row := r.session(t, session)
	want := f.Expected

	if row.WatchedMS != want.WatchedMS {
		t.Errorf("watched_ms = %d, want %d", row.WatchedMS, want.WatchedMS)
	}
	if want.WatchedMSReported != nil && row.WatchedMSReported != *want.WatchedMSReported {
		t.Errorf("watched_ms_reported = %d, want %d", row.WatchedMSReported, *want.WatchedMSReported)
	}
	if row.CoveredMS != want.CoveredMS {
		t.Errorf("covered_ms = %d, want %d", row.CoveredMS, want.CoveredMS)
	}
	if row.LoopCount != want.LoopCount {
		t.Errorf("loop_count = %d, want %d", row.LoopCount, want.LoopCount)
	}
	if want.SeekCount != nil && row.SeekCount != *want.SeekCount {
		t.Errorf("seek_count = %d, want %d", row.SeekCount, *want.SeekCount)
	}
	if want.PercentViewed != nil && math.Abs(row.PercentViewed-*want.PercentViewed) > percentTolerance {
		t.Errorf("percent_viewed = %.2f, want %.2f", row.PercentViewed, *want.PercentViewed)
	}
	if want.PercentCovered != nil && math.Abs(row.PercentCovered-*want.PercentCovered) > percentTolerance {
		t.Errorf("percent_covered = %.2f, want %.2f", row.PercentCovered, *want.PercentCovered)
	}
	if row.IsDisplayView != want.IsDisplayView {
		t.Errorf("is_display_view = %v, want %v", row.IsDisplayView, want.IsDisplayView)
	}
	if math.Abs(row.ViewScore-want.ViewScore) > scoreTolerance {
		t.Errorf("view_score = %.4f, want %.4f", row.ViewScore, want.ViewScore)
	}
	if want.FinalizeReason != "" {
		if row.FinalizeReason == nil {
			t.Errorf("finalize_reason = <nil>, want %q (the session was never closed)", want.FinalizeReason)
		} else if *row.FinalizeReason != want.FinalizeReason {
			t.Errorf("finalize_reason = %q, want %q", *row.FinalizeReason, want.FinalizeReason)
		}
	}
	switch {
	case want.EndReason == nil && row.EndReason != nil:
		t.Errorf("end_reason = %q, want none (no play_end in this fixture)", *row.EndReason)
	case want.EndReason != nil && (row.EndReason == nil || *row.EndReason != *want.EndReason):
		t.Errorf("end_reason = %v, want %q", row.EndReason, *want.EndReason)
	}
	if want.Sessions != nil {
		if n := r.sessionCount(t); n != *want.Sessions {
			t.Errorf("sessions for this viewer and content = %d, want %d", n, *want.Sessions)
		}
	}
	if t.Failed() {
		t.Logf("fixture: %s", f.Description)
		t.Logf("row: watched=%d reported=%d covered=%d loops=%d seeks=%d pv=%.2f pc=%.2f display=%v score=%.4f",
			row.WatchedMS, row.WatchedMSReported, row.CoveredMS, row.LoopCount, row.SeekCount,
			row.PercentViewed, row.PercentCovered, row.IsDisplayView, row.ViewScore)
	}
}
