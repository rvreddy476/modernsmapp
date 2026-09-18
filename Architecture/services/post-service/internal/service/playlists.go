package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ── Playlist visibility ───────────────────────────────────────────────────
//
// playlists.visibility is NOT the posts.visibility enum. Its CHECK
// (migrations/012_posttube_features.sql) admits exactly
// 'public' | 'unlisted' | 'private', where 'unlisted' means "reachable by
// anyone holding the link" — so only 'private' is owner-only, and that is
// the single rule every playlist read applies: the creator listing, the
// fetch by id, and the items.
//
// The listing and the fetch by id therefore differ, deliberately, on ONE
// value: 'unlisted'. "Unlisted" means reachable with the link but not
// discoverable in a list — the same thing it means for a video — so an
// unlisted playlist answers a direct fetch by id to anyone while staying out
// of the creator listing that a stranger loads on the channel page. Leaving
// it in that listing would make the setting do nothing. 'private' is
// owner-only in both; 'public' is everyone's in both.
const (
	playlistVisibilityPublic   = "public"
	playlistVisibilityUnlisted = "unlisted"
	playlistVisibilityPrivate  = "private"
)

// PlaylistVisibilities are the values the column's CHECK accepts, in the
// order an API error should list them.
var PlaylistVisibilities = []string{playlistVisibilityPublic, playlistVisibilityUnlisted, playlistVisibilityPrivate}

// ErrInvalidPlaylistVisibility is returned instead of letting the column's
// CHECK turn a typo into a 500 with a constraint name in it.
var ErrInvalidPlaylistVisibility = fmt.Errorf("visibility must be one of %s", strings.Join(PlaylistVisibilities, ", "))

func validPlaylistVisibility(v string) bool {
	for _, allowed := range PlaylistVisibilities {
		if v == allowed {
			return true
		}
	}
	return false
}

// playlistReadableBy is the FETCH rule, shared by GET /v1/playlists/:id and
// the items endpoint so a direct fetch can never be a way around the
// listing: public and unlisted answer anyone, private answers its creator.
//
// The leak this replaced was the listing and the fetch disagreeing by
// accident — the listing handed a private playlist's title and item count to
// anyone who knew a user id. They still differ, but now only on 'unlisted'
// and only on purpose; see the visibility note above.
func playlistReadableBy(p *postgres.Playlist, callerID *uuid.UUID) bool {
	if p == nil {
		return false
	}
	if p.Visibility != playlistVisibilityPrivate {
		return true
	}
	return playlistOwnedBy(p, callerID)
}

// playlistListableBy is the LISTING rule: a stranger's view of a creator's
// shelf is the public playlists alone. Unlisted is excluded here and not in
// playlistReadableBy — that difference IS the meaning of the word.
func playlistListableBy(p *postgres.Playlist, callerID *uuid.UUID) bool {
	if p == nil {
		return false
	}
	return p.Visibility == playlistVisibilityPublic || playlistOwnedBy(p, callerID)
}

func playlistOwnedBy(p *postgres.Playlist, callerID *uuid.UUID) bool {
	return callerID != nil && *callerID == p.CreatorID
}

// CreatePlaylist creates a new playlist owned by the creator.
func (s *Service) CreatePlaylist(ctx context.Context, p *postgres.Playlist) error {
	if p.Title == "" {
		return fmt.Errorf("title is required")
	}
	if p.Visibility == "" {
		p.Visibility = playlistVisibilityPublic
	}
	if !validPlaylistVisibility(p.Visibility) {
		return ErrInvalidPlaylistVisibility
	}
	return s.pgStore.CreatePlaylist(ctx, p)
}

// GetPlaylist retrieves a playlist by ID, enforcing visibility: private playlists are
// only visible to their creator. Pass a nil callerID for unauthenticated callers.
func (s *Service) GetPlaylist(ctx context.Context, id uuid.UUID, callerID *uuid.UUID) (*postgres.Playlist, error) {
	// Fail closed: with no store behind the lookup there is no way to know
	// whether this playlist is private, so it is not served.
	if s.authoringOwners == nil {
		return nil, ErrAuthoringStoreUnavailable
	}
	p, err := s.authoringOwners.GetPlaylist(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPlaylistNotFound
		}
		return nil, err
	}
	if p == nil {
		return nil, ErrPlaylistNotFound
	}
	if !playlistReadableBy(p, callerID) {
		return nil, ErrPlaylistPrivate
	}
	return p, nil
}

// ListPlaylistsByCreator returns paginated playlists for the given creator
// under playlistListableBy: the creator sees their whole shelf, everyone
// else sees the public playlists alone. Pass a nil callerID for
// unauthenticated callers.
//
// The filter is pushed into SQL (see the store) so a page stays a full page.
// Fails closed for the same reason GetPlaylist does: with no store behind
// the lookup there is no way to know what any of these rows are.
func (s *Service) ListPlaylistsByCreator(ctx context.Context, creatorID uuid.UUID, callerID *uuid.UUID, limit, offset int) ([]postgres.Playlist, error) {
	if s.authoringOwners == nil {
		return nil, ErrAuthoringStoreUnavailable
	}
	ownerView := callerID != nil && *callerID == creatorID
	return s.authoringOwners.ListPlaylistsByCreator(ctx, creatorID, ownerView, limit, offset)
}

