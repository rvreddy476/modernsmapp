//go:build integration

package events

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// Snapshot order for communities and broadcast channels, against a REAL
// OpenSearch (store/search/entity_state.go). The unit tests pin the Go
// rules and simulate the script; these run the Painless script itself.
// CI: integration-opensearch.yml, `go test -tags integration ./internal/events/ -run OpenSearch`.

// rawEntity is the stored _source, or nil when there is no document.
func rawEntity(t *testing.T, index, id string) map[string]any {
	t.Helper()
	res, err := http.Get(strings.TrimRight(os.Getenv("OPENSEARCH_URL"), "/") + "/" + index + "/_doc/" + id)
	if err != nil {
		t.Fatalf("get %s/%s: %v", index, id, err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil
	}
	var body struct {
		Found  bool           `json:"found"`
		Source map[string]any `json:"_source"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode %s/%s: %v", index, id, err)
	}
	if !body.Found {
		return nil
	}
	return body.Source
}

func dropEntity(index, id string) {
	req, _ := http.NewRequest(http.MethodDelete, strings.TrimRight(os.Getenv("OPENSEARCH_URL"), "/")+"/"+index+"/_doc/"+id, nil)
	if res, err := http.DefaultClient.Do(req); err == nil {
		res.Body.Close()
	}
}

func liveChannel(id, marker, status string, at time.Time) *channelSnapshot {
	return &channelSnapshot{
		ChannelID: id, OwnerID: uuid.New().String(), Handle: marker, Name: marker,
		ChannelType: "public", Visibility: "public", Category: "test",
		SubscriberCount: 3, Status: status, CreatedAt: at, UpdatedAt: at,
	}
}

func liveCommunity(id, marker, status string, at time.Time) *communitySnapshot {
	return &communitySnapshot{
		CommunityID: id, OwnerID: uuid.New().String(), Handle: marker, Name: marker,
		CommunityType: "public", Visibility: "public", Category: "test",
		MemberCount: 3, Status: status, CreatedAt: at, UpdatedAt: at,
	}
}

func channelEvent(t *testing.T, s *channelSnapshot) kafka.Message {
	return msg(t, events.EventChannelUpdated, channelUpdatedPayload{ChannelID: s.ChannelID, UpdatedAt: time.Now(), Channel: s})
}

func communityEvent(t *testing.T, s *communitySnapshot) kafka.Message {
	return msg(t, events.EventCommunityUpdated, communityUpdatedPayload{CommunityID: s.CommunityID, UpdatedAt: time.Now(), Community: s})
}

// findable runs the ranked search a caller runs and reports whether the
// marker comes back.
func findable(t *testing.T, store *search.Store, entity, marker string) bool {
	t.Helper()
	index := search.EntityToIndex(entity)
	if err := store.RefreshIndex(context.Background(), index); err != nil {
		t.Fatal(err)
	}
	res, err := store.RankedSearch(context.Background(), entity, marker, search.RankedSearchOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	return len(res.Items) > 0
}

func versionField(doc map[string]any) int64 {
	v, _ := doc["source_updated_us"].(float64)
	return int64(v)
}

// THE bug: a stale "active" snapshot arriving after a newer "suspended" one
// must not re-list the entity, for both indices.
func TestOpenSearch_EntityStaleActiveNeverRelistsAHiddenEntity(t *testing.T) {
	store := liveStore(t)
	c := liveConsumer(t, store)
	ctx := context.Background()
	t0 := time.Now().UTC().Truncate(time.Microsecond)

	chID, chMarker := uuid.New().String(), "zz"+uuid.New().String()[:8]
	t.Cleanup(func() { dropEntity(search.IndexChannels, chID) })
	for _, s := range []*channelSnapshot{
		liveChannel(chID, chMarker, search.StatusActive, t0),
		liveChannel(chID, chMarker, "suspended", t0.Add(2*time.Second)),
		liveChannel(chID, chMarker, search.StatusActive, t0.Add(time.Second)), // stale
	} {
		if err := c.processMessage(ctx, channelEvent(t, s)); err != nil {
			t.Fatal(err)
		}
	}
	if findable(t, store, search.EntityChannels, chMarker) {
		t.Fatal("a stale active snapshot re-listed a suspended channel")
	}
	doc := rawEntity(t, search.IndexChannels, chID)
	if doc["removed"] != true || versionField(doc) != t0.Add(2*time.Second).UnixMicro() || doc["name"] != nil || doc["owner_id"] != nil {
		t.Fatalf("stored = %v, want a bare tombstone at the suspension's version", doc)
	}

	coID, coMarker := uuid.New().String(), "zz"+uuid.New().String()[:8]
	t.Cleanup(func() { dropEntity(search.IndexCommunities, coID) })
	for _, s := range []*communitySnapshot{
		liveCommunity(coID, coMarker, "archived", t0.Add(2*time.Second)), // lands first: no document yet
		liveCommunity(coID, coMarker, search.StatusActive, t0.Add(time.Second)),
	} {
		if err := c.processMessage(ctx, communityEvent(t, s)); err != nil {
			t.Fatal(err)
		}
	}
	if findable(t, store, search.EntityCommunities, coMarker) {
		t.Fatal("a stale active snapshot created an archived community")
	}

	// A newer active snapshot does bring it back.
	if err := c.processMessage(ctx, communityEvent(t, liveCommunity(coID, coMarker, search.StatusActive, t0.Add(3*time.Second)))); err != nil {
		t.Fatal(err)
	}
	if !findable(t, store, search.EntityCommunities, coMarker) {
		t.Fatal("a newer active snapshot did not restore the community")
	}
}

// The race itself: a newer removal and a stale active snapshot applied
// concurrently. The removal wins every time; the mirror image (newer
// active, stale removal) stays visible every time.
func TestOpenSearch_EntityConcurrentSnapshotsResolveToTheNewest(t *testing.T) {
	store := liveStore(t)
	ctx := context.Background()
	for i := 0; i < 30; i++ {
		for _, newestHidden := range []bool{true, false} {
			id, marker := uuid.New().String(), "zz"+uuid.New().String()[:8]
			t0 := time.Now().UTC().Truncate(time.Microsecond)
			base := liveChannel(id, marker, search.StatusActive, t0).doc("")
			if err := store.ApplyChannelState(ctx, base); err != nil {
				t.Fatal(err)
			}
			older, newer := search.StatusActive, "suspended"
			if !newestHidden {
				older, newer = "suspended", search.StatusActive
			}
			var wg sync.WaitGroup
			errs := make(chan error, 2)
			for _, s := range []*channelSnapshot{
				liveChannel(id, marker, older, t0.Add(time.Second)),
				liveChannel(id, marker, newer, t0.Add(2*time.Second)),
			} {
				wg.Add(1)
				go func(d search.ChannelDoc) {
					defer wg.Done()
					errs <- store.ApplyChannelState(ctx, d)
				}(s.doc(""))
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
			if got := findable(t, store, search.EntityChannels, marker); got == newestHidden {
				t.Fatalf("iteration %d: newest hidden=%v but findable=%v", i, newestHidden, got)
			}
			if v := versionField(rawEntity(t, search.IndexChannels, id)); v != t0.Add(2*time.Second).UnixMicro() {
				t.Fatalf("iteration %d: stored version %d, want the newest", i, v)
			}
			dropEntity(search.IndexChannels, id)
		}
	}
}

// Each rule row of entity_state.go, executed by the real script. kind is
// what is stored afterwards: absent, live, tombstone or deleted.
func TestOpenSearch_EntityRulesHoldInPainless(t *testing.T) {
	store := liveStore(t)
	ctx := context.Background()
	type step func(id, marker string) error
	snap := func(status string, v int64) step {
		return func(id, marker string) error {
			d := liveChannel(id, marker, status, time.UnixMicro(v).UTC()).doc("")
			d.CreatedAt = time.Now().UTC() // ranked by recency too; keep it findable
			if v == 0 {
				d.SourceVersion = 0
			}
			return store.ApplyChannelState(ctx, d)
		}
	}
	del := func(id, _ string) error { return store.DeleteChannel(ctx, id) }
	cases := []struct {
		name    string
		steps   []step
		kind    string
		version int64
	}{
		{"versioned create", []step{snap("active", 100)}, "live", 100},
		{"newer wins", []step{snap("active", 100), snap("active", 200)}, "live", 200},
		{"older dropped", []step{snap("active", 200), snap("active", 100)}, "live", 200},
		{"same redelivered", []step{snap("active", 200), snap("active", 200)}, "live", 200},
		{"removal remembered when first", []step{snap("suspended", 200)}, "tombstone", 200},
		{"stale active after removal", []step{snap("active", 100), snap("suspended", 300), snap("active", 200)}, "tombstone", 300},
		{"removal wins ties", []step{snap("suspended", 200), snap("active", 200)}, "tombstone", 200},
		{"stale removal dropped", []step{snap("active", 300), snap("suspended", 200)}, "live", 300},
		{"newer active restores", []step{snap("suspended", 200), snap("active", 300)}, "live", 300},
		{"newer removal raises", []step{snap("suspended", 200), snap("archived", 300)}, "tombstone", 300},
		{"legacy create", []step{snap("active", 0)}, "live", 0},
		{"legacy never over versioned", []step{snap("suspended", 200), snap("active", 0)}, "tombstone", 200},
		{"versioned over legacy", []step{snap("active", 0), snap("suspended", 200)}, "tombstone", 200},
		{"legacy removal fails closed", []step{snap("active", 200), snap("suspended", 0)}, "tombstone", 200},
		{"legacy removal on nothing", []step{snap("suspended", 0)}, "absent", 0},
		{"delete is terminal", []step{snap("active", 100), del, snap("active", 1<<52)}, "deleted", 100},
		{"delete first", []step{del, snap("active", 100)}, "deleted", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, marker := uuid.New().String(), "zz"+uuid.New().String()[:8]
			t.Cleanup(func() { dropEntity(search.IndexChannels, id) })
			for _, s := range tc.steps {
				if err := s(id, marker); err != nil {
					t.Fatal(err)
				}
			}
			doc := rawEntity(t, search.IndexChannels, id)
			kind := "absent"
			switch {
			case doc == nil:
			case doc["deleted"] == true:
				kind = "deleted"
			case doc["removed"] == true:
				kind = "tombstone"
			default:
				kind = "live"
			}
			if kind != tc.kind || versionField(doc) != tc.version {
				t.Fatalf("stored %s at %d, want %s at %d: %v", kind, versionField(doc), tc.kind, tc.version, doc)
			}
			if got := findable(t, store, search.EntityChannels, marker); got != (kind == "live") {
				t.Fatalf("findable = %v for a %s document", got, kind)
			}
		})
	}
}

// The legacy partial update lands on a live unversioned community only.
func TestOpenSearch_CommunityPatchNeverCreatesOrReopens(t *testing.T) {
	store := liveStore(t)
	ctx := context.Background()
	id := uuid.New().String()
	t.Cleanup(func() { dropEntity(search.IndexCommunities, id) })
	fields := map[string]any{"name": "zzpatched", "community_type": "public", "status": search.StatusActive}

	if err := store.UpdateCommunityFields(ctx, id, fields); err != nil {
		t.Fatal(err)
	}
	if doc := rawEntity(t, search.IndexCommunities, id); doc != nil {
		t.Fatalf("a partial update created a document: %v", doc)
	}
	if err := store.HideCommunity(ctx, id, 500); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateCommunityFields(ctx, id, fields); err != nil {
		t.Fatal(err)
	}
	if doc := rawEntity(t, search.IndexCommunities, id); doc["removed"] != true || doc["name"] != nil || doc["status"] != search.StatusRemoved {
		t.Fatalf("a partial update reopened a tombstone: %v", doc)
	}
}

// Engagement events never create a document, and a tombstone keeps none.
func TestOpenSearch_EngagementNeverCreatesAStub(t *testing.T) {
	store := liveStore(t)
	ctx := context.Background()
	id := uuid.New().String()
	t.Cleanup(func() { dropEntity(search.IndexChannels, id) })
	if err := store.AddToEngagementScore(ctx, search.IndexChannels, id, 1); err != nil {
		t.Fatal(err)
	}
	if doc := rawEntity(t, search.IndexChannels, id); doc != nil {
		t.Fatalf("a subscribe on an unknown channel created %v", doc)
	}
	if err := store.ApplyChannelState(ctx, liveChannel(id, "zzx", "suspended", time.Now()).doc("")); err != nil {
		t.Fatal(err)
	}
	if err := store.AddToEngagementScore(ctx, search.IndexChannels, id, 1); err != nil {
		t.Fatal(err)
	}
	if doc := rawEntity(t, search.IndexChannels, id); doc["engagement_score"] != nil {
		t.Fatalf("a tombstone collected a score: %v", doc)
	}
}

// The backfill's purge removes stubs left by the old upsert, and only them.
func TestOpenSearch_PurgeEngagementStubsRemovesOnlyStubs(t *testing.T) {
	store := liveStore(t)
	ctx := context.Background()
	stub, real := uuid.New().String(), uuid.New().String()
	t.Cleanup(func() { dropEntity(search.IndexChannels, stub); dropEntity(search.IndexChannels, real) })
	req, _ := http.NewRequest(http.MethodPut, strings.TrimRight(os.Getenv("OPENSEARCH_URL"), "/")+"/"+search.IndexChannels+"/_doc/"+stub,
		strings.NewReader(`{"engagement_score":3}`))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if err := store.ApplyChannelState(ctx, liveChannel(real, "zzreal", search.StatusActive, time.Now()).doc("")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PurgeEngagementStubs(ctx); err != nil {
		t.Fatal(err)
	}
	if doc := rawEntity(t, search.IndexChannels, stub); doc != nil {
		t.Fatalf("stub survived: %v", doc)
	}
	if doc := rawEntity(t, search.IndexChannels, real); doc == nil {
		t.Fatal("the purge removed a real channel")
	}
}
