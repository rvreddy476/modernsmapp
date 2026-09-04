package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// UploadDetail extends PostDetail with optional video metadata.
type UploadDetail struct {
	PostDetail
	VideoMetadata *postgres.VideoMetadata `json:"video_metadata,omitempty"`
}

var (
	ErrPostNotFound  = errors.New("post not found")
	ErrPostForbidden = errors.New("forbidden")
)

// GetMyVideos returns the user's video and long_video uploads with video metadata.
func (s *Service) GetMyVideos(ctx context.Context, authorID uuid.UUID, limit int, cursor string) ([]UploadDetail, string, error) {
	posts, nextCursor, err := s.pgStore.GetUploadsByContentTypes(ctx, authorID, []string{"video", "long_video"}, limit, cursor)
	if err != nil {
		return nil, "", err
	}
	return s.enrichUploads(ctx, posts), nextCursor, nil
}

// GetMyFlicks returns the user's flick and reel uploads with video metadata.
func (s *Service) GetMyFlicks(ctx context.Context, authorID uuid.UUID, limit int, cursor string) ([]UploadDetail, string, error) {
	posts, nextCursor, err := s.pgStore.GetUploadsByContentTypes(ctx, authorID, []string{"flick", "reel"}, limit, cursor)
	if err != nil {
		return nil, "", err
	}
	return s.enrichUploads(ctx, posts), nextCursor, nil
}

// GetMyPosts returns the user's text/image posts.
func (s *Service) GetMyPosts(ctx context.Context, authorID uuid.UUID, limit int, cursor string) ([]PostDetail, string, error) {
	posts, nextCursor, err := s.pgStore.GetUploadsByContentTypes(ctx, authorID, []string{"post", "image"}, limit, cursor)
	if err != nil {
		return nil, "", err
	}

	details := make([]PostDetail, len(posts))
	for i, p := range posts {
		post := p
		counts, _ := s.scyllaStore.GetCounts(ctx, p.ID)
		details[i] = PostDetail{Post: &post, Counts: counts}
	}
	return details, nextCursor, nil
}

// GetUploadCounts returns counts of videos, flicks, and posts for a user.
func (s *Service) GetUploadCounts(ctx context.Context, authorID uuid.UUID) (videos, flicks, posts int64, err error) {
	return s.pgStore.CountUploadsByContentTypes(ctx, authorID)
}

// DefaultPurgeAfter is the production "Recently deleted" window.
const DefaultPurgeAfter = 30 * 24 * time.Hour

// SetPurgeAfter configures the restore window (POST_PURGE_AFTER).
func (s *Service) SetPurgeAfter(d time.Duration) {
	if d > 0 {
		s.purgeAfter = d
	}
}

// PurgeAfter returns the configured restore window.
func (s *Service) PurgeAfter() time.Duration {
	if s.purgeAfter <= 0 {
		return DefaultPurgeAfter
	}
	return s.purgeAfter
}

// ErrPostNotDeleted / ErrRestoreWindowExpired surface the store's restore
// refusals (mapped to 409 NOT_DELETED and 410 RESTORE_WINDOW_EXPIRED).
var (
	ErrPostNotDeleted       = postgres.ErrPostNotDeleted
	ErrRestoreWindowExpired = postgres.ErrRestoreWindowExpired
)

// DeletePost SOFT-deletes a post (and its crosspost embeds) when owned by
// the caller. The row and its media stay for PurgeAfter so the author can
// restore from "Recently deleted"; the purge worker hard-deletes after.
//
// Every fan-out goes through the transactional outbox inside the store
// (PostDeleted + PostSearchEligibilityChanged). The previous fire-and-forget
// upload.deleted publish is gone: it ran on the request context in a
// goroutine, so a fast client disconnect could lose the event and leave the
// post in every feed.
func (s *Service) DeletePost(ctx context.Context, postID, authorID uuid.UUID) error {
	// Fetch the post first for ownership (the store re-checks in the UPDATE).
	source, err := s.pgStore.GetPost(ctx, postID)
	if err != nil {
		return fmt.Errorf("failed to get post: %w", err)
	}
	if source == nil {
		return ErrPostNotFound
	}
	if source.AuthorID != authorID {
		return ErrPostForbidden
	}

	if _, err := s.pgStore.DeleteUploadCascade(ctx, postID, authorID, s.PurgeAfter()); err != nil {
		return fmt.Errorf("failed to delete post: %w", err)
	}
	// Tier 1b: drop the cached body so a stale read can't keep
	// serving deleted content for up to the TTL window.
	s.InvalidatePostBodyCache(ctx, postID)
	return nil
}

