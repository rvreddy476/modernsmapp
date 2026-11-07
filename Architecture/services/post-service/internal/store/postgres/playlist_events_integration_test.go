//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Playlists leaving search (2026-11-07): a delete, and a change of
// visibility, are enqueued in the writing transaction. CI only, on a _test
// database (VIDEO_SERIES_POSTGRES_DSN).

type outboxRow struct {
	eventType string
	payload   events.TubePlaylistPayload
}

func playlistOutbox(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) []outboxRow {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT event_type, payload FROM post_outbox_events WHERE aggregate_id = $1 ORDER BY created_at, id`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []outboxRow
	for rows.Next() {
		var r outboxRow
		var raw []byte
		if err := rows.Scan(&r.eventType, &raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &r.payload); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func TestPlaylistEvents_VisibilityChangeIsEnqueuedAndAnEditIsNot(t *testing.T) {
	pool := openVideoSeriesDB(t)
	st := postgres.New(pool)
	ctx := context.Background()
	owner := newSeriesCreator(t, pool)
	p := tubePlaylist(t, pool, st, owner)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM post_outbox_events WHERE aggregate_id = $1`, p.ID) })

	title := "renamed"
	if _, err := st.UpdatePlaylist(ctx, p.ID, postgres.PlaylistPatch{Title: &title}); err != nil {
		t.Fatal(err)
	}
	same := "public"
	if _, err := st.UpdatePlaylist(ctx, p.ID, postgres.PlaylistPatch{Visibility: &same}); err != nil {
		t.Fatal(err)
	}
	if got := playlistOutbox(t, pool, p.ID); len(got) != 0 {
		t.Fatalf("an edit that keeps the visibility enqueued %+v", got)
	}

	private := "private"
	if _, err := st.UpdatePlaylist(ctx, p.ID, postgres.PlaylistPatch{Visibility: &private}); err != nil {
		t.Fatal(err)
	}
	got := playlistOutbox(t, pool, p.ID)
	if len(got) != 1 || got[0].eventType != events.TubePlaylistVisibilityChanged {
		t.Fatalf("outbox = %+v, want one visibility_changed", got)
	}
	pl := got[0].payload
	if pl.PlaylistID != p.ID.String() || pl.OwnerID != owner.String() || pl.Visibility != "private" || pl.PreviousVisibility != "public" {
		t.Fatalf("payload = %+v", pl)
	}
}

func TestPlaylistEvents_DeleteIsEnqueuedOnce(t *testing.T) {
	pool := openVideoSeriesDB(t)
	st := postgres.New(pool)
	ctx := context.Background()
	owner := newSeriesCreator(t, pool)
	p := tubePlaylist(t, pool, st, owner)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM post_outbox_events WHERE aggregate_id = $1`, p.ID) })

	if err := st.DeletePlaylist(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.DeletePlaylist(ctx, p.ID); err != nil {
		t.Fatalf("deleting a deleted playlist must be a no-op: %v", err)
	}
	got := playlistOutbox(t, pool, p.ID)
	if len(got) != 1 || got[0].eventType != events.TubePlaylistDeleted {
		t.Fatalf("outbox = %+v, want one tube.playlist.deleted", got)
	}
	if got[0].payload.PlaylistID != p.ID.String() || got[0].payload.OwnerID != owner.String() || got[0].payload.Visibility != "public" {
		t.Fatalf("payload = %+v", got[0].payload)
	}
}
