package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// Scheduled publish (founder, 2026-09-05).
//
// The reel studio submits, returns to the profile, uploads in the
// background and then publishes — or SCHEDULES. `POST /v1/posts` accepts an
// optional `publish_at`; a scheduled post is stored with everything a live
// post has but posts.publish_at set (migration 042), and until the worker
// (internal/postschedule) publishes it:
//
//   - it is author-only on the direct read, exactly like is_processing
//     (hiddenWhileScheduled sits next to hiddenWhileProcessing on every
//     read path);
//   - every list/feed/search/hashtag query filters publish_at IS NULL;
//   - NOTHING is emitted: no PostCreated (so no feed fan-out, no search
//     index, no follower/subscriber notification), no user.mentioned, no
//     hashtag counters, no live pub/sub.
//
// Publishing — by the worker when due, or by PATCH …/schedule
// {"publish_at": null} — clears the scheduled state, moves created_at to
// the publish moment so the post sorts as new, and emits the SAME
// PostCreated a fresh post emits, in the same transaction as the row
// change. If the media is still processing at that moment the post is
// published anyway; the processing rules keep it author-only until ready.

const (
	// MinScheduleLead: publish_at must be at least this far in the future.
	MinScheduleLead = 5 * time.Minute
	// MaxScheduleLead: and at most this far.
	MaxScheduleLead = 30 * 24 * time.Hour
)

var (
	// ErrInvalidPublishAt: publish_at is outside [now+5m, now+30d].
	ErrInvalidPublishAt = errors.New("publish_at must be between 5 minutes and 30 days from now")
	// ErrPostNotScheduled: the post is live, deleted, or not the caller's.
	ErrPostNotScheduled = postgres.ErrPostNotScheduled
)

// ValidatePublishAt enforces the scheduling window against `now`. nil is
// "publish immediately" and always valid.
func ValidatePublishAt(publishAt *time.Time, now time.Time) error {
	if publishAt == nil {
		return nil
	}
	if publishAt.IsZero() {
		return ErrInvalidPublishAt
	}
	if publishAt.Before(now.Add(MinScheduleLead)) || publishAt.After(now.Add(MaxScheduleLead)) {
		return ErrInvalidPublishAt
	}
	return nil
}

// hiddenWhileScheduled is the read-side gate: a scheduled post is hidden
// from everyone but its author. An anonymous viewer is never the author.
func hiddenWhileScheduled(p *postgres.Post, viewerID *uuid.UUID) bool {
	if p == nil || p.PublishAt == nil {
		return false
	}
	return viewerID == nil || *viewerID != p.AuthorID
}

// hiddenFromViewer folds the two author-only gates — still processing,
// still scheduled — so every page filter applies both.
func hiddenFromViewer(p *postgres.Post, viewerID *uuid.UUID) bool {
	return hiddenWhileProcessing(p, viewerID) || hiddenWhileScheduled(p, viewerID)
}

