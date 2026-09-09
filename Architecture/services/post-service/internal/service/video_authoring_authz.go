package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Ownership for the video-authoring writes (2026-09-10).
//
// Cards, end screens and chapters are keyed by post_id alone, and playlist
// items by playlist_id alone, so every one of these writes used to be
// reachable by any authenticated caller who knew an id. All three of the
// post-keyed writes are full REPLACES — the store deletes every row for the
// post and re-inserts what the request carried — so the hole was not "an
// attacker can add a row" but "an attacker can wipe a creator's entire set
// with one call".
//
// The owner is the post's author for cards / end screens / chapters, and the
// playlist's creator for playlist items. That is the same rule
// AddEpisodeToVideoSeries and DeletePlaylist already apply, so the errors
// below carry the same messages and the handlers map them to the same
// statuses.
//
// ── Fail closed ───────────────────────────────────────────────────────────
// If ownership cannot be established the write is refused. A missing post is
// ErrPostNotFound, a lookup error is a wrapped error (500) — never a
// fall-through to "allowed". The store interface being unwired is refused
// too, the way the channel flows refuse an unwired channel store.
//
// ── Existence ─────────────────────────────────────────────────────────────
// A non-owner gets 403, not 404. That is a deliberate choice to match what
// this service already does for the identical shape: AddVideoSeriesEpisode,
// DeletePlaylist, RemoveCrosspost, CreateProductTag and UpdateVideoTrim all
// answer 403 to a caller who is not the owner and reserve 404 for "no such
// row". Post existence is not a secret this endpoint could keep anyway —
// GET /v1/posts/:postId answers it, and so do the sibling GET routes for
// cards, chapters and end screens. Being consistent is worth more here than
// a hardening that the rest of the surface does not implement.

// The post-side refusal is the service's existing ErrNotPostAuthor
// ("forbidden: not the post author", distribution.go) — the same sentinel
// UpdateDistribution already returns and handler.go already maps to 403. One
// error for one meaning; a second spelling of it would be a second thing to
// keep in sync.
var (
	// ErrPlaylistNotFound / ErrNotPlaylistOwner mirror the strings
	// DeletePlaylist has always returned, so the handler's mapping (404 /
	// 403) is one rule for every playlist write.
	ErrPlaylistNotFound = errors.New("playlist not found")
	ErrNotPlaylistOwner = errors.New("forbidden: you do not own this playlist")
	// ErrPlaylistPrivate is the read-side refusal GetPlaylist has always
	// returned; declared here so the items endpoint can return the same one.
	ErrPlaylistPrivate = errors.New("forbidden: playlist is private")
	// ErrAuthoringStoreUnavailable is the fail-closed answer when the
	// ownership lookups have no store behind them.
	ErrAuthoringStoreUnavailable = errors.New("ownership store not configured")
)

// videoAuthoringStore is the slice of the Postgres store the video-authoring
// authorization decisions need. A narrow interface — rather than the concrete
// *postgres.Store already on Service.pgStore — so the fail-closed behaviour
// can be tested without a live database, the same way hiddenAuthorsStore
// (privacy_gate.go) and channelStore (channels.go) are.
type videoAuthoringStore interface {
	GetPostAuthorID(ctx context.Context, postID uuid.UUID) (uuid.UUID, error)
	GetPlaylist(ctx context.Context, id uuid.UUID) (*postgres.Playlist, error)
	GetPlaylistItems(ctx context.Context, playlistID uuid.UUID) ([]postgres.PlaylistItem, error)
	GetVideoMetadata(ctx context.Context, postID uuid.UUID) (*postgres.VideoMetadata, error)
}

// videoAuthoringAuthz is embedded in Service. Declared here so the ownership
// state lives next to the code that owns it.
type videoAuthoringAuthz struct {
	// authoringOwners answers "who owns this post / playlist". Nil when
	// there is no Postgres store; every authoring write then fails closed.
	authoringOwners videoAuthoringStore
}

// requirePostAuthor establishes that callerID authored postID, and refuses if
// it cannot. Returns ErrPostNotFound for a post that does not exist (or was
// soft-deleted — GetPostAuthorID filters those), ErrNotPostAuthor for someone
// else's post, and a wrapped error when the lookup itself failed.
func (s *Service) requirePostAuthor(ctx context.Context, callerID, postID uuid.UUID) error {
	if s.authoringOwners == nil {
		return ErrAuthoringStoreUnavailable
	}
	if callerID == uuid.Nil {
		return ErrNotPostAuthor
	}
	authorID, err := s.authoringOwners.GetPostAuthorID(ctx, postID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrPostNotFound
		}
		return fmt.Errorf("lookup post author: %w", err)
	}
	if authorID != callerID {
		return ErrNotPostAuthor
	}
	return nil
}

// requirePlaylistOwner establishes that callerID created playlistID.
func (s *Service) requirePlaylistOwner(ctx context.Context, callerID, playlistID uuid.UUID) error {
	if s.authoringOwners == nil {
		return ErrAuthoringStoreUnavailable
	}
	if callerID == uuid.Nil {
		return ErrNotPlaylistOwner
	}
	p, err := s.authoringOwners.GetPlaylist(ctx, playlistID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrPlaylistNotFound
		}
		return fmt.Errorf("lookup playlist: %w", err)
	}
	if p == nil {
		return ErrPlaylistNotFound
	}
	if p.CreatorID != callerID {
		return ErrNotPlaylistOwner
	}
	return nil
}
