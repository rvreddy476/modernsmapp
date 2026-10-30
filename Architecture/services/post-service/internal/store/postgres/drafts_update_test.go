package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// LOW 11 (2026-10-30): an edit revives a parked reel draft, as
// MarkReelDraftBlocked promises; the backoff restarts only when the draft
// is scheduled again. The behaviour against a database is in
// drafts_queue_integration_test.go (CI).

const (
	reviveStatus   = "status = CASE WHEN status = 'blocked' THEN 'draft' ELSE status END"
	reviveReason   = "blocked_reason = CASE WHEN status = 'blocked' THEN NULL ELSE blocked_reason END"
	reviveSchedule = "schedule_at = CASE WHEN status = 'blocked' AND schedule_at <= NOW() THEN NULL ELSE schedule_at END"
	resetBackoff   = "publish_attempts = 0, next_attempt_at = NULL"
)

func TestReelDraftUpdateSQLRevivesABlockedDraft(t *testing.T) {
	id, author := uuid.New(), uuid.New()
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	q, args := reelDraftUpdateSQL(id, author, map[string]interface{}{"caption": "fixed", "title": "t"}, now)
	for _, want := range []string{reviveStatus, reviveReason, reviveSchedule} {
		if !strings.Contains(q, want) {
			t.Errorf("an edit does not revive a blocked draft: missing %q in\n%s", want, q)
		}
	}
	if strings.Contains(q, resetBackoff) {
		t.Errorf("an edit that does not schedule must not reset the backoff:\n%s", q)
	}
	want := "UPDATE reel_drafts SET caption = $1, title = $2, updated_at = $3, "
	if !strings.HasPrefix(q, want) || !strings.HasSuffix(q, " WHERE id = $4 AND author_id = $5") {
		t.Fatalf("statement:\n%s", q)
	}
	if len(args) != 5 || args[0] != "fixed" || args[1] != "t" || args[2] != now || args[3] != id || args[4] != author {
		t.Fatalf("args = %v", args)
	}
}

func TestReelDraftUpdateSQLScheduling(t *testing.T) {
	at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	q, _ := reelDraftUpdateSQL(uuid.New(), uuid.New(), map[string]interface{}{"schedule_at": at}, time.Now())
	if !strings.Contains(q, resetBackoff) {
		t.Errorf("scheduling must restart the backoff:\n%s", q)
	}
	if !strings.Contains(q, reviveStatus) || strings.Contains(q, reviveSchedule) {
		t.Errorf("scheduling revives the draft and keeps the new time:\n%s", q)
	}

	// PublishDraft's reschedule names the status: no CASE on it.
	q, _ = reelDraftUpdateSQL(uuid.New(), uuid.New(), map[string]interface{}{"schedule_at": at, "status": "draft"}, time.Now())
	if strings.Contains(q, reviveStatus) || strings.Contains(q, reviveReason) || strings.Count(q, "status =") != 1 {
		t.Errorf("an explicit status is written once, as given:\n%s", q)
	}
	if !strings.Contains(q, resetBackoff) {
		t.Errorf("rescheduling must restart the backoff:\n%s", q)
	}
}
