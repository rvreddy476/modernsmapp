package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// Module 3 M3-P0-8 / SR-7 — block safety on suggestions.
//
// THE DEFECT
//
// suggestion-service filtered NOTHING for blocks. The comment in
// GetSuggestions said:
//
//	// 4.5. Filter out existing friends, blocked users, and self
//
// and the code below it built a set from GetFriendIDs and the viewer's own id.
// There was no block lookup at all — not in the candidate path, not in the
// popular-users fallback, not in the interstitial path.
//
// The Kafka consumer did react to a `user.blocked` event by removing stored
// candidates and dropping the cache. That is a repair, not a guarantee: it
// depends on an event arriving. Until Module 3 SR-2 the block event was
// published from a fire-and-forget goroutine, so it could simply be lost — and
// even now it is asynchronous, so there is a window in which the candidate row
// still exists.
//
// AND THE CACHE HIT RETURNED FIRST
//
// The worst part is the ordering. GetSuggestions checks Redis before anything
// else and returns the cached list directly. So even a correct filter applied
// during generation would be bypassed on every cache hit: a block landing
// after the cache was written had no effect until the entry expired.
//
// The user-visible result is the platform recommending that you follow someone
// you blocked, on the "People you may know" surface, repeatedly.
//
// THE FIX
//
// Filter at EGRESS. Every path that returns suggestions runs through
// filterBlocked, including the cache-hit path, so the guarantee does not
// depend on which branch produced the list or on an event having arrived.
//
// FAIL CLOSED
//
// If the block set cannot be fetched, the surface returns EMPTY. An empty
// "People you may know" is a cosmetic degradation for the duration of an
// incident. Showing an unfiltered list puts someone in front of the account
// they blocked, which is the harm this exists to prevent.

// BlockLookup returns the set of users the viewer must never be shown.
type BlockLookup interface {
	// BlockedSet returns every user id blocked in either direction, plus the
	// viewer's mutes. Symmetric for blocks by contract (Module 2 M2-P0-3).
	BlockedSet(ctx context.Context, viewerID uuid.UUID) (map[uuid.UUID]struct{}, error)
}

// GraphBlockLookup reads the canonical set from graph-service.
type GraphBlockLookup struct {
	baseURL     string
	internalKey string
	client      *http.Client
}

func NewGraphBlockLookup(baseURL, internalKey string) *GraphBlockLookup {
	return &GraphBlockLookup{
		baseURL:     baseURL,
		internalKey: internalKey,
		client:      &http.Client{Timeout: 2 * time.Second},
	}
}

func (g *GraphBlockLookup) BlockedSet(ctx context.Context, viewerID uuid.UUID) (map[uuid.UUID]struct{}, error) {
	url := fmt.Sprintf("%s/v1/graph/blocked-and-muted?user_id=%s", g.baseURL, viewerID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if g.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", g.internalKey)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("graph-service unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("graph-service blocked-and-muted returned %d", resp.StatusCode)
	}

	var body struct {
		UserIDs []uuid.UUID `json:"user_ids"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode blocked-and-muted: %w", err)
	}
	set := make(map[uuid.UUID]struct{}, len(body.UserIDs))
	for _, id := range body.UserIDs {
		set[id] = struct{}{}
	}
	return set, nil
}

// filterBlocked removes every blocked candidate from a response.
//
// This is the single egress point. It runs on the cache-hit path, the
// candidate path, the popular-users fallback and the interstitial path, so no
// branch can return an unfiltered list.
func (s *Service) filterBlocked(ctx context.Context, viewerID uuid.UUID, resp *SuggestionsResponse) *SuggestionsResponse {
	if resp == nil || len(resp.Items) == 0 {
		return resp
	}

	if s.blocks == nil {
		// Not configured means block safety is not running. Returning the
		// unfiltered list would suggest accounts the viewer blocked.
		log.Printf("[suggestions] BLOCK LOOKUP NOT CONFIGURED — returning no suggestions " +
			"rather than an unfiltered list")
		resp.Items = []SuggestionItem{}
		resp.NextCursor = ""
		return resp
	}

	blocked, err := s.blocks.BlockedSet(ctx, viewerID)
	if err != nil {
		log.Printf("[suggestions] block lookup failed for viewer %s: %v — returning no "+
			"suggestions rather than an unfiltered list", viewerID, err)
		resp.Items = []SuggestionItem{}
		resp.NextCursor = ""
		return resp
	}
	if len(blocked) == 0 {
		return resp
	}

	kept := make([]SuggestionItem, 0, len(resp.Items))
	for _, item := range resp.Items {
		id, err := uuid.Parse(item.CandidateUserID)
		if err != nil {
			// An unparseable candidate id cannot be checked against the block
			// set, so it is dropped. A candidate that cannot be verified is
			// not safe to show.
			log.Printf("[suggestions] dropping candidate with unparseable id %q", item.CandidateUserID)
			continue
		}
		if _, isBlocked := blocked[id]; isBlocked {
			continue
		}
		kept = append(kept, item)
	}
	resp.Items = kept
	return resp
}
