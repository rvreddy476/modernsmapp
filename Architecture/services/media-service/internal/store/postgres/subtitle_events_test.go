package postgres

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	sharedevents "github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// MediaSubtitlesChanged (2026-09-27): the snapshot search-service sets
// has_subtitles from. The payload is pure and pinned here; the transactional
// write is pinned structurally below and end to end in
// subtitle_events_integration_test.go (a _test database).

func TestSubtitleStatePayload(t *testing.T) {
	id := uuid.New()

	b, err := SubtitleStatePayload(id, []string{"hi", "en"})
	if err != nil {
		t.Fatal(err)
	}
	var p sharedevents.MediaSubtitlesChangedPayload
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	if p.MediaID != id.String() || !p.HasPublishedSubtitles || strings.Join(p.Languages, ",") != "en,hi" {
		t.Fatalf("published tracks: got %+v", p)
	}

	none, err := SubtitleStatePayload(id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(none), `"languages":[]`) || !strings.Contains(string(none), `"has_published_subtitles":false`) {
		t.Fatalf("no published track must be an explicit false and [] (not null), got %s", none)
	}

	in := []string{"hi", "en"}
	_, _ = SubtitleStatePayload(id, in)
	if in[0] != "hi" {
		t.Fatal("the payload builder sorted the caller's slice in place")
	}
}

// Every write that can change which tracks are published must record the
// snapshot in the SAME transaction (the outbox rule of migration 013). A
// write that goes back to s.db directly would commit without its event and
// search would never hear about it — the exact gap this closes.
//
//	CreateSubtitle              manual upload / the auto-caption write
//	UpdateSubtitleContent       owner correction (may create a published track)
//	SetSubtitlePublished        the owner's publish toggle
//	MarkGeneratedSubtitleDraft  the caption job's draft mark
func TestSubtitleWritesRecordTheirStateEventInTheSameTransaction(t *testing.T) {
	for file, funcs := range map[string][]string{
		"clips.go":          {"CreateSubtitle"},
		"caption_jobs.go":   {"UpdateSubtitleContent"},
		"subtitles_mine.go": {"SetSubtitlePublished", "MarkGeneratedSubtitleDraft"},
	} {
		src := readSibling(t, file)
		for _, fn := range funcs {
			body := funcBody(t, src, "func (s *MediaAssetStore) "+fn+"(")
			if !strings.Contains(body, "s.withSubtitleStateEvent(ctx, ") {
				t.Errorf("%s no longer writes through withSubtitleStateEvent: its change would reach no search index", fn)
			}
			if strings.Contains(body, "s.db.Exec(") || strings.Contains(body, "s.db.QueryRow(") {
				t.Errorf("%s writes on s.db outside the event transaction", fn)
			}
		}
	}
}

// The snapshot must count PUBLISHED tracks only — a draft is its owner's
// alone, and must not light up the cc filter for everyone else.
func TestSubtitleStateCountsPublishedTracksOnly(t *testing.T) {
	body := funcBody(t, readSibling(t, "subtitle_events.go"), "func enqueueSubtitleStateTx(")
	if !strings.Contains(body, "WHERE media_asset_id = $1 AND published") {
		t.Fatal("the snapshot must read published tracks only")
	}
	// A replaced snapshot gets a NEW event id and is re-armed; re-arming the
	// old id would let a relay that already read it mark the new state
	// published without ever sending it.
	for _, want := range []string{"uuid.NewString()", "event_id      = EXCLUDED.event_id", "published_at  = NULL", "clock_timestamp()"} {
		if !strings.Contains(body, want) {
			t.Errorf("outbox upsert lost %q", want)
		}
	}
	lock := funcBody(t, readSibling(t, "subtitle_events.go"), "func (s *MediaAssetStore) withSubtitleStateEvent(")
	if !strings.Contains(lock, "FOR NO KEY UPDATE") {
		t.Error("the asset row must be locked before the write so snapshots for one asset are serialised")
	}
}

func readSibling(t *testing.T, name string) string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(here), name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

// funcBody returns the source from the signature to the closing brace at
// column 0.
func funcBody(t *testing.T, src, signature string) string {
	t.Helper()
	start := strings.Index(src, signature)
	if start < 0 {
		t.Fatalf("could not find %q", signature)
	}
	end := strings.Index(src[start:], "\n}\n")
	if end < 0 {
		t.Fatalf("could not find the end of %q", signature)
	}
	return src[start : start+end]
}
