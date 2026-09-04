package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	// Audit HF3: hydrated posts cached per-viewer (viewer state — viewer
	// reaction, bookmark — is part of the response) for a short window
	// so a typical feed scroll doesn't keep re-hitting post-service.
	// TTL is short because counts go stale fast.
	hydrationCacheTTL = 5 * time.Minute
)

func hydrationCacheKey(viewerID, postID uuid.UUID) string {
	return fmt.Sprintf("feed:hydrate:%s:%s", viewerID, postID)
}

// HydratedPost is the fully enriched post returned to the frontend.
type HydratedPost struct {
	ID             uuid.UUID       `json:"id"`
	AuthorID       uuid.UUID       `json:"author_id"`
	Text           string          `json:"text"`
	Visibility     string          `json:"visibility"`
	ContentType    string          `json:"content_type"`
	IsPinned       bool            `json:"is_pinned"`
	Feeling        *string         `json:"feeling,omitempty"`
	Activity       *string         `json:"activity,omitempty"`
	ActivityDetail *string         `json:"activity_detail,omitempty"`
	CoverMediaID   *uuid.UUID      `json:"cover_media_id,omitempty"`
	CreatedAt      string          `json:"created_at"`
	UpdatedAt      string          `json:"updated_at"`
	Media          []HydratedMedia `json:"media,omitempty"`
	Counts         json.RawMessage `json:"counts,omitempty"`
	// ViewCount is the display view count from analytics-service's
	// Redis counter (post:views:{id} → display). Enriched at hydration
	// time; `counts` carries likes/comments/shares only.
	ViewCount       int64           `json:"view_count"`
	ViewerReaction  *string         `json:"viewer_reaction,omitempty"`
	HasReacted      bool            `json:"has_reacted"`
	IsBookmarked    bool            `json:"is_bookmarked"`
	RepostCount     int             `json:"repost_count"`
	HasReposted     bool            `json:"has_reposted"`
	IsRepostable    bool            `json:"is_repostable"`
	Poll            json.RawMessage `json:"poll,omitempty"`
	Location        *string         `json:"location,omitempty"`
	Hashtags        json.RawMessage `json:"hashtags,omitempty"`
	PostType        string          `json:"post_type,omitempty"`
	AppOrigin       string          `json:"app_origin,omitempty"`
	ShareToPostbook bool            `json:"share_to_postbook"`
	Score           float64         `json:"score,omitempty"`
	VideoMetadata   json.RawMessage `json:"video_metadata,omitempty"`
	RichText        json.RawMessage `json:"rich_text,omitempty"`

	// Per-reel controls, category, tagged people and location (2026-09-04).
	//
	// This struct is decoded from post-service by field name, so anything not
	// declared here is dropped between post-service and the feed item. These
	// are what the reel player needs to decide which action buttons to show,
	// and they were being thrown away.
	//
	// The three switches are never omitempty: the player cannot tell
	// "downloads allowed" from "field missing", and post-service emits them
	// on every post. `location_name` is the composer's field; the older
	// `location` above was never populated by post-service and is kept only
	// so an existing reader does not break.
	Title         string      `json:"title,omitempty"`
	NoComments    bool        `json:"no_comments"`
	HideShare     bool        `json:"hide_share"`
	AllowDownload bool        `json:"allow_download"`
	RemixSetting  string      `json:"remix_setting,omitempty"`
	Category      string      `json:"category,omitempty"`
	Tags          []string    `json:"tags,omitempty"`
	TaggedUserIDs []uuid.UUID `json:"tagged_user_ids,omitempty"`
	LocationName  *string     `json:"location_name,omitempty"`
	LocationLat   *float64    `json:"location_lat,omitempty"`
	LocationLng   *float64    `json:"location_lng,omitempty"`

	// IsProcessing (instant publish, 2026-09-04): post-service sets it while
	// any attached asset is not yet ready+passed. Such a post is the
	// author's alone — post-service's batch already drops it for anyone
	// else, and applyProcessingFilter re-checks at the hydration tail so a
	// cached row cannot slip through. Never omitempty: the reel player
	// shows "improving quality" on true and must not confuse false with
	// missing.
	IsProcessing bool `json:"is_processing"`

	// Repost metadata — populated when this entry is a repost in someone's timeline
	IsRepost        bool       `json:"is_repost,omitempty"`
	RepostedBy      *uuid.UUID `json:"reposted_by,omitempty"`
	FeedContentType string     `json:"feed_content_type,omitempty"` // "post", "repost", "reel", etc.
	Author          Author     `json:"author"`
}

