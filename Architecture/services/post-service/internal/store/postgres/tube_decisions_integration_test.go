//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// The 31 Oct 2026 founder decisions' SQL against a real Postgres (CI only,
// on a _test database):
//
//	VIDEO_SERIES_POSTGRES_DSN=postgres://…/post_it_test?sslmode=disable \
//	  go test -tags integration ./internal/store/postgres/ -run TubeDecisions

// A playlist page shown to anyone but its owner leaves an unlisted video out,
// except for that video's own author, and stays a full page; the owner's
// page lists it.
func TestTubeDecisions_PlaylistItemsHideUnlistedInSQL(t *testing.T) {
	pool := openVideoSeriesDB(t)
	st := postgres.New(pool)
	ctx := context.Background()
	owner, author := newSeriesCreator(t, pool), newSeriesCreator(t, pool)
	p := tubePlaylist(t, pool, st, owner)

	public1 := insertSeriesPost(t, pool, author, nil, nil)
	unlisted1 := insertSeriesPost(t, pool, author, nil, nil)
	unlisted2 := insertSeriesPost(t, pool, author, nil, nil)
	public2 := insertSeriesPost(t, pool, author, nil, nil)
	if _, err := pool.Exec(ctx, `UPDATE posts SET visibility = 'unlisted' WHERE id = ANY($1)`, []uuid.UUID{unlisted1, unlisted2}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{public1, unlisted1, unlisted2, public2} {
		if _, err := st.AddPlaylistItem(ctx, p.ID, id, nil, postgres.MaxPlaylistItems); err != nil {
			t.Fatal(err)
		}
	}
	read := func(page postgres.PlaylistItemsPage) []uuid.UUID {
		items, err := st.GetPlaylistItems(ctx, p.ID, page)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]uuid.UUID, len(items))
		for i, it := range items {
			out[i] = it.PostID
		}
		return out
	}
	stranger := uuid.New()
	for name, viewer := range map[string]*uuid.UUID{"stranger": &stranger, "signed out": nil} {
		got := read(postgres.PlaylistItemsPage{Limit: 2, HideUnlisted: true, Viewer: viewer})
		if len(got) != 2 || got[0] != public1 || got[1] != public2 {
			t.Fatalf("%s: page %v; want a full page of the two public videos", name, got)
		}
	}
	if got := read(postgres.PlaylistItemsPage{Limit: 10, HideUnlisted: true, Viewer: &author}); len(got) != 4 {
		t.Fatalf("the videos' author: %v; want all four", got)
	}
	if got := read(postgres.PlaylistItemsPage{Limit: 10}); len(got) != 4 {
		t.Fatalf("the owner's page: %v; want all four", got)
	}
}
