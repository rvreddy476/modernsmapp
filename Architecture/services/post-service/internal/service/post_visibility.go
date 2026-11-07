// post_visibility.go decides whether a viewer may see or engage with a
// post: the engagement visibility check, the single-post view gate
// (viewerMayViewPost), and the follow-graph and username lookups those
// gates call. Moved out of post.go unchanged.

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// checkEngagementVisibility enforces the post's visibility scope on
// engagement mutations (react / unreact / bookmark). Returns nil
// when the viewer is allowed to engage:
//
//   - the viewer is the author (always allowed)
//   - visibility == "public" (everyone)
//   - visibility == "followers" AND the viewer follows the author
//     (the retired "circle" is author-only)
//
// All other cases return ErrPostNotVisible. Graph errors fail closed:
// without a working relationship check we can't distinguish a
// follower from a stranger, so the engagement is rejected — the
// alternative (fail-open) was the exploit path called out by audit
// C5 ("engagement on private posts leaks counts via React").
func (s *Service) checkEngagementVisibility(ctx context.Context, postID, viewerID uuid.UUID) error {
	_, err := s.loadPostForEngagement(ctx, postID, viewerID)
	return err
}

// loadPostForEngagement is the shared "fetch + visibility-gate" helper
// behind every engagement endpoint. Audit H2: handlers used to
// double-fetch the post (handler did a GetPost to read NoComments /
// NoLikes flags, then the service called GetPost again inside
// checkEngagementVisibility). Now they share one fetch through this
// helper; callers can read the returned Post's NoComments / NoLikes
// without an extra DB round trip.
//
// Hot path; uses pgStore.GetPost which already has a Redis-cached
// path for repeat reads of the same post.
func (s *Service) loadPostForEngagement(ctx context.Context, postID, viewerID uuid.UUID) (*postgres.Post, error) {
	if s.engagementPostSeam != nil {
		return s.engagementPostSeam(ctx, postID, viewerID)
	}
	post, err := s.pgStore.GetPost(ctx, postID)
	if err != nil {
		return nil, err
	}
	if post == nil {
		return nil, ErrPostNotFound
	}
	// One visibility rule (2026-09-29): this used to restate
	// viewerMayViewPost without 'unlisted' and without the private share
	// list, so a viewer the detail let in (an unlisted link, a user a
	// private post is shared with) was refused every engagement — while
	// the routes gated by the detail's gate let them through. Same fail-
	// closed answers: an unknown value or a graph outage denies.
	if !s.viewerMayViewPost(ctx, post, &viewerID) {
		return nil, ErrPostNotVisible
	}
	return post, nil
}

// viewerMayViewPost applies the visibility policy to an ALREADY-LOADED post
// — the allocation-free twin of loadPostForEngagement's gate, for callers
// that hold the row. Same rules: authors always see their own, unknown
// values fail closed, a graph outage denies rather than leaks.
func (s *Service) viewerMayViewPost(ctx context.Context, post *postgres.Post, viewerID *uuid.UUID) bool {
	if viewerID != nil && *viewerID == post.AuthorID {
		return true
	}
	// Account-level gate first: a private account's posts are follower-only
	// whatever the per-post visibility says, and an anonymous viewer is a
	// stranger to every private account (privacy_gate.go, fail-closed).
	if !s.canViewAuthor(ctx, viewerID, post.AuthorID) {
		return false
	}
	switch strings.ToLower(post.Visibility) {
	case "", "public", "unlisted":
		return true
	case "private":
		// Private sharing (2026-09-28): a user on the post's share list.
		// This is a single-post gate only; no listing calls it.
		return s.sharedWithViewer(ctx, post.ID, viewerID)
	case "followers":
		if viewerID == nil {
			return false
		}
		follows, err := s.checkViewerFollowsAuthor(ctx, *viewerID, post.AuthorID)
		if err != nil {
			log.Printf("Warning: visibility check graph lookup failed; rejecting: %v", err)
			return false
		}
		return follows
	case PostVisibilityFriends:
		// Friends (2026-10-18): mutual follows only. Same fail-closed
		// answer as followers on a graph outage or an anonymous viewer.
		if viewerID == nil {
			return false
		}
		mutual, err := s.checkViewerMutualWithAuthor(ctx, *viewerID, post.AuthorID)
		if err != nil {
			log.Printf("Warning: friends visibility graph lookup failed; rejecting: %v", err)
			return false
		}
		return mutual
	default:
		// "circle" (and "trusted" / "close_friends") is author-only here as
		// on the media gate (evaluatePostMediaVisibility) since 2026-09-29:
		// the close-friends audience was retired on 21 Sep (graph-service
		// migration 012), no writer can set it (PostVisibilities), and the
		// old "treat it as followers" widened a close-friends post to every
		// follower. "staged", "private" without a share and unknown values
		// fail closed as before.
		return false
	}
}

