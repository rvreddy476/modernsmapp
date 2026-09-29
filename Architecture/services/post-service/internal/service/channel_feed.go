package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

/*
	Channel feed (RSS publishing, 2026-09-29).

	GET /v1/channels/:ref/feed?category=&limit= is the JSON the web app turns
	into https://…/posttube/channel/<handle>/feed.xml. Podcast apps and news
	readers fetch that file with no account, and shared caches keep it, so
	the feed is ONE document for everyone:

	  * It is evaluated as the anonymous stranger. ChannelFeed takes no
	    viewer at all, so no caller can make it depend on one; the handler
	    never reads X-User-Id.
	  * A row is in the feed only if the signed-out playback rule
	    (anonymousMayAccessPost, media_access.go) admits it: public, approved
	    and unrestricted, live, not scheduled, not 18+, not members-only, by
	    an account a stranger may see. The SQL applies the rule
	    (postgres.channelFeedWhere) and every returned row is judged again
	    here, so a predicate lost in a refactor cannot publish a row.
	  * A channel whose owner is private or hidden (deactivated, pending
	    deletion) answers ErrChannelNotFound, the same error as no channel.
	  * An item is listed only with a playable enclosure: a video asset that
	    is ready and passed, whose file size media-service could tell us. An
	    entry with no enclosure is worse than none in a podcast app.

	Enclosure paths are gateway-relative (/v1/media/<id>/serve/720p); the web
	makes them absolute. The bytes are still authorized by media-service on
	every request, which asks this service the anonymous question again.
*/

const (
	// DefaultChannelFeedLimit and MaxChannelFeedLimit are the same number:
	// the feed is the newest 50, and a caller may only ask for fewer.
	DefaultChannelFeedLimit = postgres.MaxChannelFeedVideos
	MaxChannelFeedLimit     = postgres.MaxChannelFeedVideos

	// channelFeedMediaConcurrency bounds the media-record reads in flight.
	channelFeedMediaConcurrency = 8
	// channelFeedMediaTimeout bounds ONE media-record read.
	channelFeedMediaTimeout = 3 * time.Second
	// channelFeedResolveBudget bounds the whole enclosure phase, so a slow
	// media-service costs a request two timeouts, not one per wave of 8.
	// Lookups that have not answered by then are omitted like any failure.
	channelFeedResolveBudget = 2 * channelFeedMediaTimeout
	// channelFeedEnclosureTTL is how long a resolved enclosure is reused.
	channelFeedEnclosureTTL = 10 * time.Minute
	// channelFeedEnclosureCacheLimit bounds the in-process cache.
	channelFeedEnclosureCacheLimit = 10000

	feedVariantOriginal = "original"
	feedDefaultMime     = "video/mp4"
)

// feedEnclosureVariants is the preference order for the file a podcast app
// downloads: the broadly playable MP4 renditions, best first. The author's
// original is the last resort (pickFeedEnclosure).
var feedEnclosureVariants = []string{"720p", "480p"}

// channelFeedStore is the storage slice the feed reads
// (store/postgres/channels.go).
type channelFeedStore interface {
	ListChannelFeedVideos(ctx context.Context, ownerID uuid.UUID, category string, limit int) ([]postgres.ChannelFeedVideo, error)
	ChannelFeedFacts(ctx context.Context, ownerID uuid.UUID) (postgres.ChannelFeedFacts, error)
}

// FeedMediaVariant is one rendition on a media record.
type FeedMediaVariant struct {
	Name      string `json:"variant"`
	Mime      string `json:"mime"`
	SizeBytes *int64 `json:"size_bytes"`
	ObjectKey string `json:"object_key"`
}

// FeedMediaRecord is the part of media-service's GET /v1/media/:id the feed
// reads. Storage coordinates on that record are never decoded further than
// "is there an object behind this rendition".
type FeedMediaRecord struct {
	FileType         string             `json:"file_type"`
	MimeType         string             `json:"mime_type"`
	FileSizeBytes    int64              `json:"file_size_bytes"`
	ProcessingStatus string             `json:"processing_status"`
	ModerationStatus string             `json:"moderation_status"`
	Variants         []FeedMediaVariant `json:"variants"`
}

// feedMediaSource reads one media record. Production is the HTTP client
// below; tests substitute a fake.
type feedMediaSource interface {
	MediaRecord(ctx context.Context, mediaID uuid.UUID) (*FeedMediaRecord, error)
}

// errFeedMediaUnavailable: the record could not be read (not configured,
// transport, non-200, malformed). The item is omitted; nothing is cached.
var errFeedMediaUnavailable = errors.New("channel feed: media record unavailable")

