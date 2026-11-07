package events

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// community.updated carries the status and the visibility, so search can drop
// an archived or private community (audit 2026-11-07: a status change emitted
// nothing).
func TestCommunityUpdatedCarriesStatusAndVisibility(t *testing.T) {
	cases := []struct{ ctype, status, visibility string }{
		{"public", "active", "public"},
		{"education", "archived", "public"},
		{"professional", "active", "public"},
		{"private", "active", "private"},
		{"fan", "active", "private"},
		{"", "active", "private"},
	}
	for _, c := range cases {
		p := NewCommunityUpdatedPayload(uuid.New(), uuid.New(), "n", c.ctype, c.status)
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		_ = json.Unmarshal(raw, &got)
		if got["status"] != c.status {
			t.Errorf("%s/%s: status = %v", c.ctype, c.status, got["status"])
		}
		if got["visibility"] != c.visibility {
			t.Errorf("%s: visibility = %v, want %s", c.ctype, got["visibility"], c.visibility)
		}
	}
}

// The GDPR archive announces itself, and only after the archive succeeded.
func TestTheGDPRArchiveIsAnnounced(t *testing.T) {
	src, err := os.ReadFile("consumer.go")
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)
	archive := strings.Index(code, "c.store.ArchiveCommunity(ctx, communityID)")
	skip := strings.Index(code[archive:], "continue")
	announce := strings.Index(code[archive:], "c.announceStatus(ctx, communityID, userID)")
	if archive < 0 || skip < 0 || announce < 0 || skip > announce {
		t.Fatal("the GDPR archive does not announce community.updated, or announces a failed archive")
	}
	if !strings.Contains(code, "comm.Status)") {
		t.Fatal("announceStatus does not send the community's status")
	}
	main, err := os.ReadFile("../../cmd/server/main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(main), "WithPublisher(producer)") {
		t.Fatal("the consumer is built without a publisher: archives go unannounced")
	}
}
