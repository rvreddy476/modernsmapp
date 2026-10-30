package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
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
	// L4: the cover is the creator's own image on this platform.
	cover, err := s.normalizePlaylistCover(ctx, p.CreatorID, p.CoverURL)
	if err != nil {
		return err
	}
	p.CoverURL = cover
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
	// L1 (tube audit): a playlist is its creator's surface. A viewer the
	// creator blocked, or a stranger to a private account, reads none of
	// it (title, description, items), as for the creator's posts; answered
	// as a missing playlist so it confirms nothing. The end-screen resolver
	// already applied this.
	if !playlistOwnedBy(p, callerID) && !s.canViewAuthor(ctx, callerID, p.CreatorID) {
		return nil, ErrPlaylistNotFound
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
	// L1: nothing of a creator the caller may not view.
	if !ownerView && !s.canViewAuthor(ctx, callerID, creatorID) {
		return []postgres.Playlist{}, nil
	}
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
	if err := s.requireUserPlaylistOwner(ctx, callerID, playlistID); err != nil {
		return nil, err
	}
	// L4: a new cover is the owner's own image on this platform.
	if patch.CoverURL != nil {
		cover, err := s.normalizePlaylistCover(ctx, callerID, patch.CoverURL)
		if err != nil {
			return nil, err
		}
		patch.CoverURL = cover
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
	if err := s.requireUserPlaylistOwner(ctx, callerID, playlistID); err != nil {
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
	if err := s.requireUserPlaylistOwner(ctx, callerID, playlistID); err != nil {
		return err
	}
	return s.pgStore.DeletePlaylist(ctx, playlistID)
}

// playlistItemWriter is the playlist add's store slice (*postgres.Store).
type playlistItemWriter interface {
	AddPlaylistItem(ctx context.Context, playlistID, postID uuid.UUID, position *int, maxItems int) (*postgres.PlaylistItem, error)
}

func (s *Service) playlistItemWrites() playlistItemWriter {
	if s.playlistItemsSeam != nil {
		return s.playlistItemsSeam
	}
	return s.pgStore
}

// AddPlaylistItem adds a post to a playlist after verifying the caller owns
// the playlist — the same rule DeletePlaylist applies. Editing a playlist is
// editing the playlist, whoever authored the post being added.
//
// position nil appends; a position inside the list inserts there and moves
// the rest down. Nothing is overwritten, a post is listed once
// (ErrPlaylistItemExists) and a playlist holds at most
// postgres.MaxPlaylistItems (ErrPlaylistFull): tube audit M1 and M4. The
// row as stored comes back, so the client learns where it landed.
func (s *Service) AddPlaylistItem(ctx context.Context, callerID, playlistID, postID uuid.UUID, position *int) (*postgres.PlaylistItem, error) {
	if position != nil && *position < 0 {
		return nil, ErrPlaylistPositionInvalid
	}
	if err := s.requireUserPlaylistOwner(ctx, callerID, playlistID); err != nil {
		return nil, err
	}
	item, err := s.playlistItemWrites().AddPlaylistItem(ctx, playlistID, postID, position, postgres.MaxPlaylistItems)
	switch {
	case errors.Is(err, postgres.ErrPlaylistItemExists):
		return nil, ErrPlaylistItemExists
	case errors.Is(err, postgres.ErrPlaylistFull):
		return nil, ErrPlaylistFull
	case errors.Is(err, pgx.ErrNoRows):
		// The playlist went between the ownership read and the lock.
		return nil, ErrPlaylistNotFound
	}
	return item, err
}

// RemovePlaylistItem removes a post from a playlist after verifying the
// caller owns the playlist.
func (s *Service) RemovePlaylistItem(ctx context.Context, callerID, playlistID, postID uuid.UUID) error {
	if err := s.requireUserPlaylistOwner(ctx, callerID, playlistID); err != nil {
		return err
	}
	return s.pgStore.RemovePlaylistItem(ctx, playlistID, postID)
}

// DefaultPlaylistItemsPage is the items endpoint's page when the client
// names none (tube audit M4).
const DefaultPlaylistItemsPage = 50

// GetPlaylistItems returns the first page of a playlist's items ordered by
// position, enforcing the SAME visibility rule GetPlaylist applies: a
// private playlist is readable only by its creator. Reading the contents of
// a private playlist is reading the playlist; the two endpoints must not
// disagree. Pass a nil callerID for unauthenticated callers.
func (s *Service) GetPlaylistItems(ctx context.Context, playlistID uuid.UUID, callerID *uuid.UUID) ([]postgres.PlaylistItem, error) {
	return s.GetPlaylistItemsPage(ctx, playlistID, callerID, postgres.PlaylistItemsPage{Limit: DefaultPlaylistItemsPage})
}

// GetPlaylistItemsPage is one keyset page of the items behind GetPlaylist's
// gate (M4). The page is clamped to 1..MaxPlaylistItemsPage.
func (s *Service) GetPlaylistItemsPage(ctx context.Context, playlistID uuid.UUID, callerID *uuid.UUID, page postgres.PlaylistItemsPage) ([]postgres.PlaylistItem, error) {
	if _, err := s.GetPlaylist(ctx, playlistID, callerID); err != nil {
		return nil, err
	}
	page.Limit = clampPlaylistPage(page.Limit)
	return s.authoringOwners.GetPlaylistItems(ctx, playlistID, page)
}

func clampPlaylistPage(limit int) int {
	if limit <= 0 {
		return DefaultPlaylistItemsPage
	}
	if limit > postgres.MaxPlaylistItemsPage {
		return postgres.MaxPlaylistItemsPage
	}
	return limit
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
//
// One page at a time (tube audit M4): the hydration is per page, so a
// playlist of thousands costs one page's batch read, not thousands of rows.
// next is the cursor of the following page, "" on the last one.
func (s *Service) GetPlaylistItemsHydrated(ctx context.Context, playlistID uuid.UUID, callerID *uuid.UUID, page postgres.PlaylistItemsPage) (out []PlaylistItemDetail, next string, err error) {
	page.Limit = clampPlaylistPage(page.Limit)
	items, err := s.GetPlaylistItemsPage(ctx, playlistID, callerID, page)
	if err != nil {
		return nil, "", err
	}
	out = make([]PlaylistItemDetail, 0, len(items))
	if len(items) == 0 {
		return out, "", nil
	}
	if len(items) == page.Limit {
		next = strconv.Itoa(items[len(items)-1].Position)
	}
	ids := make([]uuid.UUID, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.PostID)
	}
	byID, err := s.GetPostsByIDs(ctx, ids, callerID)
	if err != nil {
		return nil, "", err
	}
	for _, it := range items {
		out = append(out, PlaylistItemDetail{PlaylistItem: it, Post: byID[it.PostID]})
	}
	return out, next, nil
}

// ParsePlaylistItemsCursor reads the items endpoint's cursor: the position
// of the last row of the previous page. "" is the first page.
func ParsePlaylistItemsCursor(raw string) (*int, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		return nil, ErrInvalidPlaylistCursor
	}
	return &n, nil
}
