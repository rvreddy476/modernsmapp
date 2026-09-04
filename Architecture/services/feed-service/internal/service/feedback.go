package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/atpost/feed-service/internal/ranking"
	"github.com/atpost/feed-service/internal/store/postgres"
	"github.com/google/uuid"
)

// "Interested" / "Not interested" on the post "more" sheet (product
// decision 2026-09-04) — POST /v1/feed/feedback.
//
// Two things happen on an answer. The answer is stored per (viewer, post),
// latest wins (feed_feedback, migration 006). Then, because a "Not
// interested" the viewer has to scroll past for five more minutes is a
// broken button, the per-viewer hydration cache row for that post is
// dropped, and the exclusion itself is enforced at the hydration tail of
// every surface — the one place a cached row cannot slip through, exactly
// where block/mute, keywords, private accounts and processing posts are
// re-checked. Fail-closed like those: a failed lookup is an error, never an
// unfiltered page.
//
// The ranking side is deliberately small. The scoring formula in
// internal/ranking/scorer.go always named an authorPenalty term and never
// fed it; the viewer's net answers per author now do, through one Redis
// hash the signal loader reads next to the affinity hash. Category is stored
// with the row for a later hook — the scorer has no category input today
// and this change does not give it one.
//
// "Don't recommend this account" (2026-09-04, YouTube's "Don't recommend
// this channel") is the same endpoint with author_id instead of post_id:
// one row per (viewer, author) in feed_author_feedback (migration 007),
// enforced at the same hydration tail — every post by the author goes — and
// mirrored into the same ranker hash as the maximum author penalty.
// GET /v1/feed/feedback/authors lists the active mutes so the client can
// show and undo them. See RecordAuthorFeedback.

const (
	FeedbackInterested    = "interested"
	FeedbackNotInterested = "not_interested"
)

// ErrFeedbackPostNotFound: the post is not visible to this viewer (or does
// not exist) — post-service's batch endpoint returned nothing for it.
var ErrFeedbackPostNotFound = errors.New("post not found")

// ErrInvalidFeedbackSignal: signal is neither interested nor not_interested.
var ErrInvalidFeedbackSignal = errors.New("signal must be 'interested' or 'not_interested'")

// ErrOwnAuthorFeedback: a viewer cannot answer about their own account.
var ErrOwnAuthorFeedback = errors.New("cannot give feedback about your own account")

// feedbackStore is what the service needs from Postgres for this feature.
// *postgres.MetaStore implements it; tests substitute a map.
type feedbackStore interface {
	UpsertFeedback(ctx context.Context, f *postgres.PostFeedback) error
	ExcludedPostIDs(ctx context.Context, viewerID uuid.UUID, postIDs []uuid.UUID) (map[uuid.UUID]struct{}, error)
	// AuthorFeedbackNet: post-level net (+1/-1 per answered post) and
	// whether an author-level mute is active. See ranking.NetWithMute.
	AuthorFeedbackNet(ctx context.Context, viewerID, authorID uuid.UUID) (net int, muted bool, err error)

	// Author level — "Don't recommend this account" (migration 007).
	UpsertAuthorFeedback(ctx context.Context, f *postgres.AuthorFeedback) error
	MutedAuthorIDs(ctx context.Context, viewerID uuid.UUID) (map[uuid.UUID]struct{}, error)
	ListMutedAuthors(ctx context.Context, viewerID uuid.UUID) ([]postgres.MutedAuthor, error)
}

// ValidFeedbackSignal reports whether s is a signal RecordFeedback accepts.
func ValidFeedbackSignal(s string) bool {
	return s == FeedbackInterested || s == FeedbackNotInterested
}

// RecordFeedback stores the viewer's answer about a post and makes it take
// effect: the hydration cache row goes, and the author's net feedback is
// mirrored into the ranker's hash. Idempotent — the same answer twice is
// one row; a changed answer replaces the earlier one.
func (s *Service) RecordFeedback(ctx context.Context, viewerID, postID uuid.UUID, signal string) (*postgres.PostFeedback, error) {
	if !ValidFeedbackSignal(signal) {
		return nil, ErrInvalidFeedbackSignal
	}
	if s.feedback == nil {
		return nil, fmt.Errorf("feedback store not configured")
	}

	// One batch call, viewer-scoped, so the answer is about a post the
	// viewer can actually see and the author/category travel with the row.
	post, err := s.lookupPost(ctx, viewerID, postID)
	if err != nil {
		return nil, err
	}

	f := &postgres.PostFeedback{
		UserID:   viewerID,
		PostID:   postID,
		AuthorID: post.AuthorID,
		Category: post.Category,
		Signal:   signal,
	}
	if err := s.feedback.UpsertFeedback(ctx, f); err != nil {
		return nil, fmt.Errorf("store feedback: %w", err)
	}

	s.invalidateHydrationCache(ctx, viewerID, postID)
	s.mirrorAuthorFeedback(ctx, viewerID, post.AuthorID)
	return f, nil
}

