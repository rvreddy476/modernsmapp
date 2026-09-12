package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Video series: visibility, membership and removal (2026-09-10).
//
// Three holes closed here, all found by the creator-side authoring UI:
//
//   1. A series could never shrink. There was no delete for an episode and no
//      delete for a series, so a series created by mistake was permanent and
//      an episode added by mistake could only be overwritten.
//   2. One post could occupy two episode numbers, because the table's key is
//      (series_id, episode_num) and nothing looked at post_id. The guard is
//      in the store, inside the insert's transaction — see
//      AddEpisodeToVideoSeries there.
//   3. video_series.is_public was written and never read. A private series
//      answered its full body to any caller, with or without a token.
//
// ── DECISION 1: removing an episode leaves a GAP; it does not renumber ─────
//
// Deleting episode 2 of 3 leaves episodes 1 and 3. Nothing is shifted down.
//
// The episode number is a label the creator chose and the audience has seen:
// it is rendered as "Episode 3", it is what a comment or a chat message
// refers to, and it is the identifier the watch page's next/previous control
// walks. Renumbering on delete would silently rewrite the label of every
// episode after the removed one — episode 3 would become episode 2 for
// everyone who had already watched, linked to or referred to it — and it
// would rewrite rows nobody asked to touch. A gap, by contrast, is visible
// and fixable: the creator re-adds at the free number, or moves an episode
// deliberately by adding it at the number they want.
//
// episode_count is unaffected by the choice: it is COUNT(*) of the episode
// rows, recomputed on both add and delete, NOT max(episode_num). After
// deleting 2 of 3 the count is 2 while the numbers read 1 and 3, which is
// correct — there are two episodes.
//
// ── DECISION 2: a stranger's creator listing OMITS private series ──────────
//
// GET /v1/creators/:creatorId/video-series filters is_public=false rows out
// for everyone except the creator themselves. It does not 403; the list is
// simply shorter.
//
// A listing is a set, not a lookup, so hiding a row leaks nothing — the
// caller never learns that a hidden series exists — and it is what every
// other list-of-someone-else's-things does. Refusing the whole list because
// one row is private would be wrong (the public series are public), and
// returning a placeholder would tell strangers exactly what the private flag
// is there to hide. The single-series read and the episode list DO refuse
// (403), because those name a specific series the caller asked for by id.
//
// ── 403 vs 404 ────────────────────────────────────────────────────────────
//
// Same rule the rest of this surface uses, documented at length in
// video_authoring_authz.go: 404 means "no such row", 403 means "not yours".
// A non-owner therefore learns a series exists. That is deliberate and
// consistent with AddVideoSeriesEpisode, DeletePlaylist and the playlist read
// gate (ErrPlaylistPrivate), which all answer 403 to a caller who is not the
// owner.
var (
	// ErrVideoSeriesNotFound / ErrNotVideoSeriesOwner keep the exact strings
	// AddEpisodeToVideoSeries has always returned, so the wire messages the
	// web client already matches on do not move.
	ErrVideoSeriesNotFound = errors.New("video series not found")
	ErrNotVideoSeriesOwner = errors.New("forbidden: you do not own this video series")
	// ErrVideoSeriesPrivate is the read-side refusal, mirroring
	// ErrPlaylistPrivate.
	ErrVideoSeriesPrivate = errors.New("forbidden: video series is private")
	// ErrVideoSeriesEpisodeNotFound is "that episode is not in this series"
	// — a 404, distinct from a missing series.
	ErrVideoSeriesEpisodeNotFound = errors.New("episode not found in this video series")
	// ErrEpisodePostDuplicate is the 409: the post is already an episode of
	// this series at a different number.
	ErrEpisodePostDuplicate = errors.New("post is already an episode of this series")
	// ErrVideoSeriesStoreUnavailable is the fail-closed answer when the
	// series lookups have no store behind them, matching
	// ErrAuthoringStoreUnavailable.
	ErrVideoSeriesStoreUnavailable = errors.New("video series store not configured")
	// ErrVideoSeriesFull is the 409 for an add that would grow a series past
	// postgres.MaxSeriesEpisodes. Distinct from ErrEpisodePostDuplicate so
	// the client can say "this series is full" rather than "already added".
	ErrVideoSeriesFull = errors.New("video series is full")
	// ErrPostNotInSeries is the ONE answer GET /v1/posts/:postId/series
	// gives for "this post is in no series" AND "this post is in a private
	// series you do not own". See GetPostSeries for why they must not
	// differ.
	ErrPostNotInSeries = errors.New("post is not an episode of a video series")
	// ErrVideoSeriesTitleRequired: create refuses an empty title, so a patch
	// must not be the way to end up with one.
	ErrVideoSeriesTitleRequired = errors.New("title is required")
)