// UpdatePlaylist applies a partial edit (title, description, visibility,
// cover) after verifying the caller owns the playlist — the same rule
// DeletePlaylist and the item writes apply.
func (s *Service) UpdatePlaylist(ctx context.Context, callerID, playlistID uuid.UUID, patch postgres.PlaylistPatch) (*postgres.Playlist, error) {
	if patch.Title != nil && strings.TrimSpace(*patch.Title) == "" {
		return nil, ErrPlaylistTitleRequired
	}
	if patch.Visibility != nil && !validPlaylistVisibility(*patch.Visibility) {
		return nil, ErrInvalidPlaylistVisibility
	}
	if err := s.requirePlaylistOwner(ctx, callerID, playlistID); err != nil {
		return nil, err
	}
	p, err := s.pgStore.UpdatePlaylist(ctx, playlistID, patch)
	if err != nil {
		return nil, err
	}
	if p == nil {
		// Deleted between the ownership read and the update.
		return nil, ErrPlaylistNotFound
	}
	return p, nil
}

// MovePlaylistItem re-positions one post inside a playlist, owner only, and
// returns the playlist's items in their new order. An item that is not in
// the playlist is ErrPlaylistItemNotFound, not a silent no-op.
func (s *Service) MovePlaylistItem(ctx context.Context, callerID, playlistID, postID uuid.UUID, position int) ([]postgres.PlaylistItem, error) {
	if position < 0 {
		return nil, ErrPlaylistPositionInvalid
	}
	if err := s.requirePlaylistOwner(ctx, callerID, playlistID); err != nil {
		return nil, err
	}
	items, err := s.pgStore.MovePlaylistItem(ctx, playlistID, postID, position)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPlaylistItemNotFound
		}
		return nil, err
	}
	return items, nil
}

// DeletePlaylist removes a playlist after verifying ownership.
func (s *Service) DeletePlaylist(ctx context.Context, callerID, playlistID uuid.UUID) error {
	if err := s.requirePlaylistOwner(ctx, callerID, playlistID); err != nil {
		return err
	}
	return s.pgStore.DeletePlaylist(ctx, playlistID)
}

// AddPlaylistItem adds a post to a playlist after verifying the caller owns
// the playlist — the same rule DeletePlaylist applies. Editing a playlist is
// editing the playlist, whoever authored the post being added.
func (s *Service) AddPlaylistItem(ctx context.Context, callerID, playlistID, postID uuid.UUID, position int) error {
	if err := s.requirePlaylistOwner(ctx, callerID, playlistID); err != nil {
		return err
	}
	return s.pgStore.AddPlaylistItem(ctx, playlistID, postID, position)
}

// RemovePlaylistItem removes a post from a playlist after verifying the
// caller owns the playlist.
func (s *Service) RemovePlaylistItem(ctx context.Context, callerID, playlistID, postID uuid.UUID) error {
	if err := s.requirePlaylistOwner(ctx, callerID, playlistID); err != nil {
		return err
	}
	return s.pgStore.RemovePlaylistItem(ctx, playlistID, postID)
}

// GetPlaylistItems returns all items in a playlist ordered by position,
// enforcing the SAME visibility rule GetPlaylist applies: a private playlist
// is readable only by its creator. Reading the contents of a private playlist
// is reading the playlist; the two endpoints must not disagree. Pass a nil
// callerID for unauthenticated callers.
func (s *Service) GetPlaylistItems(ctx context.Context, playlistID uuid.UUID, callerID *uuid.UUID) ([]postgres.PlaylistItem, error) {
	if _, err := s.GetPlaylist(ctx, playlistID, callerID); err != nil {
		return nil, err
	}
	return s.authoringOwners.GetPlaylistItems(ctx, playlistID)
}

// PlaylistItemDetail is a playlist row with the post it points at already
// drawn. The pointer fields are the PlaylistItem's own, kept verbatim and at
// the top level, so everything that reads playlist_id / post_id / position /
// added_at today keeps reading them; Post is additive.
//
// Post is nil for a row whose post the caller may not see (deleted between
// the two reads, moderated out, still processing, a private account) — the
// same drops GetPostsByIDs makes for POST /v1/posts/batch, which is what
// every client has had to call after this endpoint until now. The row is
// kept rather than removed so a client can tell "gone" from "never there".
type PlaylistItemDetail struct {
	postgres.PlaylistItem
	Post *PostDetail `json:"post"`
}

// GetPlaylistItemsHydrated is GetPlaylistItems with the posts attached,
// through the SAME helper POST /v1/posts/batch uses — so a playlist row and
// a batch row are the same object, down to the viewer state.
func (s *Service) GetPlaylistItemsHydrated(ctx context.Context, playlistID uuid.UUID, callerID *uuid.UUID) ([]PlaylistItemDetail, error) {
	items, err := s.GetPlaylistItems(ctx, playlistID, callerID)
	if err != nil {
		return nil, err
	}
	out := make([]PlaylistItemDetail, 0, len(items))
	if len(items) == 0 {
		return out, nil
	}
	ids := make([]uuid.UUID, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.PostID)
	}
	byID, err := s.GetPostsByIDs(ctx, ids, callerID)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		out = append(out, PlaylistItemDetail{PlaylistItem: it, Post: byID[it.PostID]})
	}
	return out, nil
}
