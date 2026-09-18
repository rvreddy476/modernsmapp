package jobs

import (
	"context"
	"log/slog"

	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

// partnerUserIDs resolves the captain USER id of every subscription's
// partner: the partner_user_id every partner-facing event carries, which is
// what notification-service addresses the push by. A failed lookup leaves
// the ids empty; the event still names partner_id (notification-service
// falls back to it with a warning).
func partnerUserIDs(ctx context.Context, st *store.Store, subs []store.PartnerSubscription) map[uuid.UUID]string {
	out := map[uuid.UUID]string{}
	if len(subs) == 0 {
		return out
	}
	ids := make([]uuid.UUID, 0, len(subs))
	seen := map[uuid.UUID]bool{}
	for i := range subs {
		if !seen[subs[i].PartnerID] {
			seen[subs[i].PartnerID] = true
			ids = append(ids, subs[i].PartnerID)
		}
	}
	m, err := st.PartnerUserIDs(ctx, ids)
	if err != nil {
		slog.Warn("rider job: partner user id lookup failed", "error", err)
		return out
	}
	for k, v := range m {
		out[k] = v.String()
	}
	return out
}