// videoSeriesStore is the slice of the Postgres store these decisions need.
// A narrow interface — rather than the concrete *postgres.Store on
// Service.pgStore — so the visibility, ownership and duplicate rules can be
// tested without a live database, the same way videoAuthoringStore
// (video_authoring_authz.go) and channelStore (channels.go) are.
type videoSeriesStore interface {
	GetVideoSeries(ctx context.Context, id uuid.UUID) (*postgres.VideoSeries, error)
	ListVideoSeriesByCreator(ctx context.Context, creatorID uuid.UUID, limit, offset int) ([]postgres.VideoSeries, error)
	GetVideoSeriesEpisodes(ctx context.Context, seriesID uuid.UUID, includeUnpublished bool) ([]postgres.VideoSeriesEpisode, error)
	AddEpisodeToVideoSeries(ctx context.Context, seriesID, postID uuid.UUID, episodeNum int, title *string) (*postgres.VideoSeriesEpisode, error)
	FindVideoSeriesEpisodeByPost(ctx context.Context, seriesID, postID uuid.UUID) (*postgres.VideoSeriesEpisode, error)
	FindSeriesMembershipsByPost(ctx context.Context, postID uuid.UUID) ([]postgres.VideoSeriesEpisode, error)
	UpdateVideoSeries(ctx context.Context, id uuid.UUID, patch postgres.VideoSeriesPatch) (*postgres.VideoSeries, error)
	DeleteVideoSeries(ctx context.Context, id uuid.UUID) error
	DeleteVideoSeriesEpisodeByNum(ctx context.Context, seriesID uuid.UUID, episodeNum int) (bool, error)
	DeleteVideoSeriesEpisodeByPost(ctx context.Context, seriesID, postID uuid.UUID) (bool, error)
}

// CreateVideoSeries creates a new video series owned by creatorID.
func (s *Service) CreateVideoSeries(ctx context.Context, vs *postgres.VideoSeries) error {
	if vs.Title == "" {
		return fmt.Errorf("title is required")
	}
	return s.pgStore.CreateVideoSeries(ctx, vs)
}

// loadVideoSeries fetches a series, failing closed when there is no store.
func (s *Service) loadVideoSeries(ctx context.Context, id uuid.UUID) (*postgres.VideoSeries, error) {
	if s.videoSeries == nil {
		return nil, ErrVideoSeriesStoreUnavailable
	}
	vs, err := s.videoSeries.GetVideoSeries(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrVideoSeriesNotFound
		}
		return nil, err
	}
	if vs == nil {
		return nil, ErrVideoSeriesNotFound
	}
	return vs, nil
}

