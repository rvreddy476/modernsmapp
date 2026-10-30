package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

var (
	// ErrPlaylistItemExists is the 409 for adding a post the playlist
	// already lists (M1).
	ErrPlaylistItemExists = errors.New("post is already in this playlist")
	// ErrPlaylistFull is the 409 for an add past postgres.MaxPlaylistItems
	// (M4).
	ErrPlaylistFull = fmt.Errorf("playlist is full (at most %d videos)", postgres.MaxPlaylistItems)
	// ErrInvalidPlaylistCursor is the 400 for a cursor this service did not
	// issue (M4).
	ErrInvalidPlaylistCursor = errors.New("invalid cursor")
	// ErrInvalidPlaylistCover is the 400 for a cover_url that is not a
	// media id on this platform (L4).
	ErrInvalidPlaylistCover = errors.New("cover_url must be a media id of your own image (or /v1/media/<id>/serve)")
	// ErrInvalidChapters is the 400 for a chapter list the rules refuse
	// (L2); the message names the rule.
	ErrInvalidChapters = errors.New("invalid chapters")
	// ErrDuplicateChapterIndex is the 400 for two chapters at one index
	// (L2): it used to reach the primary key and come back as a 500.
	ErrDuplicateChapterIndex = errors.New("duplicate chapter_index")
)

// The tube / long-video backend audit's fixes (2026-10-30), the parts that
// are not one file's concern. Each item names the audit finding it closes.

// tubeAuditSeams are test seams; all nil in production.
type tubeAuditSeams struct {
	// viewablePostsSeam stands in for viewablePostAuthors' batch read
	// (M8): the posts the viewer may open, each with its author.
	viewablePostsSeam func(ctx context.Context, ids []uuid.UUID, viewerID *uuid.UUID) (map[uuid.UUID]uuid.UUID, error)
	// playlistItemsSeam stands in for the playlist add's store (M1).
	playlistItemsSeam playlistItemWriter
	// chapterStoreSeam stands in for the chapters store (L2).
	chapterStoreSeam chapterStore
	// watchProgressSeam stands in for the progress write (L3).
	watchProgressSeam watchProgressWriter
}

// viewablePostAuthors is the batch read's decision for a set of post ids
// (M8): the ids the viewer may open, each with its author, in a fixed number
// of round trips (one posts read, one media-state read, one graph lookup
// for the authors, one for the follow edges, one age lookup) instead of a
// full single-post read per id. The drop is batchVisibility, the same one
// POST /v1/posts/batch applies, so it cannot disagree with the feeds; the
// one difference from the single-post gate is that a private post shared
// with the viewer is not listed (a share list is never listed), which is
// the fail-closed side.
//
// An error answers nothing: every id is dropped (fail closed).
func (s *Service) viewablePostAuthors(ctx context.Context, ids []uuid.UUID, viewerID *uuid.UUID) map[uuid.UUID]uuid.UUID {
	out := map[uuid.UUID]uuid.UUID{}
	if len(ids) == 0 {
		return out
	}
	if s.viewablePostsSeam != nil {
		got, err := s.viewablePostsSeam(ctx, ids, viewerID)
		if err != nil {
			return out
		}
		return got
	}
	if s.readGate != nil {
		// Unit rigs that drive the single-post gate: answer through it.
		// The author is unknown there (uuid.Nil).
		for id, ok := range s.viewablePostSet(ctx, ids, viewerID) {
			if ok {
				out[id] = uuid.Nil
			}
		}
		return out
	}
	if s.pgStore == nil {
		return out
	}
	posts, err := s.pgStore.GetPostsByIDs(ctx, uniqueIDs(ids))
	if err != nil {
		return out
	}
	ptrs := make([]*postgres.Post, len(posts))
	for i := range posts {
		ptrs[i] = &posts[i]
	}
	if err := s.attachMediaState(ctx, ptrs); err != nil {
		return out
	}
	visible := s.batchVisibility(ctx, posts, viewerID)
	for i := range posts {
		if visible(&posts[i]) {
			out[posts[i].ID] = posts[i].AuthorID
		}
	}
	return out
}

