//go:build integration

package events

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
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

// A channel event with channel_id creates the document search has never
// seen, hidden when the owner is; the ordered merge and a later reindex
// apply on top; an event without channel_id still creates nothing.
func TestOpenSearch_TubeChannelEventCreatesAnAbsentChannel(t *testing.T) {
	store := liveStore(t)
	c := liveConsumer(t, store)
	ctx := context.Background()
	if err := store.EnsureSafetyIndices(ctx); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().UTC().Truncate(time.Microsecond)
	channelID, ownerID := uuid.New().String(), uuid.New().String()
	t.Cleanup(func() { dropEntity(search.IndexTubeChannels, channelID) })

	send := func(eventType string, p events.ChannelPayload) {
		t.Helper()
		if err := c.processMessage(ctx, msg(t, eventType, p)); err != nil {
			t.Fatal(err)
		}
	}
	send(events.TubeChannelCreated, events.ChannelPayload{
		ChannelID: channelID, UserID: ownerID, Name: "fresh", Handle: "fresh", CreatedAt: t0, UpdatedAt: t0,
	})
	doc := rawEntity(t, search.IndexTubeChannels, channelID)
	if doc == nil || doc["name"] != "fresh" || doc["owner_id"] != ownerID || doc["is_hidden"] != false ||
		doc["follower_count"] != float64(0) || versionField(doc) != t0.UnixMicro() || doc["reindexed_at"] == nil {
		t.Fatalf("stored = %v, want the created channel document", doc)
	}

	send(events.TubeChannelUpdated, events.ChannelPayload{
		ChannelID: channelID, UserID: ownerID, Name: "renamed", Handle: "renamed", UpdatedAt: t0.Add(2 * time.Second),
	})
	send(events.TubeChannelUpdated, events.ChannelPayload{
		ChannelID: channelID, UserID: ownerID, Name: "stale", Handle: "stale", UpdatedAt: t0.Add(time.Second),
	})
	doc = rawEntity(t, search.IndexTubeChannels, channelID)
	if doc["name"] != "renamed" || versionField(doc) != t0.Add(2*time.Second).UnixMicro() {
		t.Fatalf("stored = %v, want the newer edit kept over the older one", doc)
	}

	// A later reindex with the listed row overwrites it whole.
	stamp := t0.Add(time.Minute)
	listed := t0.Add(3 * time.Second)
	if _, err := store.BulkIndexTubeChannels(ctx, []search.TubeChannelDoc{{
		ChannelID: channelID, OwnerID: ownerID, Name: "listed", Handle: "listed", FollowerCount: 4,
		CreatedAt: t0, UpdatedAt: listed, SourceUpdatedUs: search.SourceVersion(listed), ReindexedAt: &stamp,
	}}); err != nil {
		t.Fatal(err)
	}
	doc = rawEntity(t, search.IndexTubeChannels, channelID)
	if doc["name"] != "listed" || doc["follower_count"] != float64(4) {
		t.Fatalf("stored = %v, want the reindex's row", doc)
	}

	// A hidden owner's new channel is created hidden.
	hiddenChannel, hiddenOwner := uuid.New().String(), uuid.New().String()
	t.Cleanup(func() {
		dropEntity(search.IndexTubeChannels, hiddenChannel)
		dropEntity(search.IndexUsers, hiddenOwner)
	})
	if err := store.UpdateUserHidden(ctx, hiddenOwner, true); err != nil {
		t.Fatal(err)
	}
	send(events.TubeChannelCreated, events.ChannelPayload{
		ChannelID: hiddenChannel, UserID: hiddenOwner, Name: "quiet", Handle: "quiet", UpdatedAt: t0,
	})
	if doc := rawEntity(t, search.IndexTubeChannels, hiddenChannel); doc == nil || doc["is_hidden"] != true {
		t.Fatalf("stored = %v, want a hidden document", doc)
	}

	// No channel_id: update-only, so an owner with no document gets none.
	legacyOwner := uuid.New().String()
	send(events.TubeChannelCreated, events.ChannelPayload{
		UserID: legacyOwner, Name: "legacy", Handle: "legacy", UpdatedAt: t0,
	})
	if err := store.RefreshIndex(ctx, search.IndexTubeChannels); err != nil {
		t.Fatal(err)
	}
	q := `{"query":{"term":{"owner_id":"` + legacyOwner + `"}}}`
	res, err := http.Post(strings.TrimRight(os.Getenv("OPENSEARCH_URL"), "/")+"/"+search.IndexTubeChannels+"/_count",
		"application/json", strings.NewReader(q))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var counted struct {
		Count int64 `json:"count"`
	}
	if err := json.NewDecoder(res.Body).Decode(&counted); err != nil {
		t.Fatal(err)
	}
	if counted.Count != 0 {
		t.Fatalf("an event without channel_id created %d documents", counted.Count)
	}
}