// requireVideoSeriesOwner establishes that callerID created seriesID.
func (s *Service) requireVideoSeriesOwner(ctx context.Context, callerID, seriesID uuid.UUID) (*postgres.VideoSeries, error) {
	vs, err := s.loadVideoSeries(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	if callerID == uuid.Nil || vs.CreatorID != callerID {
		return nil, ErrNotVideoSeriesOwner
	}
	return vs, nil
}

// GetVideoSeries retrieves a video series by ID, enforcing is_public: a
// private series is its creator's alone. Pass a nil callerID for
// unauthenticated callers.
func (s *Service) GetVideoSeries(ctx context.Context, id uuid.UUID, callerID *uuid.UUID) (*postgres.VideoSeries, error) {
	vs, err := s.loadVideoSeries(ctx, id)
	if err != nil {
		return nil, err
	}
	if !vs.IsPublic {
		if callerID == nil || *callerID != vs.CreatorID {
			return nil, ErrVideoSeriesPrivate
		}
	}
	return vs, nil
}

// ListVideoSeriesByCreator returns paginated video series for the given
// creator, omitting the creator's private series for every other caller. See
// DECISION 2 at the top of this file.
func (s *Service) ListVideoSeriesByCreator(ctx context.Context, creatorID uuid.UUID, callerID *uuid.UUID, limit, offset int) ([]postgres.VideoSeries, error) {
	if s.videoSeries == nil {
		return nil, ErrVideoSeriesStoreUnavailable
	}
	all, err := s.videoSeries.ListVideoSeriesByCreator(ctx, creatorID, limit, offset)
	if err != nil {
		return nil, err
	}
	if callerID != nil && *callerID == creatorID {
		return all, nil
	}
	return visibleVideoSeries(all), nil
}

// visibleVideoSeries drops the private rows. Pure, so the filter itself is
// testable and cannot quietly become a no-op.
func visibleVideoSeries(all []postgres.VideoSeries) []postgres.VideoSeries {
	out := make([]postgres.VideoSeries, 0, len(all))
	for _, vs := range all {
		if vs.IsPublic {
			out = append(out, vs)
		}
	}
	return out
}

// AddEpisodeToVideoSeries adds an episode after verifying the caller owns the
// series. A post that is already an episode at a DIFFERENT number is refused
// with ErrEpisodePostDuplicate — a series must not offer a "next episode"
// that loops back to the video already playing. Re-adding a post at the
// number it already holds is an update, and still allowed.
func (s *Service) AddEpisodeToVideoSeries(ctx context.Context, callerID, seriesID, postID uuid.UUID, episodeNum int, title *string) (*postgres.VideoSeriesEpisode, error) {
	if _, err := s.requireVideoSeriesOwner(ctx, callerID, seriesID); err != nil {
		return nil, err
	}
	// Asked here as well as guarded in the store's transaction so the error
	// can name the episode the post already holds; the store's WHERE NOT
	// EXISTS is what actually closes the race.
	if existing, err := s.videoSeries.FindVideoSeriesEpisodeByPost(ctx, seriesID, postID); err == nil &&
		existing != nil && existing.EpisodeNum != episodeNum {
		return nil, fmt.Errorf("%w (episode %d)", ErrEpisodePostDuplicate, existing.EpisodeNum)
	}
	ep, err := s.videoSeries.AddEpisodeToVideoSeries(ctx, seriesID, postID, episodeNum, title)
	switch {
	case errors.Is(err, postgres.ErrEpisodePostAlreadyInSeries):
		return nil, ErrEpisodePostDuplicate
	case errors.Is(err, postgres.ErrVideoSeriesFull):
		return nil, ErrVideoSeriesFull
	}
	return ep, err
}

// GetVideoSeriesEpisodes returns the episodes of a video series, behind the
// same is_public gate as the series itself: reading the contents of a private
// series is reading the series, and the two endpoints must not disagree.
// Only the creator sees episodes whose post is deleted or still scheduled.
func (s *Service) GetVideoSeriesEpisodes(ctx context.Context, seriesID uuid.UUID, callerID *uuid.UUID) ([]postgres.VideoSeriesEpisode, error) {
	vs, err := s.GetVideoSeries(ctx, seriesID, callerID)
	if err != nil {
		return nil, err
	}
	return s.videoSeries.GetVideoSeriesEpisodes(ctx, seriesID, isVideoSeriesOwner(vs, callerID))
}

func isVideoSeriesOwner(vs *postgres.VideoSeries, callerID *uuid.UUID) bool {
	return callerID != nil && *callerID == vs.CreatorID
}

// UpdateVideoSeries applies a partial edit after verifying ownership.
func (s *Service) UpdateVideoSeries(ctx context.Context, callerID, seriesID uuid.UUID, patch postgres.VideoSeriesPatch) (*postgres.VideoSeries, error) {
	if patch.Title != nil && *patch.Title == "" {
		return nil, ErrVideoSeriesTitleRequired
	}
	if _, err := s.requireVideoSeriesOwner(ctx, callerID, seriesID); err != nil {
		return nil, err
	}
	vs, err := s.videoSeries.UpdateVideoSeries(ctx, seriesID, patch)
	if err != nil {
		return nil, err
	}
	if vs == nil {
		// Deleted between the ownership read and the update.
		return nil, ErrVideoSeriesNotFound
	}
	return vs, nil
}

// ── the watch page: which series is this post in? ──────────────────────────

// SeriesNeighbour is the previous or next episode as the watch page's
// controls need it: enough to label the button and navigate, nothing more.
type SeriesNeighbour struct {
	PostID     uuid.UUID `json:"post_id"`
	EpisodeNum int       `json:"episode_num"`
	Title      *string   `json:"title"`
}

// SeriesPosition is where the post being watched sits in the series.
type SeriesPosition struct {
	EpisodeNum int `json:"episode_num"`
}

// PostSeriesView is the body of GET /v1/posts/:postId/series. Next and Prev
// carry no omitempty on purpose: the watch page distinguishes "there is no
// next episode" (JSON null, hide the button) from "the field was not sent"
// (an older server, fall back to the episode list), so absence must be
// spelled out as null every time.
type PostSeriesView struct {
	Series   *postgres.VideoSeries         `json:"series"`
	Episodes []postgres.VideoSeriesEpisode `json:"episodes"`
	Current  SeriesPosition                `json:"current"`
	Next     *SeriesNeighbour              `json:"next"`
	Prev     *SeriesNeighbour              `json:"prev"`
}

// GetPostSeries resolves the series a post belongs to and the post's
// neighbours in it, for the watch page. Pass a nil callerID for anonymous
// callers.
//
// Every refusal is ErrPostNotInSeries (a 404), INCLUDING the case where the
// post is in a series that exists but is private to someone else. The
// single-series read answers 403 for that, because there the caller already
// holds the series id. Here the caller holds only a post id, and a 403 would
// tell a stranger "yes, this video is part of a private series", which is
// exactly what is_public is there to hide. A missing series and a hidden one
// must look identical from the outside.
//
// A post can be in more than one series. The memberships come back newest
// first and the first one the caller may see wins: if the newest is private
// and the caller is not the owner, the read moves on to the next rather than
// 404ing on a public series the post is also in.
func (s *Service) GetPostSeries(ctx context.Context, postID uuid.UUID, callerID *uuid.UUID) (*PostSeriesView, error) {
	if s.videoSeries == nil {
		return nil, ErrVideoSeriesStoreUnavailable
	}
	memberships, err := s.videoSeries.FindSeriesMembershipsByPost(ctx, postID)
	if err != nil {
		return nil, err
	}
	for _, m := range memberships {
		vs, err := s.loadVideoSeries(ctx, m.SeriesID)
		if errors.Is(err, ErrVideoSeriesNotFound) {
			// The episode row outlived its series; the FK cascades, so this
			// is a read racing a delete. Try the next membership.
			continue
		}
		if err != nil {
			return nil, err
		}
		owner := isVideoSeriesOwner(vs, callerID)
		if !vs.IsPublic && !owner {
			continue
		}
		episodes, err := s.videoSeries.GetVideoSeriesEpisodes(ctx, vs.ID, owner)
		if err != nil {
			return nil, err
		}
		current, ok := findEpisodeByPost(episodes, postID)
		if !ok {
			// The post's own row was filtered out: it is deleted or not yet
			// live, and the caller is not the creator. To them this video
			// is not part of anything.
			continue
		}
		prev, next := neighbours(episodes, postID)
		if episodes == nil {
			episodes = []postgres.VideoSeriesEpisode{}
		}
		return &PostSeriesView{
			Series:   vs,
			Episodes: episodes,
			Current:  SeriesPosition{EpisodeNum: current.EpisodeNum},
			Next:     next,
			Prev:     prev,
		}, nil
	}
	return nil, ErrPostNotInSeries
}

func findEpisodeByPost(episodes []postgres.VideoSeriesEpisode, postID uuid.UUID) (postgres.VideoSeriesEpisode, bool) {
	for _, ep := range episodes {
		if ep.PostID == postID {
			return ep, true
		}
	}
	return postgres.VideoSeriesEpisode{}, false
}

// neighbours finds the episodes immediately before and after postID by
// episode_num. "Immediately" means the nearest occupied number, not n-1 and
// n+1: removing an episode leaves a gap (DECISION 1), and a next-episode
// button that pointed into the gap would be a dead control. Pure and
// order-independent so it is testable on its own and does not quietly depend
// on the store's ORDER BY.
func neighbours(episodes []postgres.VideoSeriesEpisode, postID uuid.UUID) (prev, next *SeriesNeighbour) {
	current, ok := findEpisodeByPost(episodes, postID)
	if !ok {
		return nil, nil
	}
	var before, after *postgres.VideoSeriesEpisode
	for i := range episodes {
		ep := &episodes[i]
		switch {
		case ep.EpisodeNum < current.EpisodeNum && (before == nil || ep.EpisodeNum > before.EpisodeNum):
			before = ep
		case ep.EpisodeNum > current.EpisodeNum && (after == nil || ep.EpisodeNum < after.EpisodeNum):
			after = ep
		}
	}
	return asNeighbour(before), asNeighbour(after)
}

func asNeighbour(ep *postgres.VideoSeriesEpisode) *SeriesNeighbour {
	if ep == nil {
		return nil
	}
	return &SeriesNeighbour{PostID: ep.PostID, EpisodeNum: ep.EpisodeNum, Title: ep.Title}
}

// DeleteVideoSeries removes a series after verifying ownership. The episode
// rows cascade; the posts do not — an episode row is a link to a video, not
// the video.
func (s *Service) DeleteVideoSeries(ctx context.Context, callerID, seriesID uuid.UUID) error {
	if _, err := s.requireVideoSeriesOwner(ctx, callerID, seriesID); err != nil {
		return err
	}
	return s.videoSeries.DeleteVideoSeries(ctx, seriesID)
}

// DeleteVideoSeriesEpisodeByNum removes the episode at episodeNum. Leaves a
// gap in the numbering by design (DECISION 1); episode_count is recomputed.
func (s *Service) DeleteVideoSeriesEpisodeByNum(ctx context.Context, callerID, seriesID uuid.UUID, episodeNum int) error {
	if _, err := s.requireVideoSeriesOwner(ctx, callerID, seriesID); err != nil {
		return err
	}
	removed, err := s.videoSeries.DeleteVideoSeriesEpisodeByNum(ctx, seriesID, episodeNum)
	if err != nil {
		return err
	}
	if !removed {
		return ErrVideoSeriesEpisodeNotFound
	}
	return nil
}

// DeleteVideoSeriesEpisodeByPost removes whichever episode the post occupies.
// The creator tools hold post ids, so both spellings of the delete exist.
func (s *Service) DeleteVideoSeriesEpisodeByPost(ctx context.Context, callerID, seriesID, postID uuid.UUID) error {
	if _, err := s.requireVideoSeriesOwner(ctx, callerID, seriesID); err != nil {
		return err
	}
	removed, err := s.videoSeries.DeleteVideoSeriesEpisodeByPost(ctx, seriesID, postID)
	if err != nil {
		return err
	}
	if !removed {
		return ErrVideoSeriesEpisodeNotFound
	}
	return nil
}
