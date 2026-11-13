package main

import (
	"testing"

	"github.com/google/uuid"
)

func TestParseNotesMode(t *testing.T) {
	id := uuid.NewString()
	if m, err := parseNotesMode("", ""); err != nil || m.purgeAuthor != uuid.Nil || m.rebuildUser != uuid.Nil {
		t.Fatalf("no flags: %+v %v", m, err)
	}
	if m, err := parseNotesMode(id, ""); err != nil || m.purgeAuthor.String() != id {
		t.Fatalf("purge: %+v %v", m, err)
	}
	if m, err := parseNotesMode("", " "+id+" "); err != nil || m.rebuildUser.String() != id {
		t.Fatalf("rebuild: %+v %v", m, err)
	}
	for _, bad := range [][2]string{{id, id}, {"nope", ""}, {"", "nope"}, {uuid.Nil.String(), ""}, {"", uuid.Nil.String()}} {
		if _, err := parseNotesMode(bad[0], bad[1]); err == nil {
			t.Errorf("parseNotesMode(%q, %q) accepted", bad[0], bad[1])
		}
	}
	if !validTableKinds["notes"] || !validTableKinds["home"] || !validTableKinds["author"] || validTableKinds["reels"] {
		t.Fatalf("table kinds %v", validTableKinds)
	}
}
