package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Ownership on the video-authoring writes (2026-09-10).
//
// Every one of these endpoints used to take a post id or a playlist id and no
// caller at all, so any authenticated account could overwrite any creator's
// cards, end screens or chapters — a full replace, so a single call wiped the
// set — or edit any playlist.
//
// The fake below stands in for the Postgres store (videoAuthoringStore), the
// same way privacy_gate_test.go fakes hiddenAuthorsStore: these assertions are
// about the decision, and must not need a live database to run.

type fakeAuthoringStore struct {
	authorID    uuid.UUID
	authorErr   error
	playlist    *postgres.Playlist
	playlistErr error
	items       []postgres.PlaylistItem
	meta        *postgres.VideoMetadata

	authorCalls   int
	playlistCalls int
}

func (f *fakeAuthoringStore) GetPostAuthorID(_ context.Context, _ uuid.UUID) (uuid.UUID, error) {
	f.authorCalls++
	return f.authorID, f.authorErr
}

func (f *fakeAuthoringStore) GetPlaylist(_ context.Context, _ uuid.UUID) (*postgres.Playlist, error) {
	f.playlistCalls++
	return f.playlist, f.playlistErr
}

func (f *fakeAuthoringStore) GetPlaylistItems(_ context.Context, _ uuid.UUID) ([]postgres.PlaylistItem, error) {
	return f.items, nil
}

func (f *fakeAuthoringStore) GetVideoMetadata(_ context.Context, _ uuid.UUID) (*postgres.VideoMetadata, error) {
	return f.meta, nil
}

// newAuthoringService builds a Service whose ONLY wiring is the ownership
// lookup. pgStore is deliberately nil: if a refusal ever stopped refusing,
// the write would reach the nil store and panic, so these tests cannot pass
// by accident.
func newAuthoringService(store videoAuthoringStore) *Service {
	s := &Service{}
	s.authoringOwners = store
	return s
}

func strptr(s string) *string { return &s }

// ── cards / end screens / chapters: the post's author, and nobody else ──────

func TestPostAuthoringWritesRefuseNonAuthor(t *testing.T) {
	owner := uuid.New()
	attacker := uuid.New()
	postID := uuid.New()

	cases := []struct {
		name string
		call func(s *Service) error
	}{
		{"SaveVideoCards", func(s *Service) error {
			return s.SaveVideoCards(context.Background(), attacker, postID,
				[]postgres.VideoCard{{PostID: postID, Type: "video", Title: "pwned"}})
		}},
		{"SaveEndScreens", func(s *Service) error {
			return s.SaveEndScreens(context.Background(), attacker, postID,
				[]postgres.EndScreen{{PostID: postID, Type: "video"}})
		}},
		{"SaveChapters", func(s *Service) error {
			return s.SaveChapters(context.Background(), attacker, postID,
				[]postgres.MediaChapter{{PostID: postID, Title: "pwned"}})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeAuthoringStore{authorID: owner}
			err := tc.call(newAuthoringService(store))
			if !errors.Is(err, ErrNotPostAuthor) {
				t.Fatalf("%s by a non-author: err=%v want ErrNotPostAuthor", tc.name, err)
			}
			if store.authorCalls != 1 {
				t.Fatalf("%s: author lookup ran %d times, want exactly 1", tc.name, store.authorCalls)
			}
		})
	}
}

// A missing (or soft-deleted) post is a 404, not an accidental allow. This is
// the fail-closed half: GetPostAuthorID answers pgx.ErrNoRows and the write
// must stop there.
func TestPostAuthoringWritesRefuseMissingPost(t *testing.T) {
	store := &fakeAuthoringStore{authorErr: pgx.ErrNoRows}
	err := newAuthoringService(store).SaveVideoCards(context.Background(), uuid.New(), uuid.New(),
		[]postgres.VideoCard{{Type: "video", Title: "x"}})
	if !errors.Is(err, ErrPostNotFound) {
		t.Fatalf("missing post: err=%v want ErrPostNotFound", err)
	}
}