// buildPostCreatedPayload is the ONE place the PostCreated event is shaped,
// shared by the create path (fresh post) and the publish path (scheduled
// post going live) so a scheduled post announces itself exactly like a
// fresh one. policy is the post's distribution policy (nil = legacy);
// maxDuration the longest attached video in seconds; searchRev the
// revision stamped on the row in the same transaction.
func (s *Service) buildPostCreatedPayload(ctx context.Context, p *postgres.Post, policy *DistributionPolicy, maxDuration int, searchRev int64) events.PostCreatedPayload {
	pc := events.PostCreatedPayload{
		PostID:          p.ID.String(),
		AuthorID:        p.AuthorID.String(),
		Text:            p.Text,
		Visibility:      p.Visibility,
		ContentType:     p.ContentType,
		DurationSeconds: maxDuration,
		CreatedAt:       p.CreatedAt,
		DistributionRev: p.DistributionRev,
		// Module 2 M2-P0-1: carry the CANONICAL persisted moderation
		// state so search can refuse to index held content. Without
		// this, a post gated at 'pending' by the video/voice safety
		// check was indexed and publicly findable immediately.
		ReviewStatus: p.ReviewStatus,
		SearchRev:    searchRev,
	}
	// Additive pointer fields: stamped whenever an intent exists —
	// either a typed policy or explicit legacy fields (P1-1). Events
	// for clients that expressed no opinion stay byte-compatible.
	if policy != nil {
		resolved := ResolveDistribution(policy)
		mf, ns := resolved.MainFeed, resolved.NotifySubscribers
		pc.MainFeed = &mf
		pc.NotifySubscribers = &ns
	}
	// Subscriber fan-out key (P0-3): best-effort canonical channel
	// lookup for video uploads. Empty on failure — the notification
	// consumer treats a missing channel as "no subscriber fan-out".
	if isVideoContentType(p.ContentType) {
		pc.ChannelID = s.lookupChannelIDForUser(ctx, p.AuthorID)
	}
	return pc
}

// storeMentions persists the merged mention list to post_mentions (the
// join notifications and the mentions inbox read). Best-effort per row.
func (s *Service) storeMentions(ctx context.Context, postID uuid.UUID, postType string, mentions []string) {
	for _, username := range mentions {
		if err := s.pgStore.InsertMention(ctx, postID, postType, username); err != nil {
			log.Printf("Warning: failed to insert mention for @%s on post %s: %v", username, postID, err)
		}
	}
}

// announcePublishedPost is everything a post does the moment it becomes
// public that is NOT the durable PostCreated (which rides the outbox in
// the row's own transaction): resolve @mentions into user.mentioned
// events, bump trending/hashtag use counters, and the ephemeral Redis
// pub/sub the SSE streams listen on. Called by the create path for a
// fresh post and by the publish path for a scheduled one — never for a
// post that is still scheduled.
//
// Fire-and-forget: none of this is load-bearing for the response, and
// clients tolerate missing a live signal (they catch up on the next
// fetch; SSE replay covers the gap).
func (s *Service) announcePublishedPost(p *postgres.Post, mentions []string) {
	// Resolve @mentions and emit user.mentioned events.
	if s.producer != nil && len(mentions) > 0 {
		postID := p.ID
		authorID := p.AuthorID
		for _, uname := range mentions {
			go func(username string) {
				ctx2, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				userID, err := s.lookupUserByUsername(ctx2, username)
				if err != nil || userID == "" {
					return
				}
				mentionedID, err := uuid.Parse(userID)
				if err != nil || mentionedID == authorID {
					return // skip self-mentions
				}
				if err := s.producer.PublishUserMentioned(ctx2, mentionedID, authorID, postID.String()); err != nil {
					log.Printf("Warning: failed to publish UserMentioned event for @%s: %v", username, err)
				}
			}(uname)
		}
	}

	if s.rdb == nil {
		return
	}
	go func() {
		bgCtx := context.Background()

		// Bump trending hashtag scores for today's bucket. The reader is
		// search-service `GetTrending` and post-service `GetTrendingHashtagsFeed`,
		// both of which read from `trending:hashtags:{YYYY-MM-DD}` (UTC).
		if len(p.Hashtags) > 0 {
			today := time.Now().UTC().Format("2006-01-02")
			key := "trending:hashtags:" + today
			pipe := s.rdb.Pipeline()
			for _, tag := range p.Hashtags {
				pipe.ZIncrBy(bgCtx, key, 1, tag)
			}
			// 48h TTL keeps the previous day's set alive briefly so reads
			// that race past midnight don't return empty.
			pipe.Expire(bgCtx, key, 48*time.Hour)
			if _, err := pipe.Exec(bgCtx); err != nil {
				log.Printf("Warning: failed to update trending:hashtags: %v", err)
			}

			// Counter-sharding rollout: per-tag +1 into the aggregate
			// `hashtags.use_count` counter (kind: "hashtag_use_count").
			// The flush worker (cmd/server) materializes shard sums back
			// into the PG row every 10s. The PG fallback (Redis-less dev
			// loops) is the UPSERT inside adjustHashtagUseCount.
			for _, tag := range p.Hashtags {
				cleaned := strings.ToLower(strings.TrimPrefix(tag, "#"))
				if cleaned == "" {
					continue
				}
				if err := s.adjustHashtagUseCount(bgCtx, cleaned); err != nil {
					log.Printf("Warning: failed to bump hashtags.use_count for %s: %v", cleaned, err)
				}
			}
		}

		snippet := p.Text
		if len(snippet) > 120 {
			snippet = snippet[:120]
		}
		feedSignal, _ := json.Marshal(map[string]interface{}{
			"type": "new_post",
			"payload": map[string]interface{}{
				"post_id":      p.ID.String(),
				"author_id":    p.AuthorID.String(),
				"content_type": p.ContentType,
				"snippet":      snippet,
				"created_at":   p.CreatedAt,
			},
		})
		s.rdb.Publish(bgCtx, "feed:new_post", feedSignal)

		// Per-hashtag real-time push. Same shape as feed:new_post so
		// the SSE handler in internal/http/hashtag_stream.go can
		// forward straight through. One channel per tag — clients
		// subscribed to a specific tag only see posts that actually
		// carry it, no client-side filtering needed.
		for _, tag := range p.Hashtags {
			cleaned := strings.ToLower(strings.TrimPrefix(tag, "#"))
			if cleaned == "" {
				continue
			}
			s.rdb.Publish(bgCtx, "hashtag:"+cleaned+":new_post", feedSignal)
		}
	}()
}