// Author is the deliberately small, public identity needed to render a feed
// card. It mirrors profile-service's public allowlist and cannot accidentally
// expose private profile fields.
type Author struct {
	ID            uuid.UUID  `json:"id"`
	DisplayName   string     `json:"display_name"`
	Username      *string    `json:"username,omitempty"`
	AvatarMediaID *uuid.UUID `json:"avatar_media_id,omitempty"`
}

// HydratedMedia preserves the existing media_id/kind contract and adds the
// authorized delivery DTO inline. Android can render a page without issuing a
// request per row and can batch-refresh the URLs after ExpiresAt.
//
// AltText/AltDecorative are Slice C / C-CLB-3. They arrive already populated
// from post-service's batch response and are NOT overwritten by the delivery
// merge below, which only fills in the authorized-URL fields. A feed that
// dropped them would leave every image in the main scrolling surface
// unlabelled — the single place where it matters most.
//
// NOT omitempty, matching post-service. Omitting a false `alt_decorative`
// would make the feed and the post read two different contracts for the same
// image, and "field absent" is a third state neither renderer should have to
// reason about.
type HydratedMedia struct {
	MediaID uuid.UUID `json:"media_id"`
	Kind    string    `json:"kind"`

	// Position is the zero-based carousel ordinal, decoded from post-service
	// and re-emitted. Creator Studio P0-A, errata E-2.
	//
	// Always emitted, never omitempty: an absent ordinal and ordinal 0 must
	// not be the same bytes. Renumbered after the authorization filter,
	// because a denied middle asset would otherwise leave a gap the client is
	// required to reject.
	Position int `json:"position"`

	AltText       string            `json:"alt_text"`
	AltDecorative bool              `json:"alt_decorative"`
	Status        string            `json:"status,omitempty"`
	Width         *int              `json:"width,omitempty"`
	Height        *int              `json:"height,omitempty"`
	Blurhash      *string           `json:"blurhash,omitempty"`
	Variants      map[string]string `json:"variants,omitempty"`
	HLSURL        string            `json:"hls_url,omitempty"`
	ExpiresAt     *time.Time        `json:"expires_at,omitempty"`

	// Pipeline state, decoded from post-service and re-emitted (instant
	// publish). processing_status: pending_upload|uploaded|processing|
	// ready|failed; moderation_status: pending|passed|rejected|manual_review.
	ProcessingStatus string `json:"processing_status,omitempty"`
	ModerationStatus string `json:"moderation_status,omitempty"`

	// PlaybackURL is the ONE URL a video player should open, and
	// PlaybackKind says what it is: "hls" (the authorized master playlist,
	// gateway-relative like hls_url) once transcoding is done, or
	// "original" (the signed progressive MP4 the phone uploaded) while it
	// is still running. Filled from media-service's delivery DTO; absent
	// for images.
	PlaybackURL  string `json:"playback_url,omitempty"`
	PlaybackKind string `json:"playback_kind,omitempty"`
}

