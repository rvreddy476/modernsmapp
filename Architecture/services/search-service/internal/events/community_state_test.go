package events

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/atpost/shared/events"
	"github.com/segmentio/kafka-go"
)

// community.created / community.updated carry the whole row since 2026-11-08
// (community-service CommunitySnapshot), and community.updated has carried
// status and visibility since 2026-11-07. What the consumer writes for each,
// as OpenSearch sees it.

const testCommunityOwner = "9b1e4c2a-7d3f-4e5a-8c6b-1a2b3c4d5e02"

func activeCommunitySnapshot() *communitySnapshot {
	return &communitySnapshot{
		CommunityID: testCommunityID, OwnerID: testCommunityOwner,
		Handle: "riders", Name: "Riders club", Description: "",
		CommunityType: "education", Visibility: "public", Category: "sports",
		TopicTags: []string{"bikes"}, MemberCount: 12, Status: search.StatusActive,
		CreatedAt: time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 11, 8, 10, 30, 0, 0, time.UTC),
	}
}

func communityUpdated(t *testing.T, p communityUpdatedPayload) kafka.Message {
	t.Helper()
	if p.CommunityID == "" {
		p.CommunityID = testCommunityID
	}
	return msg(t, events.EventCommunityUpdated, p)
}

func processOne(t *testing.T, m kafka.Message) []recordedReq {
	t.Helper()
	os := newRecordingOS(t)
	c := os.consumer(t)
	if err := c.processMessage(context.Background(), m); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	return os.writes(search.IndexCommunities)
}

func wantOneDelete(t *testing.T, got []recordedReq) {
	t.Helper()
	if len(got) != 1 || got[0].method != http.MethodDelete || !strings.Contains(got[0].path, testCommunityID) {
		t.Fatalf("writes = %+v, want one delete of the community", got)
	}
}

// The fixture is community-service's, byte for byte: an education community
// archived by the GDPR flow. It must decode and remove the document.
func TestCommunityUpdated_ContractFixtureRemovesAnArchivedCommunity(t *testing.T) {
	raw, err := os.ReadFile("testdata/contracts/community_updated.json")
	if err != nil {
		t.Fatal(err)
	}
	var p communityUpdatedPayload
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&p); err != nil {
		t.Fatalf("the contract fixture does not decode: %v", err)
	}
	if p.Status != "archived" || p.Visibility != "public" || p.Community == nil ||
		p.Community.Status != "archived" || p.Community.Handle != "riders" || p.Community.OwnerID != testCommunityOwner ||
		p.Community.MemberCount != 12 || len(p.Community.TopicTags) != 1 {
		t.Fatalf("decoded %+v / %+v", p, p.Community)
	}
	env, _ := json.Marshal(events.EventEnvelope{EventType: events.EventCommunityUpdated, Payload: raw})
	wantOneDelete(t, processOne(t, kafka.Message{Value: env}))

	// The same contract with the community active again is an upsert of the
	// whole row: the owner and handle reach the document.
	var generic map[string]any
	_ = json.Unmarshal(raw, &generic)
	generic["status"] = "active"
	generic["community"].(map[string]any)["status"] = "active"
	active, _ := json.Marshal(generic)
	env, _ = json.Marshal(events.EventEnvelope{EventType: events.EventCommunityUpdated, Payload: active})
	got := processOne(t, kafka.Message{Value: env})
	if len(got) != 1 || !strings.Contains(got[0].path, "/_update/"+testCommunityID) {
		t.Fatalf("writes = %+v, want one _update", got)
	}
	if doc, _ := got[0].body["doc"].(map[string]any); doc["owner_id"] != testCommunityOwner || doc["handle"] != "riders" {
		t.Fatalf("doc = %v", got[0].body)
	}
}

// A community that stops being searchable leaves search, whichever field
// says so: the status, the visibility or the type.
func TestCommunityUpdated_SnapshotNotSearchableRemovesTheDocument(t *testing.T) {
	cases := map[string]func(s *communitySnapshot){
		"archived":           func(s *communitySnapshot) { s.Status = "archived" },
		"suspended":          func(s *communitySnapshot) { s.Status = "suspended" },
		"deleted":            func(s *communitySnapshot) { s.Status = "deleted" },
		"no status":          func(s *communitySnapshot) { s.Status = "" },
		"private type":       func(s *communitySnapshot) { s.CommunityType, s.Visibility = "private", "private" },
		"fan type":           func(s *communitySnapshot) { s.CommunityType, s.Visibility = "fan", "private" },
		"private visibility": func(s *communitySnapshot) { s.Visibility = "private" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			snap := activeCommunitySnapshot()
			mutate(snap)
			// The top-level fields stay those of an active public community,
			// so the snapshot alone must decide.
			wantOneDelete(t, processOne(t, communityUpdated(t, communityUpdatedPayload{
				Name: snap.Name, CommunityType: "education", Status: "active",
				Visibility: "public", UpdatedAt: time.Now(), Community: snap,
			})))
		})
	}
}

