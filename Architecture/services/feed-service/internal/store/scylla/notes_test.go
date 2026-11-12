package scylla

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// notes.go follows timelines.go's rule: every statement addresses rows by
// primary key only (see timelines_primary_key_test.go for the 2026-09-09
// defect that rule exists for). notes_timeline is keyed like the home
// timeline; its DDL lives in database/scylla/notes_timeline.cql until the
// lead copies it into Architecture/docker/scylla.

var notesPrimaryKeys = map[string]map[string]bool{
	"notes_timeline":            {"user_id": true, "bucket": true, "ts": true},
	"timeline_index_by_post_v2": primaryKeys["timeline_index_by_post_v2"],
	"user_post_interactions":    primaryKeys["user_post_interactions"],
}

func TestNotesStatementsOnlyMatchOnPrimaryKey(t *testing.T) {
	stmts := parseStatements(t, "notes.go")
	checked := 0
	for _, s := range stmts {
		keys, known := notesPrimaryKeys[s.table]
		if !known {
			t.Errorf("notes.go touches table %s, which this test does not carry a key for", s.table)
			continue
		}
		checked++
		for _, col := range s.where {
			if !keys[col] {
				t.Errorf("%s on %s matches on %q, not part of its primary key; Scylla refuses it.\n  %s",
					s.verb, s.table, col, s.raw)
			}
		}
	}
	if checked < 7 {
		t.Fatalf("checked only %d statements in notes.go — has the CQL moved?", checked)
	}
}

func TestNotesUnfollowPurgeDeletesByClusteringKeyAndRetiresIndex(t *testing.T) {
	stmts := parseStatementsInFunc(t, "notes.go", "DeleteNotesTimelineEntriesByAuthorForUser")
	var del, idx int
	for _, s := range stmts {
		if s.verb != "DELETE" {
			continue
		}
		switch s.table {
		case "notes_timeline":
			del++
			if got := strings.Join(s.where, ","); got != "user_id,bucket,ts" {
				t.Fatalf("notes purge deletes on (%s), want exactly (user_id,bucket,ts)", got)
			}
		case "timeline_index_by_post_v2":
			idx++
		}
	}
	if del != 1 || idx != 1 {
		t.Fatalf("notes purge: %d timeline DELETE(s), %d index DELETE(s); want 1 and 1", del, idx)
	}
}

// The delete path must know the notes kind, or a deleted note stays in
// every follower's Following timeline.
func TestDeletePostFromTimelinesCoversNotes(t *testing.T) {
	stmts := parseStatementsInFunc(t, "timelines.go", "DeletePostFromTimelines")
	for _, s := range stmts {
		if s.verb == "DELETE" && s.table == "notes_timeline" {
			if got := strings.Join(s.where, ","); got != "user_id,bucket,ts" {
				t.Fatalf("delete of a note row matches on (%s)", got)
			}
			return
		}
	}
	t.Fatal("DeletePostFromTimelines has no notes_timeline DELETE: deleted notes would stay in Following")
}

func TestNotesTimelineDDLMatchesThePinnedKey(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "database", "scylla", "notes_timeline.cql"))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := parseSchemaKeys(string(raw))["notes_timeline"]
	if !ok {
		t.Fatal("notes_timeline.cql does not declare notes_timeline")
	}
	if keyList(got) != keyList(notesPrimaryKeys["notes_timeline"]) {
		t.Fatalf("DDL key (%s) != pinned (%s)", keyList(got), keyList(notesPrimaryKeys["notes_timeline"]))
	}
}
