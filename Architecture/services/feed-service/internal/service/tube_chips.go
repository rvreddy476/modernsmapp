package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// MTube list narrowings (2026-09-27): `sort` and `chip` on /v1/feed/videos,
// `chip` on /v1/feed/videos/:postId/related.
//
// A chip is a viewer-relative filter over the rows a surface would have
// produced anyway — it never adds a source. `fresh` keeps what was
// published in the last seven days; `seen` keeps what is in the viewer's
// watch history (post-service, GET /v1/videos/history); `new_to_you` keeps
// only authors the viewer has never watched and does not follow. A sort
// reorders the assembled page: `popular` by the hydrated view count
// (analytics-service's display number, the one the creator is paid on),
// `recent` leaves the surface's own order — ranked, or chronological on the
// Subscriptions tab — exactly as it was before the parameter existed.
//
// The viewer-specific sets (history, follows) are resolved ONCE per request
// and fail closed: an unresolved history under `seen` or `new_to_you` is an
// error, never an unfiltered page under a heading that promised a filter.
// `fresh` needs nothing from any upstream.
//
// Validation is syntactic and lives here so the handler can refuse an
// unknown value with a 400 before any service is touched; the handler
// tests build the handler with a nil service to pin exactly that.

const (
	TubeSortRecent  = "recent"
	TubeSortPopular = "popular"

	TubeChipFresh    = "fresh"
	TubeChipSeen     = "seen"
	TubeChipNewToYou = "new_to_you"

	// RelatedChipTopic is the `topic:<slug>` chip on the related surface:
	// only videos in that category (the post-service taxonomy id).
	RelatedChipTopic = "topic"
)

// freshWindow is how far back `fresh` reaches.
const freshWindow = 7 * 24 * time.Hour

// NormalizeTubeSort returns the canonical sort for a `sort` query value.
// Empty means the surface's own order (recent). Case and surrounding
// whitespace carry no intent; anything else unknown is refused (false).
func NormalizeTubeSort(raw string) (string, bool) {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "", TubeSortRecent:
		return TubeSortRecent, true
	case TubeSortPopular:
		return TubeSortPopular, true
	}
	return "", false
}

// NormalizeTubeChip returns the canonical chip for a `chip` query value on
// /v1/feed/videos. Empty means no chip and is fine.
func NormalizeTubeChip(raw string) (string, bool) {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "":
		return "", true
	case TubeChipFresh, TubeChipSeen, TubeChipNewToYou:
		return v, true
	}
	return "", false
}

// RelatedChip is the resolved `chip` on the related surface. Kind is one
// of "", fresh, seen, topic; Topic is the category slug when Kind is topic.
type RelatedChip struct {
	Kind  string
	Topic string
}

// topicSlugRe is the shape of a `topic:<slug>` value: lowercase letters,
// digits and '-', 2–40 runes.
var topicSlugRe = regexp.MustCompile(`^[a-z0-9-]{2,40}$`)

// NormalizeRelatedChip parses the related surface's `chip`: fresh, seen,
// or topic:<slug>. new_to_you is deliberately NOT accepted here — an
// up-next list is anchored on one creator's video, and "authors you have
// never watched" has no honest meaning under it.
func NormalizeRelatedChip(raw string) (RelatedChip, bool) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return RelatedChip{}, true
	}
	if topic, ok := strings.CutPrefix(v, RelatedChipTopic+":"); ok {
		topic = strings.ToLower(strings.TrimSpace(topic))
		if !topicSlugRe.MatchString(topic) {
			return RelatedChip{}, false
		}
		return RelatedChip{Kind: RelatedChipTopic, Topic: topic}, true
	}
	switch strings.ToLower(v) {
	case TubeChipFresh, TubeChipSeen:
		return RelatedChip{Kind: strings.ToLower(v)}, true
	}
	return RelatedChip{}, false
}

// TubeFilter is the resolved narrowing for one /v1/feed/videos request:
// the category filter (category.go), a chip, and a sort. The zero value
// is the unnarrowed surface, which takes the original fast path.
type TubeFilter struct {
	Category string
	Chip     string
	Sort     string
}

// IsZero reports whether the request asked for nothing the plain surface
// does not already do. `sort=recent` is the plain surface.
func (f TubeFilter) IsZero() bool {
	return f.Category == "" && f.Chip == "" && (f.Sort == "" || f.Sort == TubeSortRecent)
}

// chipScope is what a chip needs from the viewer, resolved once per
// request. now is fixed at resolution so every window of one request
// agrees on what "the last seven days" means.
type chipScope struct {
	now time.Time
	// seenPosts: the viewer's watch history, by post (chip=seen).
	seenPosts map[uuid.UUID]struct{}
	// excludedAuthors: authors the viewer has watched or follows
	// (chip=new_to_you).
	excludedAuthors map[uuid.UUID]struct{}
}

// keep reports whether one candidate survives the chip. Pure.
func (sc *chipScope) keep(chip string, it FeedItem) bool {
	switch chip {
	case TubeChipFresh:
		return !it.CreatedAt.Before(sc.now.Add(-freshWindow))
	case TubeChipSeen:
		_, seen := sc.seenPosts[it.PostID]
		return seen
	case TubeChipNewToYou:
		_, excluded := sc.excludedAuthors[it.AuthorID]
		return !excluded
	}
	return true
}

