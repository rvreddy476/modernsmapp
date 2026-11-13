package scylla

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

// Echo notes_timeline maintenance (2026-11-13): the repair pass covers the
// notes table, and an author purge deletes exactly that author's rows.

func notesRow(owner, author uuid.UUID, created time.Time) TimelineRow {
	post := uuid.New()
	pid := gocql.UUID(post)
	return TimelineRow{
		Kind: NotesTimelineKind, Owner: gocql.UUID(owner), Bucket: bucket(created),
		TS: TimelineTS(post, created), PostID: &pid, AuthorID: gocql.UUID(author),
		CreatedAt: created.Truncate(time.Millisecond), ContentType: "note", HasTTL: true,
	}
}

func TestPlanNotesAuthorPurgeDeletesOnlyThatAuthor(t *testing.T) {
	owner, gone, kept := uuid.New(), uuid.New(), uuid.New()
	created := maintNow.Add(-24 * time.Hour)
	a1, a2 := notesRow(owner, gone, created), notesRow(owner, gone, created.Add(time.Minute))
	b := notesRow(owner, kept, created)
	ghost := notesRow(owner, gone, created)
	ghost.PostID = nil
	home := notesRow(owner, gone, created)
	home.Kind = "home"

	plan := PlanNotesAuthorPurge([]TimelineRow{a1, b, ghost, a2, home}, gocql.UUID(gone))
	if len(plan) != 2 {
		t.Fatalf("plan has %d actions, want 2: %+v", len(plan), plan)
	}
	for i, want := range []TimelineRow{a1, a2} {
		if plan[i].Op != OpDelete || plan[i].Why != WhyAuthor || plan[i].Row.TS != want.TS {
			t.Fatalf("action %d = %+v, want delete/author of %s", i, plan[i], want.TS)
		}
	}
}

// The ordinary repair plan works on notes rows as on home rows: a
// duplicate collapses onto the canonical key.
func TestPlanPartitionRepairsNotesRows(t *testing.T) {
	owner, author := uuid.New(), uuid.New()
	created := maintNow.Add(-48 * time.Hour)
	r := notesRow(owner, author, created)
	dup := r
	dup.TS = gocql.UUIDFromTime(created)
	plan := PlanPartition([]TimelineRow{r, dup}, maintNow)
	if got := countOps(plan); got["index/index"] != 1 || got["delete/duplicate"] != 1 || len(plan) != 2 {
		t.Fatalf("plan = %v", got)
	}
	if plan[1].Row.Kind != NotesTimelineKind {
		t.Fatalf("the delete lost the notes kind: %+v", plan[1].Row)
	}
}

func TestMaintenanceScanAndTableCoverNotes(t *testing.T) {
	stmt, ok := maintenanceScan(NotesTimelineKind)
	if !ok || !strings.Contains(stmt, "FROM notes_timeline") || !strings.Contains(stmt, "token(user_id, bucket)") || !strings.Contains(stmt, "author_id") {
		t.Fatalf("notes scan: %v %q", ok, stmt)
	}
	if _, ok := maintenanceScan("reels"); ok {
		t.Fatal("an unknown kind has a scan")
	}
	for kind, want := range map[string][2]string{
		NotesTimelineKind: {"notes_timeline", "user_id"},
		"home":            {"home_timeline_by_user", "user_id"},
		"author":          {"author_timeline_by_author", "author_id"},
	} {
		if table, col := maintenanceTable(kind); table != want[0] || col != want[1] {
			t.Errorf("%s: %s.%s, want %s.%s", kind, table, col, want[0], want[1])
		}
	}
}

// An author purge only ever runs over notes_timeline: on home or author it
// would delete rows by a rule written for notes. Refused before any read.
func TestAuthorPurgeRefusesOtherTables(t *testing.T) {
	m := NewMaintainer(nil, true).PurgeNotesAuthor(uuid.New())
	for _, kind := range []string{"home", "author"} {
		if _, err := m.Range(context.Background(), kind, TokenRange{}); err == nil {
			t.Errorf("author purge ran over %s", kind)
		}
	}
	if got := m.planFor(NotesTimelineKind, []TimelineRow{notesRow(uuid.New(), uuid.New(), maintNow)}); len(got) != 0 {
		t.Fatalf("author purge planned %d actions for someone else's row", len(got))
	}
}
