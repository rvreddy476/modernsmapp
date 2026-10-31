//go:build integration

package service

import (
	"context"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Series and playlist covers under the claim protocol (tube_cover_claims.go)
// against a real Postgres, media-service's claims faked: the row is created
// under the id its claim named, a cover change claims the new cover and
// releases the old one as the holder re-read sees the row, and a delete
// releases what the gone row held.
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/service/ -run MediaClaimsIT_Tube -v

func TestMediaClaimsIT_TubeSeriesCoverLifecycle(t *testing.T) {
	pool := openMediaClaimsServiceDB(t)
	svc, claims := claimsITService(t, pool)
	ctx := context.Background()
	creator := claimsITUser(t, pool)
	cover1 := claimsITAsset(t, pool, creator, "image")
	cover2 := claimsITAsset(t, pool, creator, "image")

	vs := &postgres.VideoSeries{CreatorID: creator, Title: "claims", CoverMediaID: &cover1, IsPublic: true}
	if err := svc.CreateVideoSeries(ctx, vs); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM video_series WHERE id = $1`, vs.ID) })
	sameIDs(t, "after create", claims.held(postgres.HolderVideoSeriesCover, vs.ID), cover1)

	if _, err := svc.UpdateVideoSeries(ctx, creator, vs.ID, postgres.VideoSeriesPatch{CoverMediaID: &cover2}); err != nil {
		t.Fatal(err)
	}
	sameIDs(t, "after the cover changed", claims.held(postgres.HolderVideoSeriesCover, vs.ID), cover2)

	if err := svc.DeleteVideoSeries(ctx, creator, vs.ID); err != nil {
		t.Fatal(err)
	}
	if got := claims.held(postgres.HolderVideoSeriesCover, vs.ID); len(got) != 0 {
		t.Fatalf("a deleted series kept its cover claim: %v", got)
	}
}

func TestMediaClaimsIT_TubePlaylistCoverLifecycle(t *testing.T) {
	pool := openMediaClaimsServiceDB(t)
	svc, claims := claimsITService(t, pool)
	ctx := context.Background()
	creator := claimsITUser(t, pool)
	cover1 := claimsITAsset(t, pool, creator, "image")
	cover2 := claimsITAsset(t, pool, creator, "image")

	c1 := cover1.String()
	p := &postgres.Playlist{CreatorID: creator, Title: "claims", CoverURL: &c1}
	if err := svc.CreatePlaylist(ctx, p); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM playlists WHERE id = $1`, p.ID) })
	sameIDs(t, "after create", claims.held(postgres.HolderPlaylistCover, p.ID), cover1)

	c2 := "/v1/media/" + cover2.String() + "/serve"
	if _, err := svc.UpdatePlaylist(ctx, creator, p.ID, postgres.PlaylistPatch{CoverURL: &c2}); err != nil {
		t.Fatal(err)
	}
	sameIDs(t, "after the cover changed", claims.held(postgres.HolderPlaylistCover, p.ID), cover2)

	none := ""
	if _, err := svc.UpdatePlaylist(ctx, creator, p.ID, postgres.PlaylistPatch{CoverURL: &none}); err != nil {
		t.Fatal(err)
	}
	if got := claims.held(postgres.HolderPlaylistCover, p.ID); len(got) != 0 {
		t.Fatalf("a cleared cover kept its claim: %v", got)
	}

	if _, err := svc.UpdatePlaylist(ctx, creator, p.ID, postgres.PlaylistPatch{CoverURL: &c2}); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeletePlaylist(ctx, creator, p.ID); err != nil {
		t.Fatal(err)
	}
	if got := claims.held(postgres.HolderPlaylistCover, p.ID); len(got) != 0 {
		t.Fatalf("a deleted playlist kept its cover claim: %v", got)
	}
}

// An unlisted episode is listed to its creator alone, through the real batch
// read (viewablePostAuthors carries each post's visibility from the row):
// a stranger's episode list leaves it out, the creator's keeps it.
func TestTubeDecisionsIT_UnlistedEpisodeIsListedOnlyToTheCreator(t *testing.T) {
	pool := openMediaClaimsServiceDB(t)
	svc, _ := claimsITService(t, pool)
	ctx := context.Background()
	creator, stranger := claimsITUser(t, pool), claimsITUser(t, pool)
	vs := &postgres.VideoSeries{CreatorID: creator, Title: "listing", IsPublic: true}
	if err := svc.CreateVideoSeries(ctx, vs); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM video_series WHERE id = $1`, vs.ID) })
	var posts []uuid.UUID
	for i, vis := range []string{"public", "unlisted", "public"} {
		id := uuid.New()
		if _, err := pool.Exec(ctx, `
			INSERT INTO posts (id, author_id, text, visibility, content_type, title, created_at, updated_at)
			VALUES ($1, $2, 'episode', $3, 'long_video', 'Episode', NOW(), NOW())`, id, creator, vis); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { claimsITDeletePost(t, pool, id) })
		if _, err := svc.AddEpisodeToVideoSeries(ctx, creator, vs.ID, id, i+1, nil); err != nil {
			t.Fatal(err)
		}
		posts = append(posts, id)
	}
	eps, err := svc.GetVideoSeriesEpisodes(ctx, vs.ID, &stranger)
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 2 || eps[0].PostID != posts[0] || eps[1].PostID != posts[2] {
		t.Fatalf("a stranger's episodes: %+v; want 1 and 3 (2 is unlisted)", eps)
	}
	if eps, err := svc.GetVideoSeriesEpisodes(ctx, vs.ID, &creator); err != nil || len(eps) != 3 {
		t.Fatalf("the creator's episodes: %d (err %v); want all three", len(eps), err)
	}
}
