package events

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/community-service/internal/store"
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
		row := &store.Community{ID: uuid.New(), OwnerID: uuid.New(), Name: "n", CommunityType: c.ctype, Status: c.status}
		p := NewCommunityUpdatedPayload(row, uuid.New(), time.Now())
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
		snap, _ := got["community"].(map[string]any)
		if snap["status"] != c.status || snap["visibility"] != c.visibility {
			t.Errorf("%s/%s: snapshot = %v", c.ctype, c.status, snap)
		}
	}
}

// fixtureCommunity is the row behind the contract fixture: an education
// (public-type) community archived by the GDPR flow, which search must drop.
func fixtureCommunity() *store.Community {
	return &store.Community{
		ID:            uuid.MustParse("6f1c7c8e-4b8b-4e57-9a0e-0d5f2b8a1c11"),
		OwnerID:       uuid.MustParse("9b1e4c2a-7d3f-4e5a-8c6b-1a2b3c4d5e02"),
		Handle:        "riders",
		Name:          "Riders club",
		Description:   "",
		CommunityType: "education",
		Category:      "sports",
		TopicTags:     []string{"bikes"},
		MemberCount:   12,
		Status:        "archived",
		CreatedAt:     time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC),
		UpdatedAt:     time.Date(2026, 11, 8, 10, 30, 0, 0, time.UTC),
	}
}

// The fixture is the contract search-service decodes; it is copied byte for
// byte into search-service/internal/events/testdata/contracts/.
func TestCommunityUpdatedPayloadMatchesTheContract(t *testing.T) {
	actor := uuid.MustParse("9b1e4c2a-7d3f-4e5a-8c6b-1a2b3c4d5e02")
	now := time.Date(2026, 11, 8, 10, 30, 0, 0, time.UTC)
	got, err := json.MarshalIndent(NewCommunityUpdatedPayload(fixtureCommunity(), actor, now), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/contracts/community_updated.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSpace(got), bytes.TrimSpace(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")))) {
		t.Fatalf("community.updated drifted from the contract:\n%s\nwant:\n%s", got, want)
	}
}

// community.created keeps its original top-level fields for older consumers
// and adds the snapshot; a row without tags sends [] rather than null.
func TestCommunityCreatedPayloadKeepsTheOldFields(t *testing.T) {
	row := fixtureCommunity()
	row.Status, row.TopicTags = "active", nil
	p := NewCommunityCreatedPayload(row, time.Now())
	if p.CommunityID != row.ID.String() || p.OwnerID != row.OwnerID.String() || p.Name != row.Name ||
		p.CommunityType != row.CommunityType || !p.CreatedAt.Equal(row.CreatedAt) {
		t.Fatalf("old fields = %+v", p)
	}
	if p.Community == nil || p.Community.Handle != "riders" || p.Community.Status != "active" || p.Community.TopicTags == nil {
		t.Fatalf("snapshot = %+v", p.Community)
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
	// The re-read row (with its new status) is what is announced.
	if !strings.Contains(code, "c.store.GetCommunityByID(ctx, communityID)") ||
		!strings.Contains(code, "c.publisher.PublishCommunityUpdated(ctx, comm, actorID)") {
		t.Fatal("announceStatus does not send the re-read community row")
	}
	main, err := os.ReadFile("../../cmd/server/main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(main), "WithPublisher(producer)") {
		t.Fatal("the consumer is built without a publisher: archives go unannounced")
	}
}

// The lifecycle events are keyed by community id on a key-hashing balancer,
// so an archive and a stale edit of one community stay in order on one
// partition (search upserts an active snapshot: out of order, an archived
// community would come back). LeastBytes ignores the key.
func TestLifecycleEventsAreKeyedByCommunity(t *testing.T) {
	src, err := os.ReadFile("producer.go")
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)
	for _, must := range []string{
		"Balancer: &kafka.Hash{},",
		"p.publishKeyed(ctx, EventCommunityCreated, &ownerID, payload.CommunityID, payload)",
		"p.publishKeyed(ctx, EventCommunityUpdated, &actorID, payload.CommunityID, payload)",
		"p.publishKeyed(ctx, EventCommunityDeleted, &actorID, payload.CommunityID, payload)",
		"Key:   []byte(key),",
	} {
		if !strings.Contains(code, must) {
			t.Errorf("producer.go lost %q", must)
		}
	}
	if strings.Contains(code, "LeastBytes") && !strings.Contains(code, "Hash, not LeastBytes") {
		t.Error("a LeastBytes balancer ignores the partition key")
	}
}

// The top-level updated_at is the row's version, not the publish time: a
// consumer that reads it orders the event exactly as search orders the
// snapshot (store.Community: updated_at is the row's version).
func TestCommunityUpdatedTopLevelTimeIsTheRowVersion(t *testing.T) {
	row := fixtureCommunity()
	publish := row.UpdatedAt.Add(-time.Hour) // the clock is no authority
	p := NewCommunityUpdatedPayload(row, uuid.New(), publish)
	if !p.UpdatedAt.Equal(row.UpdatedAt) || !p.Community.UpdatedAt.Equal(row.UpdatedAt) {
		t.Fatalf("updated_at = %v / snapshot %v, want the row's %v", p.UpdatedAt, p.Community.UpdatedAt, row.UpdatedAt)
	}
	row.UpdatedAt = time.Time{}
	if p := NewCommunityUpdatedPayload(row, uuid.New(), publish); !p.UpdatedAt.Equal(publish) {
		t.Fatalf("a row without updated_at falls back to the publish time: got %v", p.UpdatedAt)
	}
}
