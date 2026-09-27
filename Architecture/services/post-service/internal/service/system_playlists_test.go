package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// System collections (2026-09-27): a server-owned playlist refuses every
// generic write, the Queue routes are idempotent, and only long videos
// mirror into "Loved".

type fakeSystemPlaylistStore struct {
	created map[string]*postgres.Playlist // key owner|kind
	items   map[string]map[uuid.UUID]bool
	adds    []string
	removes []string
}

func newFakeSystemPlaylistStore() *fakeSystemPlaylistStore {
	return &fakeSystemPlaylistStore{created: map[string]*postgres.Playlist{}, items: map[string]map[uuid.UUID]bool{}}
}

func spKey(owner uuid.UUID, kind string) string { return owner.String() + "|" + kind }

func (f *fakeSystemPlaylistStore) GetOrCreateSystemPlaylist(_ context.Context, owner uuid.UUID, kind string) (*postgres.Playlist, error) {
	k := spKey(owner, kind)
	if p, ok := f.created[k]; ok {
		return p, nil
	}
	p := &postgres.Playlist{ID: uuid.New(), CreatorID: owner, Title: postgres.SystemPlaylistTitle(kind), Visibility: "private", Kind: kind}
	f.created[k] = p
	f.items[k] = map[uuid.UUID]bool{}
	return p, nil
}

func (f *fakeSystemPlaylistStore) AddToSystemPlaylist(ctx context.Context, owner uuid.UUID, kind string, postID uuid.UUID) (bool, error) {
	if _, err := f.GetOrCreateSystemPlaylist(ctx, owner, kind); err != nil {
		return false, err
	}
	f.adds = append(f.adds, kind)
	k := spKey(owner, kind)
	if f.items[k][postID] {
		return false, nil
	}
	f.items[k][postID] = true
	return true, nil
}

func (f *fakeSystemPlaylistStore) RemoveFromSystemPlaylist(_ context.Context, owner uuid.UUID, kind string, postID uuid.UUID) (bool, error) {
	f.removes = append(f.removes, kind)
	k := spKey(owner, kind)
	if !f.items[k][postID] {
		return false, nil
	}
	delete(f.items[k], postID)
	return true, nil
}

func (f *fakeSystemPlaylistStore) IsInSystemPlaylist(_ context.Context, owner uuid.UUID, kind string, postID uuid.UUID) (bool, error) {
	return f.items[spKey(owner, kind)][postID], nil
}

func TestSystemPlaylistRefusesGenericWrites(t *testing.T) {
	owner := uuid.New()
	system := &postgres.Playlist{ID: uuid.New(), CreatorID: owner, Kind: postgres.PlaylistKindWatchLater, Title: "Queue", Visibility: "private"}
	svc := newAuthoringService(&fakeAuthoringStore{playlist: system})
	ctx := context.Background()

	if _, err := svc.UpdatePlaylist(ctx, owner, system.ID, postgres.PlaylistPatch{Title: strptr("Mine")}); !errors.Is(err, ErrSystemPlaylist) {
		t.Fatalf("rename: err=%v want ErrSystemPlaylist", err)
	}
	if _, err := svc.UpdatePlaylist(ctx, owner, system.ID, postgres.PlaylistPatch{Visibility: strptr("public")}); !errors.Is(err, ErrSystemPlaylist) {
		t.Fatalf("visibility: err=%v want ErrSystemPlaylist", err)
	}
	if err := svc.DeletePlaylist(ctx, owner, system.ID); !errors.Is(err, ErrSystemPlaylist) {
		t.Fatalf("delete: err=%v want ErrSystemPlaylist", err)
	}
	if err := svc.AddPlaylistItem(ctx, owner, system.ID, uuid.New(), 0); !errors.Is(err, ErrSystemPlaylist) {
		t.Fatalf("add item: err=%v want ErrSystemPlaylist", err)
	}
	if err := svc.RemovePlaylistItem(ctx, owner, system.ID, uuid.New()); !errors.Is(err, ErrSystemPlaylist) {
		t.Fatalf("remove item: err=%v want ErrSystemPlaylist", err)
	}
	// A stranger is still a stranger first (403 before 409).
	if _, err := svc.UpdatePlaylist(ctx, uuid.New(), system.ID, postgres.PlaylistPatch{Title: strptr("x")}); !errors.Is(err, ErrNotPlaylistOwner) {
		t.Fatalf("stranger: err=%v want ErrNotPlaylistOwner", err)
	}
	// A user playlist of the same owner passes the guard (and then panics
	// on the nil pgStore, which is the proof the guard let it through).
	user := &postgres.Playlist{ID: uuid.New(), CreatorID: owner, Kind: postgres.PlaylistKindUser}
	svc2 := newAuthoringService(&fakeAuthoringStore{playlist: user})
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("user playlist edit did not reach the store")
			}
		}()
		_, _ = svc2.UpdatePlaylist(ctx, owner, user.ID, postgres.PlaylistPatch{Title: strptr("ok")})
	}()
}