// maxVideoDuration is the longest attached video in seconds (0 when none
// or unknown), the DurationSeconds the PostCreated event carries.
func (s *Service) maxVideoDuration(ctx context.Context, p *postgres.Post) int {
	var ids []uuid.UUID
	for _, m := range p.Media {
		if m.Kind == "video" {
			ids = append(ids, m.MediaID)
		}
	}
	if len(ids) == 0 {
		return 0
	}
	meta, err := s.pgStore.BatchGetMediaMetadata(ctx, ids)
	if err != nil {
		return 0
	}
	maxDur := 0
	for _, id := range ids {
		if m, ok := meta[id]; ok && m.DurationSeconds > maxDur {
			maxDur = m.DurationSeconds
		}
	}
	return maxDur
}

// PublishScheduled makes one scheduled post live: the row flip and its
// PostCreated commit together (store.PublishScheduledPost), then the
// post announces itself exactly like a fresh one. Returns false when the
// post was not scheduled any more — a concurrent run already published it,
// it was deleted, or it was never scheduled — so the worker and "publish
// now" are both idempotent by construction.
//
// dueOnly=true refuses a post whose publish_at is still in the future (the
// worker); false takes it now (the author). authorID, when non-nil, limits
// the flip to that author's post.
func (s *Service) PublishScheduled(ctx context.Context, postID uuid.UUID, authorID *uuid.UUID, dueOnly bool) (bool, error) {
	p, err := s.pgStore.GetPost(ctx, postID)
	if err != nil {
		return false, fmt.Errorf("publish scheduled: load post: %w", err)
	}
	if p == nil || p.PublishAt == nil {
		return false, nil
	}
	if authorID != nil && p.AuthorID != *authorID {
		return false, nil
	}

	// Everything the event needs that lives outside the row, resolved
	// before the transaction so it holds no locks while calling out.
	policy, perr := ParseDistributionPolicy(p.Distribution)
	if perr != nil {
		// A stored policy that no longer parses is a data fault, not a
		// reason to keep the post scheduled forever: publish with legacy
		// distribution and say so.
		slog.Warn("publish scheduled: stored distribution policy unreadable; publishing with legacy distribution",
			"post_id", postID, "err", perr)
		policy = nil
	}
	maxDuration := s.maxVideoDuration(ctx, p)
	now := time.Now().UTC()

	published, err := s.pgStore.PublishScheduledPost(ctx, postID, authorID, now, dueOnly,
		func(rev int64) (string, interface{}, error) {
			if s.producer == nil {
				return "", nil, nil
			}
			live := *p
			live.CreatedAt = now
			return events.PostCreated, s.buildPostCreatedPayload(ctx, &live, policy, maxDuration, rev), nil
		})
	if err != nil {
		return false, err
	}
	if published == nil {
		return false, nil
	}

	// The cached body still says scheduled; drop it before anyone reads.
	s.InvalidatePostBodyCache(ctx, postID)
	if s.rdb != nil {
		s.rdb.Del(ctx, fmt.Sprintf("post:author-counts:%s", p.AuthorID))
	}

	p.PublishAt = nil
	p.IsScheduled = false
	p.PublishedAt = &published.PublishedAt
	p.CreatedAt = published.PublishedAt
	p.UpdatedAt = published.PublishedAt
	s.announcePublishedPost(p, p.MentionUsernames)

	slog.Info("scheduled post published", "event", "post_scheduled_published",
		"post_id", postID, "author_id", p.AuthorID, "content_type", p.ContentType,
		"published_at", published.PublishedAt, "search_rev", published.SearchRev)
	return true, nil
}