// ChannelFeed is the feed document (the `data` member of the envelope).
type ChannelFeed struct {
	Channel ChannelFeedChannel `json:"channel"`
	// Category echoes the normalized ?category= filter; "" when unfiltered.
	Category string `json:"category"`
	// UpdatedAt is the newest of the channel's own update and the listed
	// items' publication and last edit.
	UpdatedAt time.Time         `json:"updated_at"`
	Items     []ChannelFeedItem `json:"items"`
}

// ChannelFeedChannel is the channel as the feed describes it.
type ChannelFeedChannel struct {
	UserID        uuid.UUID  `json:"user_id"`
	Name          string     `json:"name"`
	Handle        string     `json:"handle"`
	About         string     `json:"about"`
	AvatarMediaID *uuid.UUID `json:"avatar_media_id"`
	// AvatarURL is the gateway-relative original of the avatar; null when
	// the channel has none. A channel avatar is public to every viewer.
	AvatarURL        *string `json:"avatar_url"`
	ContactEmail     string  `json:"contact_email"`
	Language         string  `json:"language"`
	DominantCategory string  `json:"dominant_category"`
}

// ChannelFeedItem is one video in the feed.
type ChannelFeedItem struct {
	ID           uuid.UUID            `json:"id"`
	Title        string               `json:"title"`
	Text         string               `json:"text"`
	Category     string               `json:"category"`
	Language     string               `json:"language"`
	Hashtags     []string             `json:"hashtags"`
	PublishedAt  time.Time            `json:"published_at"`
	MediaID      uuid.UUID            `json:"media_id"`
	DurationMs   int                  `json:"duration_ms"`
	CoverMediaID *uuid.UUID           `json:"cover_media_id"`
	Enclosure    ChannelFeedEnclosure `json:"enclosure"`
}

// ChannelFeedEnclosure is the file a podcast app downloads.
type ChannelFeedEnclosure struct {
	// Variant is "720p", "480p" or "original".
	Variant string `json:"variant"`
	// Path is gateway-relative: /v1/media/<id>/serve/<variant>, or
	// /v1/media/<id>/serve for the original.
	Path      string `json:"path"`
	Mime      string `json:"mime"`
	SizeBytes int64  `json:"size_bytes"`
}

// NormalizeChannelFeedLimit resolves ?limit=: absent or 0 is the default,
// anything above the ceiling is the ceiling.
func NormalizeChannelFeedLimit(limit int) int {
	if limit <= 0 {
		return DefaultChannelFeedLimit
	}
	if limit > MaxChannelFeedLimit {
		return MaxChannelFeedLimit
	}
	return limit
}

// NormalizeChannelFeedCategory canonicalises ?category= and holds it to the
// long-video taxonomy (categories.go): ErrInvalidCategory for anything
// else. Empty means the whole channel.
func NormalizeChannelFeedCategory(raw string) (string, error) {
	return NormalizeCategoryFor("long_video", raw)
}

// feedEligible is the Go half of the feed predicate, applied to every row
// the store returns: the owner's own long video that the signed-out
// playback rule admits.
func feedEligible(p *postgres.Post, ownerID uuid.UUID, ownerVisible bool) bool {
	if p == nil || p.AuthorID != ownerID {
		return false
	}
	if !isLongVideoContentType(p.ContentType) {
		return false
	}
	return anonymousMayAccessPost(p, ownerVisible)
}

// pickFeedEnclosure chooses the enclosure for a media record: 720p, then
// 480p, then the original, the first that has an object and a known size.
// false when the asset is not a ready, passed video or no candidate has a
// size, in which case the item is left out of the feed.
func pickFeedEnclosure(mediaID uuid.UUID, rec *FeedMediaRecord) (ChannelFeedEnclosure, bool) {
	if rec == nil || rec.FileType != "video" {
		return ChannelFeedEnclosure{}, false
	}
	if !mediaPublishable(rec.ProcessingStatus, rec.ModerationStatus) {
		return ChannelFeedEnclosure{}, false
	}
	base := "/v1/media/" + mediaID.String() + "/serve"
	for _, want := range feedEnclosureVariants {
		for _, v := range rec.Variants {
			if v.Name != want || v.ObjectKey == "" || v.SizeBytes == nil || *v.SizeBytes <= 0 {
				continue
			}
			mime := strings.TrimSpace(v.Mime)
			if mime == "" {
				mime = feedDefaultMime
			}
			return ChannelFeedEnclosure{Variant: want, Path: base + "/" + want, Mime: mime, SizeBytes: *v.SizeBytes}, true
		}
	}
	if rec.FileSizeBytes <= 0 {
		return ChannelFeedEnclosure{}, false
	}
	mime := strings.TrimSpace(rec.MimeType)
	if mime == "" {
		mime = feedDefaultMime
	}
	return ChannelFeedEnclosure{Variant: feedVariantOriginal, Path: base, Mime: mime, SizeBytes: rec.FileSizeBytes}, true
}

