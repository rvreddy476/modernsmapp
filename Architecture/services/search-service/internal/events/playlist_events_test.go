package events

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/atpost/shared/events"
)

// Playlists leaving search (2026-11-07): post-service announces a delete
// and a change of visibility; the document goes without a reindex.

const testPlaylistID = "5d2b9c1e-3a4f-4b6c-8d7e-9f0a1b2c3d04"

func playlistEvent(visibility string) events.TubePlaylistPayload {
	return events.TubePlaylistPayload{
		PlaylistID: testPlaylistID, OwnerID: "9b1e4c2a-7d3f-4e5a-8c6b-1a2b3c4d5e02",
		Visibility: visibility, OccurredAt: time.Now(),
	}
}

func TestPlaylistDeleted_RemovesTheCollection(t *testing.T) {
	// Even a playlist that was public when deleted.
	os := newRecordingOS(t)
	c := os.consumer(t)
	if err := c.processMessage(context.Background(), msg(t, events.TubePlaylistDeleted, playlistEvent("public"))); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	got := os.writes(search.IndexTubeCollections)
	if len(got) != 1 || got[0].method != http.MethodDelete || got[0].path != "/"+search.IndexTubeCollections+"/_doc/"+testPlaylistID {
		t.Fatalf("writes = %+v, want one delete of the playlist", got)
	}
}

func TestPlaylistVisibilityChanged_NotPublicRemovesTheCollection(t *testing.T) {
	for _, v := range []string{"private", "unlisted"} {
		os := newRecordingOS(t)
		c := os.consumer(t)
		p := playlistEvent(v)
		p.PreviousVisibility = "public"
		if err := c.processMessage(context.Background(), msg(t, events.TubePlaylistVisibilityChanged, p)); err != nil {
			t.Fatalf("processMessage: %v", err)
		}
		got := os.writes(search.IndexTubeCollections)
		if len(got) != 1 || got[0].method != http.MethodDelete {
			t.Fatalf("made %s: writes = %+v, want one delete", v, got)
		}
	}
}

// Made public: nothing to write (no title in the event); the reindex adds it.
func TestPlaylistVisibilityChanged_ToPublicWritesNothing(t *testing.T) {
	os := newRecordingOS(t)
	c := os.consumer(t)
	p := playlistEvent("public")
	p.PreviousVisibility = "private"
	if err := c.processMessage(context.Background(), msg(t, events.TubePlaylistVisibilityChanged, p)); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	if got := os.writes(search.IndexTubeCollections); len(got) != 0 {
		t.Fatalf("writes = %+v, want none", got)
	}
}