// ReschedulePost is PATCH /v1/posts/{id}/schedule. publishAt non-nil moves
// the publish moment (same window as create); nil publishes now. Author
// only. Returns the post as the author sees it afterwards.
func (s *Service) ReschedulePost(ctx context.Context, postID, actorID uuid.UUID, publishAt *time.Time) (*PostDetail, error) {
	p, err := s.pgStore.GetPost(ctx, postID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, ErrPostNotFound
	}
	if p.AuthorID != actorID {
		return nil, ErrPostForbidden
	}
	if p.PublishAt == nil {
		return nil, ErrPostNotScheduled
	}

	if publishAt == nil {
		published, err := s.PublishScheduled(ctx, postID, &actorID, false)
		if err != nil {
			return nil, err
		}
		if !published {
			// Lost a race with the worker (or a concurrent publish-now):
			// the post is live either way, which is what was asked for.
			slog.Info("publish now: post was already published", "post_id", postID)
		}
	} else {
		if err := ValidatePublishAt(publishAt, time.Now()); err != nil {
			return nil, err
		}
		if err := s.pgStore.ReschedulePost(ctx, postID, actorID, publishAt.UTC()); err != nil {
			return nil, err
		}
		s.InvalidatePostBodyCache(ctx, postID)
	}

	detail, err := s.GetPost(ctx, postID, &actorID)
	if err != nil {
		return nil, err
	}
	if detail == nil {
		return nil, ErrPostNotFound
	}
	return detail, nil
}

// ListMyScheduledPosts is GET /v1/posts/me/scheduled: the caller's
// scheduled posts, newest publish_at first, with the live media state so
// the row can show "still processing" next to the publish time.
func (s *Service) ListMyScheduledPosts(ctx context.Context, authorID uuid.UUID, limit int, cursor string) ([]PostDetail, string, error) {
	posts, next, err := s.pgStore.ListScheduledPostsByAuthor(ctx, authorID, limit, cursor)
	if err != nil {
		return nil, "", err
	}
	details := make([]PostDetail, 0, len(posts))
	ptrs := make([]*postgres.Post, 0, len(posts))
	for i := range posts {
		ptrs = append(ptrs, &posts[i])
		details = append(details, PostDetail{Post: &posts[i]})
	}
	if err := s.attachMediaState(ctx, ptrs); err != nil {
		return nil, "", err
	}
	return details, next, nil
}
