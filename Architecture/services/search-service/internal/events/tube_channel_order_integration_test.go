//go:build integration

package events

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// Tube channel events and the reindex rewrite against a REAL OpenSearch:
// the two Painless scripts (tube_channel_state.go, keepHiddenUpsertScript)
// in any arrival order. CI: integration-opensearch.yml, -run OpenSearch.

func TestOpenSearch_TubeChannelEventsEndAtTheNewestRow(t *testing.T) {
	store := liveStore(t)
	c := liveConsumer(t, store)
	ctx := context.Background()
	t0 := time.Now().UTC().Truncate(time.Microsecond)
	channelID, ownerID := uuid.New().String(), uuid.New().String()
	t.Cleanup(func() { dropEntity(search.IndexTubeChannels, channelID) })

	// The reindex wrote the channel (version t0), hidden by the owner's
	// deactivation since.
	stamp := t0
	if _, err := store.BulkIndexTubeChannels(ctx, []search.TubeChannelDoc{{
		ChannelID: channelID, OwnerID: ownerID, Name: "listed", Handle: "listed",
		FollowerCount: 7, UpdatedAt: t0, SourceUpdatedUs: search.SourceVersion(t0), ReindexedAt: &stamp,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateOwnerTubeHidden(ctx, ownerID, true); err != nil {
		t.Fatal(err)
	}

	edit := func(name string, at time.Time) {
		t.Helper()
		if err := c.processMessage(ctx, msg(t, events.TubeChannelUpdated, events.ChannelPayload{
			UserID: ownerID, Name: name, Handle: name, UpdatedAt: at,
		})); err != nil {
			t.Fatal(err)
		}
	}
	edit("newest", t0.Add(2*time.Second))
	edit("stale", t0.Add(time.Second)) // older: refused
	doc := rawEntity(t, search.IndexTubeChannels, channelID)
	if doc["name"] != "newest" || versionField(doc) != t0.Add(2*time.Second).UnixMicro() {
		t.Fatalf("stored = %v, want the newest edit at its version", doc)
	}
	if doc["is_hidden"] != true || doc["follower_count"] != float64(7) || doc["channel_id"] != channelID {
		t.Fatalf("stored = %v: the event dropped a field it does not own", doc)
	}

	// A reindex that read the row before the newest edit keeps the edit,
	// and still stamps reindexed_at so the sweep keeps the document.
	later := t0.Add(time.Minute)
	if _, err := store.BulkIndexTubeChannels(ctx, []search.TubeChannelDoc{{
		ChannelID: channelID, OwnerID: ownerID, Name: "listed", Handle: "listed",
		UpdatedAt: t0, SourceUpdatedUs: search.SourceVersion(t0), ReindexedAt: &later,
	}}); err != nil {
		t.Fatal(err)
	}
	doc = rawEntity(t, search.IndexTubeChannels, channelID)
	if doc["name"] != "newest" {
		t.Fatalf("stored = %v: an older listing overwrote a newer edit", doc)
	}
	if got, _ := time.Parse(time.RFC3339Nano, doc["reindexed_at"].(string)); !got.Equal(later) {
		t.Fatalf("reindexed_at = %v, want the run's stamp %v", doc["reindexed_at"], later)
	}

	// A newer listing does rewrite it, and keeps the hide.
	if _, err := store.BulkIndexTubeChannels(ctx, []search.TubeChannelDoc{{
		ChannelID: channelID, OwnerID: ownerID, Name: "relisted", Handle: "relisted",
		UpdatedAt: t0.Add(3 * time.Second), SourceUpdatedUs: search.SourceVersion(t0.Add(3 * time.Second)), ReindexedAt: &later,
	}}); err != nil {
		t.Fatal(err)
	}
	doc = rawEntity(t, search.IndexTubeChannels, channelID)
	if doc["name"] != "relisted" || doc["is_hidden"] != true {
		t.Fatalf("stored = %v, want the newer listing, still hidden", doc)
	}
}