// A searchable community is a field update with an upsert: the edit lands,
// engagement_score (kept by member events) is untouched, and a community with
// no document (just made public) is created whole, owner included.
func TestCommunityUpdated_SnapshotSearchableIsAnUpsertOfEveryField(t *testing.T) {
	snap := activeCommunitySnapshot()
	got := processOne(t, communityUpdated(t, communityUpdatedPayload{
		Name: snap.Name, CommunityType: snap.CommunityType, Status: snap.Status,
		Visibility: snap.Visibility, UpdatedAt: time.Now(), Community: snap,
	}))
	if len(got) != 1 || got[0].method != http.MethodPost || !strings.Contains(got[0].path, "/_update/"+testCommunityID) {
		t.Fatalf("writes = %+v, want one _update", got)
	}
	doc, _ := got[0].body["doc"].(map[string]any)
	for field, want := range map[string]any{
		"name": "Riders club", "handle": "riders", "community_type": "education", "status": search.StatusActive,
		"owner_id": testCommunityOwner, "category": "sports", "member_count": float64(12),
		"description": "", // cleared, and the clear must be written
	} {
		if doc[field] != want {
			t.Fatalf("doc[%q] = %v, want %v (doc %v)", field, doc[field], want, doc)
		}
	}
	if _, ok := doc["engagement_score"]; ok {
		t.Fatalf("an edit must not reset engagement_score: %v", doc)
	}
	upsert, _ := got[0].body["upsert"].(map[string]any)
	if upsert["status"] != search.StatusActive || upsert["owner_id"] != testCommunityOwner || upsert["engagement_score"] == nil {
		t.Fatalf("upsert = %v, want the whole document", upsert)
	}
}

// community.updated from the 2026-11-07 producer (status and visibility, no
// row): a status other than active or a private visibility removes the
// document even when the type is public.
func TestCommunityUpdated_StatusAndVisibilityFieldsAreHonoured(t *testing.T) {
	for name, p := range map[string]communityUpdatedPayload{
		"archived":   {Name: "n", CommunityType: "education", Status: "archived", Visibility: "public"},
		"suspended":  {Name: "n", CommunityType: "public", Status: "suspended", Visibility: "public"},
		"private":    {Name: "n", CommunityType: "public", Status: "active", Visibility: "private"},
		"type local": {Name: "n", CommunityType: "local", Status: "active", Visibility: "private"},
	} {
		t.Run(name, func(t *testing.T) {
			p.UpdatedAt = time.Now()
			wantOneDelete(t, processOne(t, communityUpdated(t, p)))
		})
	}

	// Active and public: a partial update that writes the status and never
	// creates a document (no owner to write).
	got := processOne(t, communityUpdated(t, communityUpdatedPayload{
		Name: "n", CommunityType: "professional", Status: "active", Visibility: "public", UpdatedAt: time.Now(),
	}))
	if len(got) != 1 || got[0].method != http.MethodPost || !strings.Contains(got[0].path, "/_update/") {
		t.Fatalf("writes = %+v, want one partial update", got)
	}
	if _, upsert := got[0].body["upsert"]; upsert {
		t.Fatal("a payload without the row must never create a document")
	}
	if doc, _ := got[0].body["doc"].(map[string]any); doc["status"] != "active" || doc["community_type"] != "professional" {
		t.Fatalf("partial doc = %v", got[0].body)
	}
}

// community.created with the row indexes the handle too, through the same
// path; created private, it is never written.
func TestCommunityCreated_SnapshotIsApplied(t *testing.T) {
	snap := activeCommunitySnapshot()
	got := processOne(t, msg(t, events.EventCommunityCreated, communityCreatedPayload{
		CommunityID: testCommunityID, OwnerID: snap.OwnerID, Name: snap.Name, CommunityType: snap.CommunityType,
		CreatedAt: snap.CreatedAt, Community: snap,
	}))
	if len(got) != 1 || !strings.Contains(got[0].path, "/_update/") {
		t.Fatalf("writes = %+v, want one _update", got)
	}
	if doc, _ := got[0].body["doc"].(map[string]any); doc["handle"] != "riders" {
		t.Fatalf("doc = %v", got[0].body)
	}

	snap.CommunityType, snap.Visibility = "private", "private"
	for _, w := range processOne(t, msg(t, events.EventCommunityCreated, communityCreatedPayload{
		CommunityID: testCommunityID, Community: snap,
	})) {
		if w.method != http.MethodDelete {
			t.Fatalf("a private community was written: %s %s %v", w.method, w.path, w.body)
		}
	}
}

// Every other event community-service publishes on community-events is
// handled or deliberately ignored. The member events move the engagement
// score of the community named in the payload; deleted removes it.
func TestCommunityTopicEventsAreHandled(t *testing.T) {
	for _, eventType := range []string{
		events.EventCommunityMemberJoined, events.EventCommunityMemberLeft, events.EventCommunityMemberBanned,
	} {
		got := processOne(t, msg(t, eventType, map[string]any{
			"community_id": testCommunityID, "user_id": testCommunityOwner, "joined_at": time.Now(),
		}))
		if len(got) != 1 || !strings.Contains(got[0].path, "/_update/"+testCommunityID) {
			t.Fatalf("%s: writes = %+v, want one engagement update", eventType, got)
		}
	}
	wantOneDelete(t, processOne(t, msg(t, events.EventCommunityDeleted, map[string]any{
		"community_id": testCommunityID, "actor_id": testCommunityOwner, "deleted_at": time.Now(),
	})))
	for _, ignored := range []string{
		events.EventCommunityMemberRoleChanged, events.EventCommunitySpaceCreated,
		events.EventCommunitySpaceRemoved, events.EventCommunitySpaceQuarantined,
	} {
		if got := processOne(t, msg(t, ignored, map[string]any{"community_id": testCommunityID})); len(got) != 0 {
			t.Fatalf("%s wrote %+v; search has no use for it", ignored, got)
		}
	}
}