// RestorePost undoes a soft delete within the window (author only).
func (s *Service) RestorePost(ctx context.Context, postID, authorID uuid.UUID) error {
	if _, err := s.pgStore.RestorePost(ctx, postID, authorID, s.PurgeAfter()); err != nil {
		return err
	}
	// The body cache only ever held the live row, and DeletePost dropped
	// it; dropping again is cheap insurance against a stale negative.
	s.InvalidatePostBodyCache(ctx, postID)
	return nil
}

// ListMyDeletedPosts is the author's "Recently deleted" page. Each post
// carries deleted_at and purge_at (= deleted_at + window).
func (s *Service) ListMyDeletedPosts(ctx context.Context, authorID uuid.UUID, limit int, cursor string) ([]PostDetail, string, error) {
	posts, next, err := s.pgStore.ListDeletedPostsByAuthor(ctx, authorID, limit, cursor)
	if err != nil {
		return nil, "", err
	}
	window := s.PurgeAfter()
	details := make([]PostDetail, 0, len(posts))
	for i := range posts {
		p := &posts[i]
		if p.DeletedAt != nil {
			purgeAt := p.DeletedAt.Add(window)
			p.PurgeAt = &purgeAt
		}
		details = append(details, PostDetail{Post: p})
	}
	return details, next, nil
}

// DeleteMediaForPurgedPost asks media-service to delete an asset the purged
// post was the last post to reference (internal/postpurge.MediaDeleter).
//
//	DELETE {MEDIA_SERVICE_URL}/v1/media/internal/{mediaId}
//	{"referrer":"post","referrer_id":"<postId>"}
//
// media-service re-checks its own reference tables before deleting; a 409
// (something else still holds the asset) and a 404 (already gone) both mean
// "nothing more for this post to do" and resolve the queue row.
func (s *Service) DeleteMediaForPurgedPost(ctx context.Context, mediaID, postID uuid.UUID) error {
	base := os.Getenv("MEDIA_SERVICE_URL")
	if base == "" {
		base = "http://media-service:8087"
	}
	body, _ := json.Marshal(map[string]string{"referrer": "post", "referrer_id": postID.String()})
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		strings.TrimRight(base, "/")+"/v1/media/internal/"+mediaID.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil // already gone
	case resp.StatusCode == http.StatusConflict:
		slog.Info("post purge: media-service kept the asset (still referenced elsewhere)",
			"media_id", mediaID, "post_id", postID)
		return nil
	case resp.StatusCode >= 300:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("media-service returned %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// DeleteUploadCascade preserves the legacy "uploads" surface and delegates to DeletePost.
func (s *Service) DeleteUploadCascade(ctx context.Context, postID, authorID uuid.UUID) error {
	return s.DeletePost(ctx, postID, authorID)
}

// enrichUploads adds engagement counts and video metadata to posts.
func (s *Service) enrichUploads(ctx context.Context, posts []postgres.Post) []UploadDetail {
	if len(posts) == 0 {
		return nil
	}

	// Collect post IDs for batch lookups
	postIDs := make([]uuid.UUID, len(posts))
	for i, p := range posts {
		postIDs[i] = p.ID
	}

	// Batch-fetch video metadata
	videoMeta, _ := s.pgStore.BatchGetVideoMetadata(ctx, postIDs)

	// Live media state — processing/moderation status, duration_ms, hls_url
	// — from media_assets, the same overlay every other read gets (Tube
	// "You" page, 2026-09-05). The author is the caller, so nothing is
	// hidden while processing; is_processing simply says so. Best effort:
	// the list is still the author's own uploads without it.
	ptrs := make([]*postgres.Post, len(posts))
	for i := range posts {
		ptrs[i] = &posts[i]
	}
	if err := s.attachMediaState(ctx, ptrs); err != nil {
		slog.Warn("my uploads: media state overlay failed", "error", err)
	}

	details := make([]UploadDetail, len(posts))
	for i, p := range posts {
		post := p
		counts, _ := s.scyllaStore.GetCounts(ctx, p.ID)
		details[i] = UploadDetail{
			PostDetail:    PostDetail{Post: &post, Counts: counts},
			VideoMetadata: videoMeta[p.ID],
		}
	}
	return details
}