// A lookup that errors is a refusal too — never a fall-through to allowed.
func TestPostAuthoringWritesFailClosedOnLookupError(t *testing.T) {
	boom := errors.New("connection reset")
	store := &fakeAuthoringStore{authorErr: boom}
	err := newAuthoringService(store).SaveChapters(context.Background(), uuid.New(), uuid.New(),
		[]postgres.MediaChapter{{Title: "x"}})
	if err == nil {
		t.Fatal("lookup error: write was allowed; want a refusal")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("lookup error: err=%v want it to wrap %v", err, boom)
	}
}

// An unwired ownership store refuses rather than writing unchecked.
func TestPostAuthoringWritesFailClosedWithoutStore(t *testing.T) {
	err := (&Service{}).SaveEndScreens(context.Background(), uuid.New(), uuid.New(), nil)
	if !errors.Is(err, ErrAuthoringStoreUnavailable) {
		t.Fatalf("no ownership store: err=%v want ErrAuthoringStoreUnavailable", err)
	}
}

// ── playlist items: the playlist's creator, and nobody else ─────────────────

func TestPlaylistItemWritesRefuseNonOwner(t *testing.T) {
	owner := uuid.New()
	attacker := uuid.New()
	playlistID := uuid.New()
	postID := uuid.New()

	cases := []struct {
		name string
		call func(s *Service) error
	}{
		{"AddPlaylistItem", func(s *Service) error {
			return s.AddPlaylistItem(context.Background(), attacker, playlistID, postID, 0)
		}},
		{"RemovePlaylistItem", func(s *Service) error {
			return s.RemovePlaylistItem(context.Background(), attacker, playlistID, postID)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeAuthoringStore{playlist: &postgres.Playlist{
				ID: playlistID, CreatorID: owner, Visibility: "public",
			}}
			if err := tc.call(newAuthoringService(store)); !errors.Is(err, ErrNotPlaylistOwner) {
				t.Fatalf("%s by a non-owner: err=%v want ErrNotPlaylistOwner", tc.name, err)
			}
		})
	}
}

func TestPlaylistItemWritesRefuseMissingPlaylist(t *testing.T) {
	store := &fakeAuthoringStore{playlist: nil}
	err := newAuthoringService(store).AddPlaylistItem(context.Background(), uuid.New(), uuid.New(), uuid.New(), 0)
	if !errors.Is(err, ErrPlaylistNotFound) {
		t.Fatalf("missing playlist: err=%v want ErrPlaylistNotFound", err)
	}
}

// ── GET /v1/playlists/:id/items: the same visibility rule as GetPlaylist ────

func TestGetPlaylistItemsHidesPrivatePlaylist(t *testing.T) {
	owner := uuid.New()
	stranger := uuid.New()
	playlistID := uuid.New()
	private := &postgres.Playlist{ID: playlistID, CreatorID: owner, Visibility: "private"}
	items := []postgres.PlaylistItem{{PlaylistID: playlistID, PostID: uuid.New(), Position: 0}}

	t.Run("stranger", func(t *testing.T) {
		s := newAuthoringService(&fakeAuthoringStore{playlist: private, items: items})
		got, err := s.GetPlaylistItems(context.Background(), playlistID, &stranger)
		if !errors.Is(err, ErrPlaylistPrivate) {
			t.Fatalf("stranger: err=%v want ErrPlaylistPrivate", err)
		}
		if got != nil {
			t.Fatalf("stranger got %d items of a private playlist; want none", len(got))
		}
	})

	t.Run("anonymous", func(t *testing.T) {
		s := newAuthoringService(&fakeAuthoringStore{playlist: private, items: items})
		got, err := s.GetPlaylistItems(context.Background(), playlistID, nil)
		if !errors.Is(err, ErrPlaylistPrivate) {
			t.Fatalf("anonymous: err=%v want ErrPlaylistPrivate", err)
		}
		if got != nil {
			t.Fatalf("anonymous got %d items of a private playlist; want none", len(got))
		}
	})

	t.Run("owner still reads them", func(t *testing.T) {
		s := newAuthoringService(&fakeAuthoringStore{playlist: private, items: items})
		got, err := s.GetPlaylistItems(context.Background(), playlistID, &owner)
		if err != nil {
			t.Fatalf("owner: unexpected err %v", err)
		}
		if len(got) != len(items) {
			t.Fatalf("owner got %d items, want %d", len(got), len(items))
		}
	})
}