// checkViewerFollowsAuthor does a single graph-service relationship
// lookup. Returns (follows=true) when viewer→author edge exists.
// Empty graphServiceURL is treated as "no policy" — same as the
// existing fetchFollowing helper — so unit tests + dev rigs without
// graph-service skip the gate cleanly.
func (s *Service) checkViewerFollowsAuthor(ctx context.Context, viewerID, authorID uuid.UUID) (bool, error) {
	follows, _, err := s.fetchFollowEdges(ctx, viewerID, authorID)
	return follows, err
}

// checkViewerMutualWithAuthor answers the `friends` audience: the viewer
// follows the author AND the author follows the viewer, from the same one
// graph lookup checkViewerFollowsAuthor makes. Empty graphServiceURL is
// "no policy" exactly as there (unit tests and dev rigs).
func (s *Service) checkViewerMutualWithAuthor(ctx context.Context, viewerID, authorID uuid.UUID) (bool, error) {
	follows, followedBy, err := s.fetchFollowEdges(ctx, viewerID, authorID)
	if err != nil {
		return false, err
	}
	return follows && followedBy, nil
}

// fetchFollowEdges is the one GET /v1/graph/relationship call behind both
// audience checks: follows = viewer→author, followedBy = author→viewer.
func (s *Service) fetchFollowEdges(ctx context.Context, viewerID, authorID uuid.UUID) (follows, followedBy bool, err error) {
	if s.graphServiceURL == "" {
		return true, true, nil
	}
	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	url := fmt.Sprintf(
		"%s/v1/graph/relationship?user_id=%s&other_id=%s",
		s.graphServiceURL, viewerID, authorID,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, false, fmt.Errorf("build relationship request: %w", err)
	}
	// graph-service gates /v1/graph/* behind the internal service key.
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, false, fmt.Errorf("relationship request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, false, fmt.Errorf("graph-service status %d", resp.StatusCode)
	}
	var envelope struct {
		Data struct {
			Follows    bool `json:"follows"`
			FollowedBy bool `json:"followed_by"`
		} `json:"data"`
		Follows    bool `json:"follows"`     // legacy un-wrapped shape tolerated
		FollowedBy bool `json:"followed_by"` // legacy un-wrapped shape tolerated
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return false, false, fmt.Errorf("decode relationship: %w", err)
	}
	return envelope.Data.Follows || envelope.Follows, envelope.Data.FollowedBy || envelope.FollowedBy, nil
}

func (s *Service) fetchFollowing(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	if s.graphServiceURL == "" {
		return nil, nil
	}

	var allFollowing []uuid.UUID
	offset := 0
	limit := 100

	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}

	for {
		url := fmt.Sprintf(
			"%s/v1/graph/following/%s?limit=%d&offset=%d",
			s.graphServiceURL,
			userID.String(),
			limit,
			offset,
		)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("create following request: %w", err)
		}
		// graph-service gates /v1/graph/* behind the internal service key.
		if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
			req.Header.Set("X-Internal-Service-Key", key)
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("graph-service request failed: %w", err)
		}

		var envelope struct {
			Data []uuid.UUID `json:"data"`
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&envelope)
		resp.Body.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("decode following response: %w", decodeErr)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("graph-service returned %d", resp.StatusCode)
		}

		allFollowing = append(allFollowing, envelope.Data...)
		if len(envelope.Data) < limit {
			break
		}
		offset += limit
	}

	return allFollowing, nil
}

// lookupUserByUsername resolves a username to a user ID via the app
// user-service (`GET /v1/users/by-username/{username}`), the same service the
// api-gateway routes `/v1/users` to. app.users.username is the authority for
// handles; identity-profile is a different store and 404s for app handles.
//
// The internal key is sent so the call keeps working if the route is ever
// gated. The reply is the standard envelope `{"data":{"id":"<uuid>",...}}`.
// Returns ("", nil) when the URL is unset or the user does not exist, so
// callers skip the mention without treating it as an error.
func (s *Service) lookupUserByUsername(ctx context.Context, username string) (string, error) {
	if s.userServiceURL == "" {
		return "", nil
	}
	reqURL := fmt.Sprintf("%s/v1/users/by-username/%s",
		strings.TrimRight(s.userServiceURL, "/"), url.PathEscape(username))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", err
	}
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return "", nil
	default:
		return "", fmt.Errorf("user-service by-username returned %d", resp.StatusCode)
	}
	var result struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode user-service by-username: %w", err)
	}
	return result.Data.ID, nil
}