// ChannelFeed builds the published feed of the channel named by ref (handle,
// @handle or owner id). There is no viewer parameter on purpose: see the
// file comment.
//
//	ErrInvalidCategory   category is not a long-video taxonomy id
//	ErrChannelNotFound   no such channel, or its owner is private / hidden
func (s *Service) ChannelFeed(ctx context.Context, ref, category string, limit int) (*ChannelFeed, error) {
	category, err := NormalizeChannelFeedCategory(category)
	if err != nil {
		return nil, err
	}
	if s.channels == nil || s.channelFeed == nil {
		return nil, errors.New("channel store not configured")
	}
	ch, err := s.resolveChannelRef(ctx, ref)
	if err != nil {
		return nil, err
	}
	// The account gate, asked as the anonymous stranger (nil viewer):
	// graph-service's account privacy plus the hidden-author list, fail
	// closed. Not visible is "no channel", with nothing to tell them apart.
	ownerVisible := s.canViewAuthor(ctx, nil, ch.UserID)
	if !ownerVisible {
		return nil, ErrChannelNotFound
	}

	rows, err := s.channelFeed.ListChannelFeedVideos(ctx, ch.UserID, category, NormalizeChannelFeedLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("channel feed: list videos: %w", err)
	}
	facts, err := s.channelFeed.ChannelFeedFacts(ctx, ch.UserID)
	if err != nil {
		return nil, fmt.Errorf("channel feed: channel facts: %w", err)
	}

	eligible := make([]postgres.ChannelFeedVideo, 0, len(rows))
	var mediaIDs []uuid.UUID
	for i := range rows {
		row := rows[i]
		if !feedEligible(&row.Post, ch.UserID, ownerVisible) {
			slog.WarnContext(ctx, "channel feed: store returned a row the anonymous rule refuses; dropped",
				"post_id", row.Post.ID, "owner_id", ch.UserID)
			continue
		}
		if row.MediaID == nil || !mediaPublishable(row.ProcessingStatus, row.ModerationStatus) {
			// No video, or one still in the pipeline: not an episode yet.
			continue
		}
		eligible = append(eligible, row)
		mediaIDs = append(mediaIDs, *row.MediaID)
	}
	enclosures := s.resolveFeedEnclosures(ctx, mediaIDs)

	feed := &ChannelFeed{
		Channel: ChannelFeedChannel{
			UserID: ch.UserID, Name: ch.Name, Handle: ch.Handle, About: ch.About,
			AvatarMediaID: ch.AvatarMediaID, ContactEmail: ch.ContactEmail,
			Language: facts.Language, DominantCategory: facts.DominantCategory,
		},
		Category:  category,
		UpdatedAt: ch.UpdatedAt.UTC(),
		Items:     make([]ChannelFeedItem, 0, len(eligible)),
	}
	if ch.AvatarMediaID != nil {
		u := "/v1/media/" + ch.AvatarMediaID.String() + "/serve"
		feed.Channel.AvatarURL = &u
	}
	for _, row := range eligible {
		enc, ok := enclosures[*row.MediaID]
		if !ok {
			continue
		}
		p := row.Post
		published := p.CreatedAt
		if p.PublishedAt != nil {
			published = *p.PublishedAt
		}
		hashtags := p.Hashtags
		if hashtags == nil {
			hashtags = []string{}
		}
		feed.Items = append(feed.Items, ChannelFeedItem{
			ID: p.ID, Title: p.Title, Text: p.Text, Category: p.Category, Language: p.Language,
			Hashtags: hashtags, PublishedAt: published.UTC(), MediaID: *row.MediaID, DurationMs: row.DurationMs,
			CoverMediaID: p.CoverMediaID, Enclosure: enc,
		})
		for _, t := range []time.Time{published, p.UpdatedAt} {
			if t.After(feed.UpdatedAt) {
				feed.UpdatedAt = t.UTC()
			}
		}
	}
	return feed, nil
}

// ── enclosure resolution ────────────────────────────────────────────────────

