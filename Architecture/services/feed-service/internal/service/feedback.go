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

const (
	FeedbackInterested    = "interested"
	FeedbackNotInterested = "not_interested"
)

// ErrFeedbackPostNotFound: the post is not visible to this viewer (or does
// not exist) — post-service's batch endpoint returned nothing for it.
var ErrFeedbackPostNotFound = errors.New("post not found")

// ErrInvalidFeedbackSignal: signal is neither interested nor not_interested.
var ErrInvalidFeedbackSignal = errors.New("signal must be 'interested' or 'not_interested'")

// feedbackStore is what the service needs from Postgres for this feature.
// *postgres.MetaStore implements it; tests substitute a map.
type feedbackStore interface {
	UpsertFeedback(ctx context.Context, f *postgres.PostFeedback) error
	ExcludedPostIDs(ctx context.Context, viewerID uuid.UUID, postIDs []uuid.UUID) (map[uuid.UUID]struct{}, error)
	AuthorFeedbackNet(ctx context.Context, viewerID, authorID uuid.UUID) (int, error)
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
	net, err := s.feedback.AuthorFeedbackNet(ctx, viewerID, authorID)
	if err != nil {
		log.Printf("[feed-feedback] author net lookup failed: %v", err)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if err := s.rdb.HSet(ctx, ranking.AuthorFeedbackKey(viewerID), authorID.String(), net).Err(); err != nil {
		log.Printf("[feed-feedback] author feedback HSET failed: %v", err)
	}
}

// applyFeedbackFilter drops every hydrated post the viewer marked
// not_interested (or hid via POST /v1/feed/hide). Runs at the hydration
// tail on every surface; FAILS CLOSED on a lookup error. A service built
// without a store (tests) passes through — that is a construction choice,
// not a lookup failure.
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
	return filterExcludedPosts(posts, excluded), nil
}

// filterExcludedPosts is the pure step, mirroring applyBlockFilter.
func filterExcludedPosts(posts []HydratedPost, excluded map[uuid.UUID]struct{}) []HydratedPost {
	if len(excluded) == 0 || len(posts) == 0 {
		return posts
	}
	out := posts[:0]
	for _, p := range posts {
		if _, drop := excluded[p.ID]; drop {
			continue
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
