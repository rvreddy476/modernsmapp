package service

import (
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

// For You source 4 (2026-11-13): notes carrying a hashtag the viewer
// follows. post-service owns hashtag follows (migration 074) and the note
// rows' hashtags, so it answers which recent live public top-level notes
// match, never the viewer's own:
//
//	GET {POST_SERVICE_URL}/internal/users/{viewer}/followed-hashtag-notes?since=&limit=
//	  -> {"data": {"note_ids": [...]}}
//
// Only ids echo_notes also records as live public top-level notes are kept
// (LiveNotesAmong, as the network source does), and the pool then passes
// the same own-note, block / mute and hidden-author filters as every other
// candidate; the page passes the keyword, privacy, listing and feedback
// filters at hydration. Best effort, like the network and rising sources.

const (
	// sourceEchoHashtag marks a note from a hashtag the viewer follows.
	sourceEchoHashtag = "echo_hashtag"
	// forYouHashtagWindow is how far back the hashtag source looks.
	forYouHashtagWindow = 7 * 24 * time.Hour
	// forYouHashtagRows bounds the ids read from post-service.
	forYouHashtagRows = 100
)

// forYouHashtagNotes is source 4. No post-service address: no source.
func (s *Service) forYouHashtagNotes(ctx context.Context, viewerID uuid.UUID) ([]FeedItem, error) {
	if s.postServiceURL == "" {
		return nil, nil
	}
	ids, err := s.fetchFollowedHashtagNoteIDs(ctx, viewerID, time.Now().Add(-forYouHashtagWindow), forYouHashtagRows)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	notes, err := s.notesMeta.LiveNotesAmong(ctx, ids)
	if err != nil {
		return nil, err
	}
	return noteRowsToItems(notes, sourceEchoHashtag), nil
}

// fetchFollowedHashtagNoteIDs reads post-service's internal route. A non-200,
// an unreachable post-service or a malformed body is an error, never "no
// notes".
func (s *Service) fetchFollowedHashtagNoteIDs(ctx context.Context, viewerID uuid.UUID, since time.Time, limit int) ([]uuid.UUID, error) {
	url := fmt.Sprintf("%s/internal/users/%s/followed-hashtag-notes?since=%s&limit=%d",
		strings.TrimRight(s.postServiceURL, "/"), viewerID, since.UTC().Format(time.RFC3339), limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	client := s.postFor(ctx)
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("post-service followed-hashtag notes request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("post-service followed-hashtag notes returned %d: %s", resp.StatusCode, string(body))
	}
	var envelope struct {
		Data struct {
			NoteIDs []string `json:"note_ids"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode followed-hashtag notes: %w", err)
	}
	out := make([]uuid.UUID, 0, len(envelope.Data.NoteIDs))
	for _, raw := range envelope.Data.NoteIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("followed-hashtag notes: bad id %q", raw)
		}
		out = append(out, id)
	}
	return out, nil
}