type feedEnclosureEntry struct {
	enclosure ChannelFeedEnclosure
	expires   time.Time
}

func (s *Service) feedClock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Service) cachedFeedEnclosure(mediaID uuid.UUID) (ChannelFeedEnclosure, bool) {
	s.feedEnclosureMu.Lock()
	defer s.feedEnclosureMu.Unlock()
	e, ok := s.feedEnclosures[mediaID]
	if !ok || !s.feedClock().Before(e.expires) {
		return ChannelFeedEnclosure{}, false
	}
	return e.enclosure, true
}

func (s *Service) rememberFeedEnclosure(mediaID uuid.UUID, enc ChannelFeedEnclosure) {
	now := s.feedClock()
	s.feedEnclosureMu.Lock()
	defer s.feedEnclosureMu.Unlock()
	if s.feedEnclosures == nil {
		s.feedEnclosures = make(map[uuid.UUID]feedEnclosureEntry)
	}
	if len(s.feedEnclosures) >= channelFeedEnclosureCacheLimit {
		for k, e := range s.feedEnclosures {
			if !now.Before(e.expires) {
				delete(s.feedEnclosures, k)
			}
		}
		if len(s.feedEnclosures) >= channelFeedEnclosureCacheLimit {
			s.feedEnclosures = make(map[uuid.UUID]feedEnclosureEntry)
		}
	}
	s.feedEnclosures[mediaID] = feedEnclosureEntry{enclosure: enc, expires: now.Add(channelFeedEnclosureTTL)}
}

// resolveFeedEnclosures answers, for each media id, the enclosure to list;
// an id absent from the result has none and its item is omitted. Resolved
// enclosures are reused for channelFeedEnclosureTTL. Failures and
// not-ready answers are NOT cached: a video that finishes transcoding, or a
// media-service that comes back, shows on the next build of the feed.
func (s *Service) resolveFeedEnclosures(ctx context.Context, mediaIDs []uuid.UUID) map[uuid.UUID]ChannelFeedEnclosure {
	out := make(map[uuid.UUID]ChannelFeedEnclosure, len(mediaIDs))
	var pending []uuid.UUID
	seen := make(map[uuid.UUID]bool, len(mediaIDs))
	for _, id := range mediaIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		if enc, ok := s.cachedFeedEnclosure(id); ok {
			out[id] = enc
			continue
		}
		pending = append(pending, id)
	}
	if len(pending) == 0 {
		return out
	}
	source := s.feedMedia
	if source == nil {
		source = httpFeedMediaSource{svc: s}
	}

	ctx, cancel := context.WithTimeout(ctx, channelFeedResolveBudget)
	defer cancel()
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, channelFeedMediaConcurrency)
	)
	for _, id := range pending {
		wg.Add(1)
		go func(id uuid.UUID) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			callCtx, callCancel := context.WithTimeout(ctx, channelFeedMediaTimeout)
			defer callCancel()
			rec, err := source.MediaRecord(callCtx, id)
			if err != nil {
				slog.WarnContext(ctx, "channel feed: media record unresolved; item omitted", "media_id", id, "err", err)
				return
			}
			enc, ok := pickFeedEnclosure(id, rec)
			if !ok {
				return
			}
			s.rememberFeedEnclosure(id, enc)
			mu.Lock()
			out[id] = enc
			mu.Unlock()
		}(id)
	}
	wg.Wait()
	return out
}

// httpFeedMediaSource reads GET {mediaServiceURL}/v1/media/:id as a trusted
// service caller: the internal key, and never a viewer id, so media-service
// answers the record (MediaForService) without an audience question. The
// audience was already decided here, by the anonymous rule.
type httpFeedMediaSource struct{ svc *Service }

func (h httpFeedMediaSource) MediaRecord(ctx context.Context, mediaID uuid.UUID) (*FeedMediaRecord, error) {
	s := h.svc
	if s == nil || s.mediaServiceURL == "" || s.httpClient == nil {
		return nil, fmt.Errorf("%w: media-service not configured", errFeedMediaUnavailable)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(s.mediaServiceURL, "/")+"/v1/media/"+mediaID.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errFeedMediaUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errFeedMediaUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return nil, fmt.Errorf("%w: status %d", errFeedMediaUnavailable, resp.StatusCode)
	}
	var envelope struct {
		Data *FeedMediaRecord `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("%w: decode: %v", errFeedMediaUnavailable, err)
	}
	if envelope.Data == nil {
		return nil, fmt.Errorf("%w: empty record", errFeedMediaUnavailable)
	}
	return envelope.Data, nil
}