// RecordAuthorFeedback is "Don't recommend this account": the viewer's
// answer about an AUTHOR rather than one post. not_interested mutes the
// author — every post of theirs is dropped from every surface at the
// hydration tail from the next fetch on, and the ranker treats them as the
// maximum author penalty; interested clears the mute and their posts come
// back. Latest answer wins, one row per (viewer, author). The viewer's own
// account is rejected. No existence check on the author: post-service is
// not consulted, and muting an unknown id is harmless.
func (s *Service) RecordAuthorFeedback(ctx context.Context, viewerID, authorID uuid.UUID, signal string) (*postgres.AuthorFeedback, error) {
	if !ValidFeedbackSignal(signal) {
		return nil, ErrInvalidFeedbackSignal
	}
	if authorID == viewerID {
		return nil, ErrOwnAuthorFeedback
	}
	if s.feedback == nil {
		return nil, fmt.Errorf("feedback store not configured")
	}
	f := &postgres.AuthorFeedback{UserID: viewerID, AuthorID: authorID, Signal: signal}
	if err := s.feedback.UpsertAuthorFeedback(ctx, f); err != nil {
		return nil, fmt.Errorf("store author feedback: %w", err)
	}
	s.invalidateViewerHydrationCache(ctx, viewerID)
	s.mirrorAuthorFeedback(ctx, viewerID, authorID)
	return f, nil
}

// ListMutedAuthors is GET /v1/feed/feedback/authors: the authors this
// viewer currently has "Don't recommend" on, newest first. Never nil.
func (s *Service) ListMutedAuthors(ctx context.Context, viewerID uuid.UUID) ([]postgres.MutedAuthor, error) {
	if s.feedback == nil {
		return nil, fmt.Errorf("feedback store not configured")
	}
	out, err := s.feedback.ListMutedAuthors(ctx, viewerID)
	if err != nil {
		return nil, fmt.Errorf("list muted authors: %w", err)
	}
	if out == nil {
		out = []postgres.MutedAuthor{}
	}
	return out, nil
}

// invalidateViewerHydrationCache drops EVERY hydrated row cached for the
// viewer (feed:hydrate:{viewer}:*). An author answer has no post id to
// target, and the cached rows carry no author-mute state, so the whole
// per-viewer set goes. Correctness does not rest on this: applyFeedbackFilter
// re-checks cached rows at the tail either way. It keeps the un-mute
// honest (the next page is rebuilt with fresh viewer state) and is
// best-effort — a SCAN bounded by a short timeout, errors logged.
func (s *Service) invalidateViewerHydrationCache(ctx context.Context, viewerID uuid.UUID) {
	if s.rdb == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	pattern := hydrationCacheKey(viewerID, uuid.Nil)
	pattern = pattern[:len(pattern)-len(uuid.Nil.String())] + "*"
	var cursor uint64
	for {
		keys, next, err := s.rdb.Scan(ctx, cursor, pattern, 500).Result()
		if err != nil {
			log.Printf("[feed-feedback] hydration cache SCAN failed for viewer %s: %v", viewerID, err)
			return
		}
		if len(keys) > 0 {
			if err := s.rdb.Del(ctx, keys...).Err(); err != nil {
				log.Printf("[feed-feedback] hydration cache DEL failed for viewer %s: %v", viewerID, err)
				return
			}
		}
		if next == 0 {
			return
		}
		cursor = next
	}
}

// invalidateHydrationCache drops the per-(viewer, post) hydrated row so the
// next page is rebuilt for it. The hydration-tail filter would drop a
// not_interested post either way; this keeps "interested" honest too, since
// the cached row carries the pre-answer viewer state.
func (s *Service) invalidateHydrationCache(ctx context.Context, viewerID, postID uuid.UUID) {
	if s.rdb == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if err := s.rdb.Del(ctx, hydrationCacheKey(viewerID, postID)).Err(); err != nil {
		log.Printf("[feed-feedback] hydration cache DEL failed: %v", err)
	}
}

