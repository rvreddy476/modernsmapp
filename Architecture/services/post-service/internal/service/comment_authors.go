package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/post-service/internal/store/postgres"
)

// SetProfileServiceURL configures the identity-profile base URL used to
// hydrate comment authors. Empty disables hydration (comments go out with
// author_id only, as before).
func (s *Service) SetProfileServiceURL(url string) {
	s.profileServiceURL = url
}

// The wire shape of one profile in identity-profile's batch response —
// the same contract feed-service consumes for post authors.
type commentProfile struct {
	Username      string     `json:"username"`
	DisplayName   string     `json:"display_name"`
	AvatarMediaID *uuid.UUID `json:"avatar_media_id,omitempty"`
}

const commentAuthorHydrationTimeout = 3 * time.Second

// HydrateCommentAuthors fills each comment's (and inline reply's) Author from
// identity-profile's batch endpoint.
//
// BEST-EFFORT by design, unlike the feed's hydration: a profile-service blip
// must not take down a conversation, so on any failure the comments ship
// exactly as they did before this existed — author_id only — and the client
// renders its minimal fallback. A profile the batch does not return (deleted
// account) is named as such rather than left blank, so the absence reads as
// a fact instead of a rendering bug.
func (s *Service) HydrateCommentAuthors(ctx context.Context, viewerID *uuid.UUID, comments []postgres.Comment) {
	if s.profileServiceURL == "" || len(comments) == 0 {
		return
	}

	seen := make(map[uuid.UUID]bool)
	var ids []string
	collect := func(c *postgres.Comment) {
		if c != nil && c.AuthorID != uuid.Nil && !seen[c.AuthorID] {
			seen[c.AuthorID] = true
			ids = append(ids, c.AuthorID.String())
		}
	}
	for i := range comments {
		collect(&comments[i])
		collect(comments[i].Reply)
	}
	if len(ids) == 0 {
		return
	}

	profiles, err := s.fetchCommentProfiles(ctx, viewerID, ids)
	if err != nil {
		slog.WarnContext(ctx, "comment author hydration skipped", "err", err)
		return
	}

	apply := func(c *postgres.Comment) {
		if c == nil || c.AuthorID == uuid.Nil {
			return
		}
		author := &postgres.CommentAuthor{ID: c.AuthorID, DisplayName: "Deleted account"}
		if p, ok := profiles[c.AuthorID]; ok {
			author.Username = p.Username
			author.DisplayName = p.DisplayName
			author.AvatarMediaID = p.AvatarMediaID
		}
		c.Author = author
	}
	for i := range comments {
		apply(&comments[i])
		apply(comments[i].Reply)
	}
}

func (s *Service) fetchCommentProfiles(
	ctx context.Context,
	viewerID *uuid.UUID,
	ids []string,
) (map[uuid.UUID]commentProfile, error) {
	ctx, cancel := context.WithTimeout(ctx, commentAuthorHydrationTimeout)
	defer cancel()

	body, err := json.Marshal(map[string]any{"user_ids": ids})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(s.profileServiceURL, "/")+"/v1/profiles/batch", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if viewerID != nil {
		req.Header.Set("X-User-Id", viewerID.String())
	}
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("identity-profile returned %d: %s", resp.StatusCode, string(b))
	}

	profiles := make(map[uuid.UUID]commentProfile)
	if err := json.NewDecoder(resp.Body).Decode(&profiles); err != nil {
		return nil, fmt.Errorf("decode profile batch: %w", err)
	}
	return profiles, nil
}