func uniqueIDs(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// ── platform media references (M3, L2, L4) ─────────────────────────────────

// mediaServePath matches the platform's own media path, as the cards draw
// it: /v1/media/<id>/serve, optionally with a variant (/serve/thumb).
var mediaServePath = regexp.MustCompile(`^/v1/media/([0-9a-fA-F-]{36})/serve(?:/[a-z0-9_-]+)?$`)

// parsePlatformMediaRef reads a media reference a client may store as an
// image: a bare media id, or this platform's /v1/media/<id>/serve path.
// Anything else (an https URL on another host, a tracking pixel) is not one.
func parsePlatformMediaRef(raw string) (uuid.UUID, bool) {
	v := strings.TrimSpace(raw)
	if id, err := uuid.Parse(v); err == nil && len(v) == 36 {
		return id, id != uuid.Nil
	}
	m := mediaServePath.FindStringSubmatch(v)
	if m == nil {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(m[1])
	if err != nil || id == uuid.Nil {
		return uuid.Nil, false
	}
	return id, true
}

// mediaServeURL is how a stored image reference is drawn.
func mediaServeURL(id uuid.UUID) string {
	return "/v1/media/" + id.String() + "/serve"
}

// verifyOwnImage is the cover rule every post cover passes
// (checkCoverAuthority): the asset exists, ownerID uploaded it, its upload
// was confirmed and nothing refused it, and it is an image. A stranger's id
// would otherwise let anyone show their media, or pin it against its
// owner's delete. Fail closed on an unreadable store. The refusal is a
// CoverMediaError, answered with the cover statuses (PostEditErrorStatus).
func (s *Service) verifyOwnImage(ctx context.Context, ownerID, mediaID uuid.UUID) error {
	if s.ownMedia == nil {
		return fmt.Errorf("verify cover media: %w", ErrAuthoringStoreUnavailable)
	}
	ownership, err := s.ownMedia.BatchGetMediaOwnership(ctx, []uuid.UUID{mediaID})
	if err != nil {
		return fmt.Errorf("verify cover media: %w", err)
	}
	m, ok := ownership[mediaID]
	if err := checkCoverAuthority(mediaID, ownerID, m, ok); err != nil {
		return &CoverMediaError{Err: err}
	}
	return nil
}

// verifySeriesCover is M3: a series cover_media_id is the creator's own
// finished image, as a post cover is. It used to accept any id, so a
// stranger's asset could be pinned (the cover's foreign key then blocked
// its owner's delete, until migration 081 made it ON DELETE SET NULL).
func (s *Service) verifySeriesCover(ctx context.Context, creatorID uuid.UUID, cover *uuid.UUID) error {
	if cover == nil {
		return nil
	}
	return s.verifyOwnImage(ctx, creatorID, *cover)
}

// normalizePlaylistCover is L4: a playlist's cover_url is drawn on every
// viewer's end screen, so it must be platform media (the owner's own
// image), never an arbitrary URL that would tell another host who watched
// what. Stored as /v1/media/<id>/serve. nil leaves it unset; an empty
// string is kept (no cover: the first item lends its thumbnail).
func (s *Service) normalizePlaylistCover(ctx context.Context, ownerID uuid.UUID, raw *string) (*string, error) {
	if raw == nil {
		return nil, nil
	}
	if strings.TrimSpace(*raw) == "" {
		empty := ""
		return &empty, nil
	}
	id, ok := parsePlatformMediaRef(*raw)
	if !ok {
		return nil, ErrInvalidPlaylistCover
	}
	if err := s.verifyOwnImage(ctx, ownerID, id); err != nil {
		return nil, err
	}
	out := mediaServeURL(id)
	return &out, nil
}

// ── chapters (L2) ──────────────────────────────────────────────────────────

const (
	// MaxChapters is the most chapters one video carries. Chapters are a
	// navigation aid over the timeline; a hundred is already one every
	// minute and a half of a 2.5-hour video.
	MaxChapters = 100
	// MaxChapterTitleRunes caps a chapter title, in code points like every
	// other text limit here. A chapter title is a label on the scrubber.
	MaxChapterTitleRunes = 100
)

// chapterStore is the chapters' store slice (*postgres.Store).
type chapterStore interface {
	SaveChapters(ctx context.Context, postID uuid.UUID, chapters []postgres.MediaChapter) error
	GetChapters(ctx context.Context, postID uuid.UUID) ([]postgres.MediaChapter, error)
}

func (s *Service) chapterRows() chapterStore {
	if s.chapterStoreSeam != nil {
		return s.chapterStoreSeam
	}
	if s.pgStore == nil {
		return nil
	}
	return s.pgStore
}

// validateChapters is the shape of a chapter list, pure (L2): at most
// MaxChapters; every index at least 0 and used once; every title present
// and at most MaxChapterTitleRunes; start times at least 0 and strictly
// ascending in index order; and, when the video's duration is known
// (durationMs > 0), every start inside it.
func validateChapters(chapters []postgres.MediaChapter, durationMs int) error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidChapters, fmt.Sprintf(format, a...))
	}
	if len(chapters) > MaxChapters {
		return bad("at most %d chapters", MaxChapters)
	}
	ordered := make([]postgres.MediaChapter, len(chapters))
	copy(ordered, chapters)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ChapterIndex < ordered[j].ChapterIndex })
	for i, ch := range ordered {
		if ch.ChapterIndex < 0 {
			return bad("chapter_index must be zero or greater")
		}
		if i > 0 && ch.ChapterIndex == ordered[i-1].ChapterIndex {
			return fmt.Errorf("%w: chapter_index %d is used twice", ErrDuplicateChapterIndex, ch.ChapterIndex)
		}
		title := strings.TrimSpace(ch.Title)
		if title == "" {
			return bad("chapter %d needs a title", ch.ChapterIndex)
		}
		if n := utf8.RuneCountInString(title); n > MaxChapterTitleRunes {
			return bad("chapter %d title is %d characters, limit %d", ch.ChapterIndex, n, MaxChapterTitleRunes)
		}
		if ch.StartMs < 0 {
			return bad("chapter %d start_ms must not be negative", ch.ChapterIndex)
		}
		if i > 0 && ch.StartMs <= ordered[i-1].StartMs {
			return bad("chapter %d must start after chapter %d", ch.ChapterIndex, ordered[i-1].ChapterIndex)
		}
		if durationMs > 0 && ch.StartMs >= durationMs {
			return bad("chapter %d starts at %d ms, past the video's end (%d ms)", ch.ChapterIndex, ch.StartMs, durationMs)
		}
	}
	return nil
}