// mirrorAuthorFeedback recomputes the viewer's net answer about the author
// from Postgres (the source of truth) and writes it to the ranker's hash.
// Recompute-and-set rather than increment, so a retried request cannot
// double count. Best-effort: the row is already stored; a Redis failure
// only delays the ranking nudge until the viewer's next answer.
func (s *Service) mirrorAuthorFeedback(ctx context.Context, viewerID, authorID uuid.UUID) {
	if s.rdb == nil {
		return
	}
	net, muted, err := s.feedback.AuthorFeedbackNet(ctx, viewerID, authorID)
	if err != nil {
		log.Printf("[feed-feedback] author net lookup failed: %v", err)
		return
	}
	mirrored := ranking.NetWithMute(float64(net), muted)
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if err := s.rdb.HSet(ctx, ranking.AuthorFeedbackKey(viewerID), authorID.String(), mirrored).Err(); err != nil {
		log.Printf("[feed-feedback] author feedback HSET failed: %v", err)
	}
}

// applyFeedbackFilter drops every hydrated post the viewer marked
// not_interested (or hid via POST /v1/feed/hide), and EVERY post by an
// author the viewer answered "Don't recommend this account" about. Runs at
// the hydration tail on every surface (home, reels, flicks, videos, watch
// all funnel through HydratePosts); FAILS CLOSED on a lookup error. A
// service built without a store (tests) passes through — that is a
// construction choice, not a lookup failure.
func (s *Service) applyFeedbackFilter(ctx context.Context, viewerID uuid.UUID, posts []HydratedPost) ([]HydratedPost, error) {
	if len(posts) == 0 || s.feedback == nil {
		return posts, nil
	}
	ids := make([]uuid.UUID, 0, len(posts))
	seen := make(map[uuid.UUID]struct{}, len(posts))
	for _, p := range posts {
		if _, ok := seen[p.ID]; ok {
			continue
		}
		seen[p.ID] = struct{}{}
		ids = append(ids, p.ID)
	}
	excluded, err := s.feedback.ExcludedPostIDs(ctx, viewerID, ids)
	if err != nil {
		return nil, fmt.Errorf("feed unavailable: feedback state could not be resolved: %w", err)
	}
	mutedAuthors, err := s.feedback.MutedAuthorIDs(ctx, viewerID)
	if err != nil {
		return nil, fmt.Errorf("feed unavailable: author feedback state could not be resolved: %w", err)
	}
	return filterExcludedPosts(posts, excluded, mutedAuthors), nil
}

// filterExcludedPosts is the pure step, mirroring applyBlockFilter. A post
// goes when its id is excluded, when its author is muted, or when the
// account that put it on this surface by reposting it is muted — a muted
// account must not reach the viewer through a repost either.
func filterExcludedPosts(posts []HydratedPost, excluded map[uuid.UUID]struct{}, mutedAuthors map[uuid.UUID]struct{}) []HydratedPost {
	if (len(excluded) == 0 && len(mutedAuthors) == 0) || len(posts) == 0 {
		return posts
	}
	out := posts[:0]
	for _, p := range posts {
		if _, drop := excluded[p.ID]; drop {
			continue
		}
		if _, drop := mutedAuthors[p.AuthorID]; drop {
			continue
		}
		if p.RepostedBy != nil {
			if _, drop := mutedAuthors[*p.RepostedBy]; drop {
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

// lookupPost fetches one post through post-service's viewer-scoped batch
// endpoint — the same call hydration makes, so "visible to this viewer"
// means the same thing on both paths. A post the batch omits is
// ErrFeedbackPostNotFound; a failed call is an error, never a guess.
func (s *Service) lookupPost(ctx context.Context, viewerID, postID uuid.UUID) (*HydratedPost, error) {
	reqBody, err := json.Marshal(map[string]any{
		"ids":       []string{postID.String()},
		"viewer_id": viewerID.String(),
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.postServiceURL+"/v1/posts/batch", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
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
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("post-service returned %d: %s", resp.StatusCode, string(b))
	}
	var envelope struct {
		Data map[string]HydratedPost `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("unmarshal batch response: %w", err)
	}
	post, ok := envelope.Data[postID.String()]
	if !ok {
		return nil, ErrFeedbackPostNotFound
	}
	return &post, nil
}