// HydratePosts calls post-service's batch endpoint to enrich timeline entries
// with full post details (text, media, counts, etc.).
//
// Audit HF3: a Redis MGET-then-MSET cache fronts the post-service
// round trip. Cache key is per-(viewer, post) because the response
// embeds viewer state (viewer_reaction, is_bookmarked). TTL is
// intentionally short (5 min) so engagement counts don't go stale
// long enough to be misleading. On any Redis miss/error we fall
// through to the original batch fetch — best-effort cache.
func (s *Service) HydratePosts(ctx context.Context, items []FeedItem, viewerID uuid.UUID) ([]HydratedPost, error) {
	if len(items) == 0 {
		return []HydratedPost{}, nil
	}

	// 1. Collect unique post IDs
	seen := make(map[uuid.UUID]bool, len(items))
	uniquePostIDs := make([]uuid.UUID, 0, len(items))
	for _, item := range items {
		if !seen[item.PostID] {
			seen[item.PostID] = true
			uniquePostIDs = append(uniquePostIDs, item.PostID)
		}
	}

	// 1a. Check the per-viewer Redis cache for prebuilt hydrated rows.
	cached := s.fetchHydratedCache(ctx, viewerID, uniquePostIDs)

	// Build the list of post IDs we still need from post-service.
	ids := make([]string, 0, len(uniquePostIDs))
	for _, pid := range uniquePostIDs {
		if _, ok := cached[pid]; !ok {
			ids = append(ids, pid.String())
		}
	}

	envelopeData := make(map[string]HydratedPost, len(uniquePostIDs))
	for pid, h := range cached {
		envelopeData[pid.String()] = h
	}

	if len(ids) == 0 {
		// Entire batch served from cache — skip the HTTP call.
		merged := s.mergeHydratedItems(items, envelopeData, nil)
		// Instant publish: a post still processing is the author's alone.
		// post-service's batch already dropped it for anyone else; this
		// re-checks the cached rows too. Pure and viewer-keyed — see
		// processingfilter.go.
		merged = applyProcessingFilter(viewerID, merged)
		// Viewer keyword filter ("Filter keywords"): every surface — home,
		// reels, flicks, videos, watch — funnels through HydratePosts, so
		// applying it here is the one place no surface can bypass. It runs
		// before render enrichment so dropped posts cost no profile/media
		// fetches, and it FAILS CLOSED like block/mute: a failed lookup is
		// an error, never an unfiltered page.
		merged, err := s.applyKeywordHideFilter(ctx, viewerID, merged)
		if err != nil {
			return nil, err
		}
		// Private accounts: the cached rows above were hydrated up to five
		// minutes ago, so they may pre-date an author going private or this
		// viewer being removed as a follower. Same fail-closed policy as
		// block/mute and keywords — see privacyfilter.go.
		merged, err = s.applyAuthorPrivacyFilter(ctx, viewerID, merged)
		if err != nil {
			return nil, err
		}
		s.enrichViewCounts(ctx, merged)
		if err := s.enrichRenderData(ctx, merged, viewerID); err != nil {
			return nil, err
		}
		return merged, nil
	}

	// 2. Build request body
	reqBody, err := json.Marshal(map[string]interface{}{
		"ids":       ids,
		"viewer_id": viewerID.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal batch request: %w", err)
	}

	// 3. Call post-service batch endpoint
	url := fmt.Sprintf("%s/v1/posts/batch", s.postServiceURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("create batch request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", viewerID.String())
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}

	resp, err := s.postClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("post-service batch request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read batch response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("post-service returned %d: %s", resp.StatusCode, string(body))
	}

	// 4. Parse response: {"data": {"uuid1": {...}, "uuid2": {...}}}
	var envelope struct {
		Data map[string]HydratedPost `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("unmarshal batch response: %w", err)
	}

	// 4a. Merge the fresh response with anything we got from cache.
	for k, v := range envelope.Data {
		envelopeData[k] = v
	}

	// 4b. Populate cache for fresh entries so subsequent feed pages
	// reuse them. Fire-and-forget; never block the response.
	s.storeHydratedCache(viewerID, envelope.Data)

	merged := s.mergeHydratedItems(items, envelopeData, nil)
	// Instant publish: a post still processing is the author's alone.
	// post-service's batch already dropped it for anyone else; this
	// re-checks the cached rows too. Pure and viewer-keyed — see
	// processingfilter.go.
	merged = applyProcessingFilter(viewerID, merged)
	// Viewer keyword filter — same step as the cache-only path above; see
	// that comment. Fail-closed by design.
	merged, err = s.applyKeywordHideFilter(ctx, viewerID, merged)
	if err != nil {
		return nil, err
	}
	// Private accounts — same step as the cache-only path above.
	merged, err = s.applyAuthorPrivacyFilter(ctx, viewerID, merged)
	if err != nil {
		return nil, err
	}
	s.enrichViewCounts(ctx, merged)
	if err := s.enrichRenderData(ctx, merged, viewerID); err != nil {
		return nil, err
	}
	return merged, nil
}

type publicProfile struct {
	UserID        uuid.UUID  `json:"user_id"`
	Username      *string    `json:"username,omitempty"`
	DisplayName   string     `json:"display_name"`
	AvatarMediaID *uuid.UUID `json:"avatar_media_id,omitempty"`
}

type mediaDelivery struct {
	MediaID   uuid.UUID         `json:"media_id"`
	Kind      string            `json:"kind"`
	Status    string            `json:"status"`
	Width     *int              `json:"width,omitempty"`
	Height    *int              `json:"height,omitempty"`
	Blurhash  *string           `json:"blurhash,omitempty"`
	Variants  map[string]string `json:"variants,omitempty"`
	HLSURL    string            `json:"hls_url,omitempty"`
	ExpiresAt *time.Time        `json:"expires_at,omitempty"`
	// Instant publish: the one URL to play and what it is ("hls" |
	// "original"). See HydratedMedia.PlaybackURL.
	PlaybackURL  string `json:"playback_url,omitempty"`
	PlaybackKind string `json:"playback_kind,omitempty"`
}

// enrichRenderData resolves author identity and media delivery concurrently,
// then applies the results only after both batch calls succeed. Delivery data
// is intentionally fetched after the post cache: a five-minute signed URL is
// never persisted in a five-minute cache and handed out at the expiry edge.
func (s *Service) enrichRenderData(ctx context.Context, posts []HydratedPost, viewerID uuid.UUID) error {
	if len(posts) == 0 {
		return nil
	}
	authorIDs := make([]uuid.UUID, 0, len(posts))
	mediaIDs := make([]uuid.UUID, 0, len(posts))
	seenAuthors := make(map[uuid.UUID]bool, len(posts))
	seenMedia := make(map[uuid.UUID]bool, len(posts))
	for _, post := range posts {
		if !seenAuthors[post.AuthorID] {
			seenAuthors[post.AuthorID] = true
			authorIDs = append(authorIDs, post.AuthorID)
		}
		for _, media := range post.Media {
			if !seenMedia[media.MediaID] {
				seenMedia[media.MediaID] = true
				mediaIDs = append(mediaIDs, media.MediaID)
			}
		}
	}

	var profiles map[uuid.UUID]publicProfile
	var deliveries map[uuid.UUID]mediaDelivery
	var profileErr, mediaErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		profiles, profileErr = s.fetchAuthors(ctx, viewerID, authorIDs)
	}()
	if len(mediaIDs) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			deliveries, mediaErr = s.fetchMediaDeliveries(ctx, viewerID, mediaIDs)
		}()
	}
	wg.Wait()
	if profileErr != nil {
		return fmt.Errorf("profile hydration failed: %w", profileErr)
	}
	if mediaErr != nil {
		return fmt.Errorf("media hydration failed: %w", mediaErr)
	}

	for i := range posts {
		profile, ok := profiles[posts[i].AuthorID]
		posts[i].Author = Author{ID: posts[i].AuthorID, DisplayName: "Deleted account"}
		if ok {
			posts[i].Author.DisplayName = profile.DisplayName
			posts[i].Author.Username = profile.Username
			posts[i].Author.AvatarMediaID = profile.AvatarMediaID
		}
		authorizedMedia := make([]HydratedMedia, 0, len(posts[i].Media))
		for _, m := range posts[i].Media {
			delivery, ok := deliveries[m.MediaID]
			if !ok {
				slog.WarnContext(ctx, "feed hydration: media asset denied and omitted from post",
					"viewer_id", viewerID,
					"post_id", posts[i].ID,
					"media_id", m.MediaID)
				continue // omit denied media from the post rather than failing the whole feed page
			}
			m.Status = delivery.Status
			m.Width = delivery.Width
			m.Height = delivery.Height
			m.Blurhash = delivery.Blurhash
			m.Variants = delivery.Variants
			m.HLSURL = delivery.HLSURL
			m.PlaybackURL = delivery.PlaybackURL
			m.PlaybackKind = delivery.PlaybackKind
			if delivery.ExpiresAt != nil && !delivery.ExpiresAt.IsZero() {
				expires := *delivery.ExpiresAt
				m.ExpiresAt = &expires
			} else {
				m.ExpiresAt = nil
			}
			authorizedMedia = append(authorizedMedia, m)
		}
		// Renumber after filtering.
		//
		// post-service already returned this slice ordered and the loop above
		// preserves that order, so index IS the ordinal. It is reassigned rather
		// than passed through because a denied asset is omitted, and dropping
		// position 1 from a three-image post would emit ordinals 0 and 2 - a gap
		// the client contiguity check rejects, which would lose the whole post
		// from the feed instead of one unviewable image.
		for j := range authorizedMedia {
			authorizedMedia[j].Position = j
		}
		posts[i].Media = authorizedMedia
	}
	return nil
}

func (s *Service) fetchAuthors(ctx context.Context, viewerID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]publicProfile, error) {
	result := make(map[uuid.UUID]publicProfile, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	requestIDs := make([]string, len(ids))
	for i, id := range ids {
		requestIDs[i] = id.String()
	}
	body, err := json.Marshal(map[string]any{"user_ids": requestIDs})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(s.profileServiceURL, "/")+"/v1/profiles/batch", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", viewerID.String())
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	resp, err := s.profileClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("profile-service returned %d: %s", resp.StatusCode, string(b))
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode profile batch: %w", err)
	}
	return result, nil
}

func (s *Service) fetchMediaDeliveries(ctx context.Context, viewerID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]mediaDelivery, error) {
	result := make(map[uuid.UUID]mediaDelivery, len(ids))
	for start := 0; start < len(ids); start += 50 {
		end := start + 50
		if end > len(ids) {
			end = len(ids)
		}
		requestIDs := make([]string, end-start)
		for i, id := range ids[start:end] {
			requestIDs[i] = id.String()
		}
		body, err := json.Marshal(map[string]any{"ids": requestIDs})
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			strings.TrimRight(s.mediaServiceURL, "/")+"/v1/media/batch", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-Id", viewerID.String())
		if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
			req.Header.Set("X-Internal-Service-Key", key)
		}
		resp, err := s.mediaClient.Do(req)
		if err != nil {
			return nil, err
		}
		var envelope struct {
			Data map[uuid.UUID]mediaDelivery `json:"data"`
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&envelope)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("media-service returned %d", resp.StatusCode)
		}
		if decodeErr != nil {
			return nil, fmt.Errorf("decode media batch: %w", decodeErr)
		}
		for id, delivery := range envelope.Data {
			result[id] = delivery
		}
	}
	return result, nil
}

// enrichViewCounts fills HydratedPost.ViewCount from the shared Redis
// view counter (post:views:{id} hash, "display" field) that
// analytics-service maintains. One pipelined round trip for the whole
// page; best-effort — on any Redis error the counts stay 0 rather than
// failing the feed. View counts intentionally aren't part of the
// hydration cache blob, so this always reflects the live counter.
func (s *Service) enrichViewCounts(ctx context.Context, posts []HydratedPost) {
	if s.rdb == nil || len(posts) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	pipe := s.rdb.Pipeline()
	cmds := make([]*redis.StringCmd, len(posts))
	for i, p := range posts {
		cmds[i] = pipe.HGet(ctx, "post:views:"+p.ID.String(), "display")
	}
	// Exec returns redis.Nil when any key/field is missing — expected for
	// posts with no views yet. Per-command parsing below handles it.
	_, _ = pipe.Exec(ctx)
	for i := range posts {
		if n, err := cmds[i].Int64(); err == nil {
			posts[i].ViewCount = n
		}
	}
}

// mergeHydratedItems flattens (feed item × hydrated post) into the
// ordered response. Reposts of an already-seen original are kept (a
// repost is a distinct feed event — "User X reposted this"); other
// duplicates are dropped. The optional `score` map overrides item.Score
// when non-nil — kept around for future re-ranking, currently unused.
func (s *Service) mergeHydratedItems(items []FeedItem, envelopeData map[string]HydratedPost, score map[uuid.UUID]float64) []HydratedPost {
	hydrated := make([]HydratedPost, 0, len(items))
	emitted := make(map[uuid.UUID]bool, len(items))
	for _, item := range items {
		isRepost := item.ContentType == "repost"
		if emitted[item.PostID] && !isRepost {
			continue
		}
		post, ok := envelopeData[item.PostID.String()]
		if !ok {
			// Post was deleted, hidden, or filtered by post-service's
			// visibility gate (audit CF1) — skip it.
			continue
		}
		post.Score = item.Score
		if score != nil {
			if v, ok := score[item.PostID]; ok {
				post.Score = v
			}
		}
		post.FeedContentType = item.ContentType
		if isRepost {
			post.IsRepost = true
			authorID := item.AuthorID
			post.RepostedBy = &authorID
		}
		hydrated = append(hydrated, post)
		emitted[item.PostID] = true
	}
	return hydrated
}

// fetchHydratedCache reads the per-(viewer, post) cache via Redis MGET.
// Missing / unparseable entries are dropped silently. Returns an empty
// map (never nil) so callers can range over it.
func (s *Service) fetchHydratedCache(ctx context.Context, viewerID uuid.UUID, postIDs []uuid.UUID) map[uuid.UUID]HydratedPost {
	result := make(map[uuid.UUID]HydratedPost, len(postIDs))
	if s.rdb == nil || len(postIDs) == 0 {
		return result
	}
	keys := make([]string, len(postIDs))
	for i, pid := range postIDs {
		keys[i] = hydrationCacheKey(viewerID, pid)
	}
	mgetCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	vals, err := s.rdb.MGet(mgetCtx, keys...).Result()
	if err != nil {
		// Best-effort cache; never break the request.
		log.Printf("[feed-hydrate] cache MGET failed: %v", err)
		return result
	}
	for i, raw := range vals {
		s, ok := raw.(string)
		if !ok || s == "" {
			continue
		}
		var hp HydratedPost
		if err := json.Unmarshal([]byte(s), &hp); err != nil {
			continue
		}
		result[postIDs[i]] = hp
	}
	return result
}

// storeHydratedCache writes fresh hydrated rows back into Redis.
// Asynchronous so the response isn't held up by Redis write latency.
func (s *Service) storeHydratedCache(viewerID uuid.UUID, fresh map[string]HydratedPost) {
	if s.rdb == nil || len(fresh) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		pipe := s.rdb.Pipeline()
		for idStr, hp := range fresh {
			pid, err := uuid.Parse(idStr)
			if err != nil {
				continue
			}
			// Instant publish: never cache a processing post. Only its
			// author receives one, and the author's next scroll must see
			// is_processing flip the moment transcoding lands, not five
			// minutes later.
			if hp.IsProcessing {
				continue
			}
			data, err := json.Marshal(hp)
			if err != nil {
				continue
			}
			pipe.Set(ctx, hydrationCacheKey(viewerID, pid), data, hydrationCacheTTL)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			log.Printf("[feed-hydrate] cache SET failed: %v", err)
		}
	}()
}
