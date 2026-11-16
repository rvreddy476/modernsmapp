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

// users_v1 profile writes against a REAL OpenSearch: the ordered Painless
// upsert (store/search/user_profile_state.go) for events and the reindex in
// any arrival order, with the lifecycle hide kept. CI only.

func TestOpenSearch_UserProfileEventsEndAtTheNewest(t *testing.T) {
	store := liveStore(t)
	c := liveConsumer(t, store)
	ctx := context.Background()
	t0 := time.Now().UTC().Truncate(time.Microsecond)
	id := uuid.New().String()
	t.Cleanup(func() { dropEntity(search.IndexUsers, id) })

	edit := func(name string, at time.Time) {
		t.Helper()
		if err := c.processMessage(ctx, msg(t, events.UserProfileUpdated, events.UserProfileUpdatedPayload{
			UserID: id, Username: name, DisplayName: name, UpdatedAt: at,
		})); err != nil {
			t.Fatalf("%s: %v (an ordered no-op must ack)", name, err)
		}
	}

	// A reindex page read at t0.
	if _, err := store.BulkIndexUsers(ctx, []search.UserDoc{{
		UserID: id, Username: "reindexed", DisplayName: "reindexed", SourceVersion: search.SourceVersion(t0),
	}}); err != nil {
		t.Fatal(err)
	}
	edit("before-read", t0.Add(-time.Second)) // older than the read: skipped
	if doc := rawEntity(t, search.IndexUsers, id); doc["display_name"] != "reindexed" {
		t.Fatalf("an edit older than the reindex read rewrote it: %v", doc)
	}

	edit("newest", t0.Add(2*time.Second))
	edit("stale", t0.Add(time.Second))    // older: skipped
	edit("newest", t0.Add(2*time.Second)) // redelivery: no-op
	doc := rawEntity(t, search.IndexUsers, id)
	if doc["display_name"] != "newest" || versionField(doc) != t0.Add(2*time.Second).UnixMicro() {
		t.Fatalf("stored = %v, want the newest edit at its version", doc)
	}

	// The same reindex page retried late, and a late registration.
	if _, err := store.BulkIndexUsers(ctx, []search.UserDoc{{
		UserID: id, Username: "reindexed", DisplayName: "reindexed", SourceVersion: search.SourceVersion(t0),
	}}); err != nil {
		t.Fatal(err)
	}
	if err := c.processMessage(ctx, msg(t, events.UserRegistered, events.UserRegisteredPayload{
		UserID: id, FirstName: "registered", CreatedAt: t0.Add(-time.Hour),
	})); err != nil {
		t.Fatal(err)
	}
	if doc := rawEntity(t, search.IndexUsers, id); doc["display_name"] != "newest" {
		t.Fatalf("a late reindex page or registration rewrote a newer edit: %v", doc)
	}

	// The lifecycle hide survives a newer edit.
	if err := store.UpdateUserHidden(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	edit("after-hide", t0.Add(3*time.Second))
	doc = rawEntity(t, search.IndexUsers, id)
	if doc["display_name"] != "after-hide" || doc["is_hidden"] != true {
		t.Fatalf("stored = %v, want the edit applied and the account still hidden", doc)
	}
}