// chapterVideoDurationMs is the video's duration as the end screens know it
// (0 = unknown, and the upper bound is then not applied).
func (s *Service) chapterVideoDurationMs(ctx context.Context, postID uuid.UUID) (int, error) {
	if s.endScreens == nil {
		return 0, nil
	}
	sub, err := s.endScreens.GetEndScreenSubject(ctx, postID)
	if err != nil {
		return 0, fmt.Errorf("load chapter video: %w", err)
	}
	if sub == nil {
		return 0, ErrPostNotFound
	}
	return sub.DurationMs, nil
}

// normalizeChapterThumbnails holds each thumbnail to the cover rule (L2): a
// media reference to the author's own image, stored as its serve path, or
// absent. thumbnail_url used to take any string, an off-platform tracking
// URL included.
func (s *Service) normalizeChapterThumbnails(ctx context.Context, authorID uuid.UUID, chapters []postgres.MediaChapter) error {
	for i := range chapters {
		raw := chapters[i].ThumbnailURL
		if raw == nil || strings.TrimSpace(*raw) == "" {
			chapters[i].ThumbnailURL = nil
			continue
		}
		id, ok := parsePlatformMediaRef(*raw)
		if !ok {
			return fmt.Errorf("%w: chapter %d thumbnail_url must be a media id of your own image",
				ErrInvalidChapters, chapters[i].ChapterIndex)
		}
		if err := s.verifyOwnImage(ctx, authorID, id); err != nil {
			return err
		}
		u := mediaServeURL(id)
		chapters[i].ThumbnailURL = &u
	}
	return nil
}

// ── view counts on a page (M7) ─────────────────────────────────────────────

// viewCountPageDeadline is the one deadline a page's view counts get, in
// total. fetchViewCounts asks analytics-service once per 200 ids, each call
// under viewCountTimeout; this bounds them all together, so a page never
// waits longer than this on analytics whatever its size.
const viewCountPageDeadline = 2 * time.Second

// pageViewCounts is the view count of every id on a page: one batched read
// (fetchViewCounts) under one total deadline. Fail-open like every view
// count: an id analytics did not answer in time is 0.
func (s *Service) pageViewCounts(ctx context.Context, ids []uuid.UUID) map[uuid.UUID]int64 {
	dctx, cancel := context.WithTimeout(ctx, viewCountPageDeadline)
	defer cancel()
	return s.fetchViewCounts(dctx, ids)
}

// attachPageViewCounts sets ViewCount on every row of a page from one
// pageViewCounts read.
func (s *Service) attachPageViewCounts(ctx context.Context, rows []*PostDetail) {
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		if r != nil && r.Post != nil {
			ids = append(ids, r.Post.ID)
		}
	}
	views := s.pageViewCounts(ctx, ids)
	for _, r := range rows {
		if r != nil && r.Post != nil {
			r.ViewCount = views[r.Post.ID]
		}
	}
}
