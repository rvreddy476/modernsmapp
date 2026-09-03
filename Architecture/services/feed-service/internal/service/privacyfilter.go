package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Private accounts on every feed surface.
//
// Fanout writes recipient timelines from the follow graph AT PUBLISH TIME.
// Nothing rewrites those rows when an author later flips to private, or
// when a follower is removed, so a timeline can hold posts the viewer may no
// longer read. post-service's batch endpoint now refuses those rows, but
// this service also fronts that endpoint with a five-minute per-viewer
// hydration cache — so a filter at the hydration tail is the only place
// where a cached row cannot slip through.
//
// This runs alongside the keyword step in HydratePosts, after the block
// filter, on every surface (home, reels, flicks, videos, watch) because they
// all funnel through the same tail. Same policy as block/mute and keywords
// (M2-P0-3, blocksafety_test.go): FAIL CLOSED. An unresolved answer is an
// error and the surface answers with an error, never an unfiltered page.
//
// The authority is graph-service's POST /v1/internal/graph/can — the §4
// permission matrix over the author's CURRENT account_visibility and the
// live follow graph. viewer==author is always allowed and is not even sent.

// authorPrivacyCacheTTL is deliberately short: this decides whether a
// private account's posts render, so staleness is measured in "how long
// after going private can a removed follower still scroll me".
const authorPrivacyCacheTTL = 3 * time.Second

// graphCanBatchLimit mirrors graph-service's per-call ceiling (400
// BATCH_TOO_LARGE above it).
const graphCanBatchLimit = 100

type authorPrivacyEntry struct {
	allowed bool
	expires time.Time
}

func authorPrivacyKey(viewerID, authorID uuid.UUID) string {
	return viewerID.String() + "|" + authorID.String()
}

func (s *Service) privacyClock() time.Time {
	if s.apNow != nil {
		return s.apNow()
	}
	return time.Now()
}

// applyAuthorPrivacyFilter drops every hydrated post whose ORIGINAL author
// the viewer may not read. Reposts are judged by the original author: a
// repost of a private account's post is that post, re-shared.
func (s *Service) applyAuthorPrivacyFilter(ctx context.Context, viewerID uuid.UUID, posts []HydratedPost) ([]HydratedPost, error) {
	if len(posts) == 0 {
		return posts, nil
	}
	seen := make(map[uuid.UUID]bool, len(posts))
	authors := make([]uuid.UUID, 0, len(posts))
	for _, p := range posts {
		if p.AuthorID == viewerID || p.AuthorID == uuid.Nil || seen[p.AuthorID] {
			continue
		}
		seen[p.AuthorID] = true
		authors = append(authors, p.AuthorID)
	}
	allowed, err := s.canViewAuthors(ctx, viewerID, authors)
	if err != nil {
		return nil, fmt.Errorf("feed unavailable: author privacy could not be resolved: %w", err)
	}
	return filterPostsByAuthorPrivacy(posts, viewerID, allowed), nil
}

// filterPostsByAuthorPrivacy is the pure step, split out for tests. An
// author absent from `allowed` is dropped — absence is not permission.
func filterPostsByAuthorPrivacy(posts []HydratedPost, viewerID uuid.UUID, allowed map[uuid.UUID]bool) []HydratedPost {
	out := posts[:0]
	for _, p := range posts {
		if p.AuthorID != viewerID && !allowed[p.AuthorID] {
			continue
		}
		out = append(out, p)
	}
	return out
}

// canViewAuthors resolves view_posts for every author, serving fresh
// (viewer, author) answers from the 3s in-process cache and batching the
// rest at the graph limit. Errors are NOT cached: the next request retries
// rather than serving three seconds of denial after a blip.
func (s *Service) canViewAuthors(ctx context.Context, viewerID uuid.UUID, authors []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := make(map[uuid.UUID]bool, len(authors))
	if len(authors) == 0 {
		return out, nil
	}
	now := s.privacyClock()

	var pending []uuid.UUID
	s.apMu.Lock()
	for _, a := range authors {
		if e, ok := s.apCache[authorPrivacyKey(viewerID, a)]; ok && now.Before(e.expires) {
			out[a] = e.allowed
			continue
		}
		pending = append(pending, a)
	}
	s.apMu.Unlock()

	for start := 0; start < len(pending); start += graphCanBatchLimit {
		end := start + graphCanBatchLimit
		if end > len(pending) {
			end = len(pending)
		}
		chunk := pending[start:end]
		answers, err := s.fetchCanViewPosts(ctx, viewerID, chunk)
		if err != nil {
			return nil, err
		}
		s.apMu.Lock()
		if s.apCache == nil {
			s.apCache = make(map[string]authorPrivacyEntry)
		}
		for _, a := range chunk {
			allowed, answered := answers[a.String()]
			if !answered {
				// A target the graph did not answer for is unresolved — the
				// same rule as the story relationship batch. Deny, and do not
				// cache a guess.
				out[a] = false
				continue
			}
			out[a] = allowed
			s.apCache[authorPrivacyKey(viewerID, a)] = authorPrivacyEntry{allowed: allowed, expires: now.Add(authorPrivacyCacheTTL)}
		}
		if len(s.apCache) > 50000 {
			for k, e := range s.apCache {
				if now.After(e.expires) {
					delete(s.apCache, k)
				}
			}
		}
		s.apMu.Unlock()
	}
	return out, nil
}

// fetchCanViewPosts is one call to graph-service's can endpoint. Any
// non-200, transport failure or malformed body is an error — an empty map
// must be a positive answer, never a decode accident (same reasoning as
// getBlockedAndMuted).
func (s *Service) fetchCanViewPosts(ctx context.Context, viewerID uuid.UUID, authors []uuid.UUID) (map[string]bool, error) {
	ids := make([]string, len(authors))
	for i, a := range authors {
		ids[i] = a.String()
	}
	body, err := json.Marshal(map[string]any{
		"viewer_id":  viewerID.String(),
		"action":     "view_posts",
		"target_ids": ids,
	})
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(s.graphURL, "/") + "/v1/internal/graph/can"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	client := s.graphClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("graph-service can request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("graph-service can returned %d: %s", resp.StatusCode, string(b))
	}
	var envelope struct {
		Data map[string]bool `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode graph-service can: %w", err)
	}
	if envelope.Data == nil {
		envelope.Data = map[string]bool{}
	}
	return envelope.Data, nil
}