func TestGetSystemPlaylistCreatesOnFirstRead(t *testing.T) {
	store := newFakeSystemPlaylistStore()
	svc := &Service{systemPlaylists: store}
	owner := uuid.New()
	ctx := context.Background()

	if _, err := svc.GetSystemPlaylist(ctx, owner, "favourites"); !errors.Is(err, ErrInvalidSystemPlaylistKind) {
		t.Fatalf("bad kind: %v", err)
	}
	if _, err := svc.GetSystemPlaylist(ctx, uuid.Nil, postgres.PlaylistKindLiked); !errors.Is(err, ErrNotPlaylistOwner) {
		t.Fatalf("anonymous: %v", err)
	}
	first, err := svc.GetSystemPlaylist(ctx, owner, postgres.PlaylistKindWatchLater)
	if err != nil {
		t.Fatal(err)
	}
	if first.Title != "Queue" || first.Visibility != "private" || first.Kind != postgres.PlaylistKindWatchLater {
		t.Fatalf("first read: %+v", first)
	}
	second, _ := svc.GetSystemPlaylist(ctx, owner, postgres.PlaylistKindWatchLater)
	if second.ID != first.ID {
		t.Fatal("second read created another collection")
	}
	loved, _ := svc.GetSystemPlaylist(ctx, owner, postgres.PlaylistKindLiked)
	if loved.Title != "Loved" {
		t.Fatalf("liked title=%q", loved.Title)
	}
}

func TestLikedCollectionMirrorsLongVideosOnly(t *testing.T) {
	store := newFakeSystemPlaylistStore()
	svc := &Service{systemPlaylists: store}
	ctx := context.Background()
	viewer := uuid.New()
	video := &postgres.Post{ID: uuid.New(), ContentType: "long_video"}
	reel := &postgres.Post{ID: uuid.New(), ContentType: "flick"}

	svc.syncLikedCollection(ctx, viewer, video, true)
	svc.syncLikedCollection(ctx, viewer, reel, true)
	if in, _ := store.IsInSystemPlaylist(ctx, viewer, postgres.PlaylistKindLiked, video.ID); !in {
		t.Fatal("liked long video not in Loved")
	}
	if in, _ := store.IsInSystemPlaylist(ctx, viewer, postgres.PlaylistKindLiked, reel.ID); in {
		t.Fatal("a reel was written to Loved; reels keep /v1/reels/liked")
	}
	if len(store.adds) != 1 {
		t.Fatalf("adds=%v want exactly one", store.adds)
	}
	svc.syncLikedCollection(ctx, viewer, video, false)
	if in, _ := store.IsInSystemPlaylist(ctx, viewer, postgres.PlaylistKindLiked, video.ID); in {
		t.Fatal("unlike did not remove the video from Loved")
	}
	// Unwired: a no-op, never a panic.
	(&Service{}).syncLikedCollection(ctx, viewer, video, true)
}

// Queueing consults the post's visibility BEFORE the collection is touched:
// with no post store behind the gate the request cannot get past it (the
// nil store panics), and the collection store sees nothing. A gate that
// stopped gating would write the row and return quietly.
func TestAddToWatchLaterGatesOnVisibilityFirst(t *testing.T) {
	store := newFakeSystemPlaylistStore()
	svc := &Service{systemPlaylists: store}
	ctx := context.Background()
	viewer, post := uuid.New(), uuid.New()
	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		_, _ = svc.AddToWatchLater(ctx, viewer, post)
	}()
	if !panicked {
		t.Fatal("AddToWatchLater did not consult the post store before writing")
	}
	if len(store.adds) != 0 {
		t.Fatal("the collection was written before the visibility gate answered")
	}
}

func TestRemoveFromWatchLaterIsIdempotent(t *testing.T) {
	store := newFakeSystemPlaylistStore()
	svc := &Service{systemPlaylists: store}
	ctx := context.Background()
	viewer, post := uuid.New(), uuid.New()
	res, err := svc.RemoveFromWatchLater(ctx, viewer, post)
	if err != nil || res.Queued {
		t.Fatalf("remove of never-queued: %+v %v", res, err)
	}
	if _, err := (&Service{}).RemoveFromWatchLater(ctx, viewer, post); !errors.Is(err, ErrAuthoringStoreUnavailable) {
		t.Fatalf("unwired: %v", err)
	}
	if svc.viewerQueued(ctx, viewer, post) {
		t.Fatal("viewer_queued true for an empty queue")
	}
	if _, err := store.AddToSystemPlaylist(ctx, viewer, postgres.PlaylistKindWatchLater, post); err != nil {
		t.Fatal(err)
	}
	if !svc.viewerQueued(ctx, viewer, post) {
		t.Fatal("viewer_queued false after queueing")
	}
}