// A public playlist stays public — the fix must not lock viewers out.
func TestGetPlaylistItemsAllowsPublicPlaylistAnonymously(t *testing.T) {
	playlistID := uuid.New()
	items := []postgres.PlaylistItem{{PlaylistID: playlistID, PostID: uuid.New()}}
	s := newAuthoringService(&fakeAuthoringStore{
		playlist: &postgres.Playlist{ID: playlistID, CreatorID: uuid.New(), Visibility: "public"},
		items:    items,
	})
	got, err := s.GetPlaylistItems(context.Background(), playlistID, nil)
	if err != nil || len(got) != 1 {
		t.Fatalf("public playlist anonymously: got %d items, err=%v; want 1 item, no error", len(got), err)
	}
}

// ── GET /v1/videos/:videoId: storage_video_url is the owner's alone ─────────

func TestGetVideoDetailRedactsStorageURLForNonOwner(t *testing.T) {
	owner := uuid.New()
	stranger := uuid.New()
	postID := uuid.New()
	const internalPath = "s3://atpost-media-private/originals/secret.mp4"

	newStore := func() *fakeAuthoringStore {
		return &fakeAuthoringStore{
			authorID: owner,
			meta: &postgres.VideoMetadata{
				PostID:          postID,
				DurationSeconds: 42,
				StorageVideoURL: strptr(internalPath),
				PlaybackURL:     strptr("https://cdn.example.com/hls/x.m3u8"),
			},
		}
	}

	t.Run("anonymous", func(t *testing.T) {
		vm, err := newAuthoringService(newStore()).GetVideoDetailForCaller(context.Background(), postID, nil)
		if err != nil {
			t.Fatalf("unexpected err %v", err)
		}
		if vm.StorageVideoURL != nil {
			t.Fatalf("anonymous caller got storage_video_url=%q; want it redacted", *vm.StorageVideoURL)
		}
		if vm.PlaybackURL == nil {
			t.Fatal("playback_url was redacted too; viewers still need it")
		}
	})

	t.Run("other user", func(t *testing.T) {
		vm, err := newAuthoringService(newStore()).GetVideoDetailForCaller(context.Background(), postID, &stranger)
		if err != nil {
			t.Fatalf("unexpected err %v", err)
		}
		if vm.StorageVideoURL != nil {
			t.Fatalf("non-owner got storage_video_url=%q; want it redacted", *vm.StorageVideoURL)
		}
	})

	t.Run("owner keeps it", func(t *testing.T) {
		vm, err := newAuthoringService(newStore()).GetVideoDetailForCaller(context.Background(), postID, &owner)
		if err != nil {
			t.Fatalf("unexpected err %v", err)
		}
		if vm.StorageVideoURL == nil || *vm.StorageVideoURL != internalPath {
			t.Fatalf("owner lost storage_video_url (%v); the creator tools need it", vm.StorageVideoURL)
		}
	})
}

// Redaction must not depend on the author lookup succeeding.
func TestGetVideoDetailRedactsWhenAuthorLookupFails(t *testing.T) {
	postID := uuid.New()
	caller := uuid.New()
	store := &fakeAuthoringStore{
		authorErr: errors.New("connection reset"),
		meta: &postgres.VideoMetadata{
			PostID:          postID,
			StorageVideoURL: strptr("s3://atpost-media-private/originals/secret.mp4"),
		},
	}
	vm, err := newAuthoringService(store).GetVideoDetailForCaller(context.Background(), postID, &caller)
	if err != nil {
		t.Fatalf("unexpected err %v", err)
	}
	if vm.StorageVideoURL != nil {
		t.Fatalf("author lookup failed and storage_video_url was still served (%q)", *vm.StorageVideoURL)
	}
}
