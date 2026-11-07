package service

import (
	"context"
	"log"

	"github.com/atpost/suggestion-service/internal/store"
	"github.com/google/uuid"
)

// The one egress filter (audit SG-M2, SG-L3).
//
// Every suggestion surface returns through filterEgress, so no pool and no
// path (cache hit, stored candidates, popular fallback, interstitial) can hand
// back an account the viewer must not see:
//
//   - blocked in either direction, or muted (filterBlocked, graph-service);
//   - hidden: deactivated or scheduled for deletion (hidden_users). Four
//     pools filtered this in SQL and six did not — location, profession,
//     groups, life entries, popular and trending — so a deactivated account
//     was suggested for the 30 days before purge;
//   - under one of the viewer's own cooldowns (hide, dismiss, decline, block,
//     a sent message request). The popular fallback ignored them, so a
//     hidden card came back on the next request despite its 30-day cooldown.
//
// Each check fails closed: if it cannot run, the surface is empty, never
// unfiltered.

// egressReader is the slice of the store the filter reads.
type egressReader interface {
	HiddenAmong(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]struct{}, error)
	GetActiveCooldowns(ctx context.Context, viewerID uuid.UUID) (map[uuid.UUID]store.CooldownEntry, error)
}

func (s *Service) filterEgress(ctx context.Context, viewerID uuid.UUID, resp *SuggestionsResponse) *SuggestionsResponse {
	resp = s.filterBlocked(ctx, viewerID, resp)
	if resp == nil || len(resp.Items) == 0 {
		return resp
	}
	empty := func(why string, err error) *SuggestionsResponse {
		log.Printf("[suggestions] %s for viewer %s: %v — returning no suggestions rather than an unfiltered list",
			why, viewerID, err)
		resp.Items = []SuggestionItem{}
		resp.NextCursor = ""
		return resp
	}
	if s.egress == nil {
		return empty("egress filter not configured", nil)
	}

	// filterBlocked has already dropped every unparseable id.
	ids := make([]uuid.UUID, 0, len(resp.Items))
	for _, item := range resp.Items {
		ids = append(ids, uuid.MustParse(item.CandidateUserID))
	}
	hidden, err := s.egress.HiddenAmong(ctx, ids)
	if err != nil {
		return empty("hidden-account lookup failed", err)
	}
	cooldowns, err := s.egress.GetActiveCooldowns(ctx, viewerID)
	if err != nil {
		return empty("cooldown lookup failed", err)
	}

	kept := make([]SuggestionItem, 0, len(resp.Items))
	for i, item := range resp.Items {
		id := ids[i]
		if id == viewerID {
			continue
		}
		if _, ok := hidden[id]; ok {
			continue
		}
		if _, ok := cooldowns[id]; ok {
			continue
		}
		kept = append(kept, item)
	}
	resp.Items = kept
	return resp
}