// filterItemsByChip keeps the candidates that survive the chip, in order.
// Pure; with no chip the input is returned unchanged.
func filterItemsByChip(items []FeedItem, chip string, sc *chipScope) []FeedItem {
	if chip == "" || sc == nil {
		return items
	}
	out := make([]FeedItem, 0, len(items))
	for _, it := range items {
		if sc.keep(chip, it) {
			out = append(out, it)
		}
	}
	return out
}

// resolveChipScope fetches what the chip needs, failing closed. A chip
// that needs nothing (fresh, or none) costs no upstream call.
func (s *Service) resolveChipScope(ctx context.Context, viewerID uuid.UUID, chip string) (*chipScope, error) {
	sc := &chipScope{now: time.Now()}
	switch chip {
	case TubeChipSeen:
		history, err := s.fetchWatchHistory(ctx, viewerID)
		if err != nil {
			return nil, fmt.Errorf("chip %s: watch history could not be resolved: %w", chip, err)
		}
		sc.seenPosts = make(map[uuid.UUID]struct{}, len(history))
		for _, row := range history {
			sc.seenPosts[row.PostID] = struct{}{}
		}
	case TubeChipNewToYou:
		history, err := s.fetchWatchHistory(ctx, viewerID)
		if err != nil {
			return nil, fmt.Errorf("chip %s: watch history could not be resolved: %w", chip, err)
		}
		following, err := s.fetchFollowing(ctx, viewerID)
		if err != nil {
			return nil, fmt.Errorf("chip %s: follow graph could not be resolved: %w", chip, err)
		}
		sc.excludedAuthors = make(map[uuid.UUID]struct{}, len(history)+len(following))
		for _, row := range history {
			if row.AuthorID != uuid.Nil {
				sc.excludedAuthors[row.AuthorID] = struct{}{}
			}
		}
		for _, id := range following {
			sc.excludedAuthors[id] = struct{}{}
		}
	}
	return sc, nil
}

// watchHistoryRow is one row of the viewer's watch history as this
// service needs it: the post and its author.
type watchHistoryRow struct {
	PostID   uuid.UUID
	AuthorID uuid.UUID
}

const (
	// watchHistoryPageSize is post-service's page ceiling for
	// GET /v1/videos/history.
	watchHistoryPageSize = 100
	// watchHistoryMaxPages bounds the walk: five pages, 500 rows, most
	// recent first. A history longer than that is cut, which for `seen`
	// means the oldest rows are treated as unseen and for `new_to_you`
	// as unwatched — the honest failure, since both err towards showing
	// the viewer more rather than less, and neither is a safety filter.
	watchHistoryMaxPages = 5
)

// fetchWatchHistory reads the viewer's watch history through post-service
// (GET /v1/videos/history, evaluated AS the viewer with the internal key,
// the same way every other post-service read here is made). Rows whose
// post is gone are already dropped by post-service.
func (s *Service) fetchWatchHistory(ctx context.Context, viewerID uuid.UUID) ([]watchHistoryRow, error) {
	var out []watchHistoryRow
	cursor := ""
	for page := 0; page < watchHistoryMaxPages; page++ {
		url := fmt.Sprintf("%s/v1/videos/history?limit=%d", s.postServiceURL, watchHistoryPageSize)
		if cursor != "" {
			url += "&cursor=" + neturl.QueryEscape(cursor)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Internal-Service-Key", os.Getenv("INTERNAL_SERVICE_KEY"))
		req.Header.Set("X-User-Id", viewerID.String())

		resp, err := s.postClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("post-service request failed: %w", err)
		}
		var envelope struct {
			Data []struct {
				PostID string `json:"post_id"`
				Post   *struct {
					ID       string `json:"id"`
					AuthorID string `json:"author_id"`
				} `json:"post"`
			} `json:"data"`
			Meta *struct {
				NextCursor string `json:"next_cursor"`
			} `json:"meta"`
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
			resp.Body.Close()
			return nil, fmt.Errorf("post-service returned %d: %s", resp.StatusCode, string(body))
		}
		err = json.NewDecoder(resp.Body).Decode(&envelope)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("decode watch history: %w", err)
		}
		for _, row := range envelope.Data {
			idStr := row.PostID
			if idStr == "" && row.Post != nil {
				idStr = row.Post.ID
			}
			postID, err := uuid.Parse(idStr)
			if err != nil {
				continue
			}
			r := watchHistoryRow{PostID: postID}
			if row.Post != nil {
				if aid, err := uuid.Parse(row.Post.AuthorID); err == nil {
					r.AuthorID = aid
				}
			}
			out = append(out, r)
		}
		if envelope.Meta == nil || envelope.Meta.NextCursor == "" || len(envelope.Data) == 0 {
			break
		}
		cursor = envelope.Meta.NextCursor
	}
	return out, nil
}

// sortHydratedByViews orders a page by view_count, highest first, keeping
// the incoming order among equals so a tie does not shuffle between
// requests. Pure.
func sortHydratedByViews(posts []HydratedPost) []HydratedPost {
	if len(posts) < 2 {
		return posts
	}
	out := make([]HydratedPost, len(posts))
	copy(out, posts)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ViewCount > out[j].ViewCount
	})
	return out
}
