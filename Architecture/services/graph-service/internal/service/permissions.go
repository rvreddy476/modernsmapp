package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/atpost/graph-service/internal/permission"
	"github.com/google/uuid"
)

// ResolvePermissions gathers the relationship snapshot between actor and
// target plus the target's privacy settings, then resolves every requested
// action through the §4 permission matrix.
func (s *Service) ResolvePermissions(ctx context.Context, actorID, targetID uuid.UUID, actions []permission.Action) (map[permission.Action]permission.Decision, error) {
	facts, err := s.permissionFacts(ctx, actorID, targetID)
	if err != nil {
		return nil, err
	}
	privacy := s.fetchPrivacy(ctx, targetID)
	return permission.ResolveAll(actions, facts, privacy), nil
}

// permissionFacts assembles the relationship snapshot. GetRelationshipFull
// reports a block only in the target→actor direction, so the actor→target
// direction is checked separately — §4 treats a block either way as fatal.
func (s *Service) permissionFacts(ctx context.Context, actorID, targetID uuid.UUID) (permission.Facts, error) {
	full, err := s.store.GetRelationshipFull(ctx, actorID, targetID)
	if err != nil {
		return permission.Facts{}, err
	}
	actorBlockedTarget, err := s.store.CheckBlock(ctx, actorID, targetID)
	if err != nil {
		return permission.Facts{}, err
	}
	// Second degree matters only when the pair is NOT directly connected —
	// skip the extra EXISTS for the common connected case. A lookup failure
	// fails toward "not second degree" (the strictly safer answer for every
	// consumer of this fact), never toward granting.
	secondDegree := false
	if !full.IsConnection && !full.Blocked && !actorBlockedTarget {
		secondDegree, err = s.store.CheckSecondDegree(ctx, actorID, targetID)
		if err != nil {
			log.Printf("[graph] second-degree check failed for %s->%s, treating as false: %v", actorID, targetID, err)
			secondDegree = false
		}
	}
	return permission.Facts{
		Blocked:            full.Blocked || actorBlockedTarget,
		IsConnection:       full.IsConnection,
		ActorFollowsTarget: full.Follows,
		TargetFollowsActor: full.FollowedBy,
		SecondDegree:       secondDegree,
	}, nil
}

// strictPrivacyDefaults is the fail-safe privacy posture used when the target's
// real settings cannot be fetched — it never silently opens messaging up.
func strictPrivacyDefaults() permission.Privacy {
	return permission.Privacy{
		// P0-2 correction: chat actions and presence disclosure DENY outright
		// while the target's real settings are unreadable. The previous
		// connections_only posture let an existing connection open a chat and
		// read online/receipt state during an identity outage even when the
		// target's real setting was no_one or paused — an unknown setting is
		// not a licence to act on the most permissive plausible one.
		WhoCanMessage:               "no_one",
		WhoCanCall:                  "no_one",
		WhoCanAddToGroups:           "no_one",
		WhoCanSendConnectionRequest: "friends_of_friends_or_contacts",
		WhoCanSeeOnlineStatus:       "no_one",
		WhoCanSeeReadReceipts:       "no_one",
		WhoCanSeeLastSeen:           "no_one",
		WhoCanSeeProfilePhoto:       "everyone",
		// NOT 'paused': the no_one values above already deny every chat
		// action; 'paused' would additionally masquerade as a state the user
		// chose. This stays 'enabled' purely so the pause-specific reason
		// codes remain truthful.
		ChatAvailability: "enabled",
	}
}

// PrivacyCacheTTL is how stale a cached privacy record may be.
//
// It was 60 seconds, matching the spec's §6.2 freshness budget, and there is
// no settings-invalidation consumer here — nothing tells graph-service that a
// user just changed who_can_message. Sixty seconds of that is a full minute in
// which someone who has just set themselves to `no_one` still receives direct
// messages, and a minute in which someone who has just opened up is still told
// they cannot be reached. The first of those is a privacy failure, not a
// staleness inconvenience.
//
// Three seconds is the freshness bound this service now documents and the
// closure proof measures against. It costs one extra internal GET per target
// per three seconds under load, which is a cache on a single-row read behind
// the internal key — cheap next to enforcing a privacy setting the user
// believes is already in effect.
//
// If this becomes a load problem, the answer is an invalidation channel keyed
// on the settings write, not a longer TTL: the TTL is the fallback for when
// invalidation is missed, and its length is the size of the privacy hole.
const PrivacyCacheTTL = 3 * time.Second

// fetchPrivacy returns the target's privacy settings, cached in Redis for
// [PrivacyCacheTTL]. Any fetch failure falls back to strict defaults rather
// than failing the permission check — a dependency outage must not open
// messaging up.
func (s *Service) fetchPrivacy(ctx context.Context, userID uuid.UUID) permission.Privacy {
	cacheKey := "privacy:" + userID.String()
	if val, err := s.rdb.Get(ctx, cacheKey).Result(); err == nil {
		var p permission.Privacy
		if json.Unmarshal([]byte(val), &p) == nil {
			return p
		}
	}

	if s.userServiceURL == "" {
		return strictPrivacyDefaults()
	}

	p, err := s.fetchPrivacyFromUserService(ctx, userID)
	if err != nil {
		log.Printf("[graph] privacy fetch failed for %s, using strict defaults: %v", userID, err)
		return strictPrivacyDefaults()
	}

	if data, err := json.Marshal(p); err == nil {
		s.rdb.Set(ctx, cacheKey, data, PrivacyCacheTTL)
	}
	return p
}

func (s *Service) fetchPrivacyFromUserService(ctx context.Context, userID uuid.UUID) (permission.Privacy, error) {
	url := fmt.Sprintf("%s/v1/users/%s/settings", s.userServiceURL, userID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return permission.Privacy{}, err
	}
	if s.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalKey)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return permission.Privacy{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return permission.Privacy{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return permission.Privacy{}, fmt.Errorf("user-service returned %d: %s", resp.StatusCode, string(body))
	}

	var envelope struct {
		Data struct {
			WhoCanMessage               string `json:"who_can_message"`
			WhoCanCall                  string `json:"who_can_call"`
			WhoCanAddToGroups           string `json:"who_can_add_to_groups"`
			WhoCanSendConnectionRequest string `json:"who_can_send_connection_request"`
			WhoCanSeeOnlineStatus       string `json:"who_can_see_online_status"`
			WhoCanSeeReadReceipts       string `json:"who_can_see_read_receipts"`
			WhoCanSeeLastSeen           string `json:"who_can_see_last_seen"`
			WhoCanSeeProfilePhoto       string `json:"who_can_see_profile_photo"`
			ChatAvailability            string `json:"chat_availability"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return permission.Privacy{}, fmt.Errorf("decode privacy response: %w", err)
	}
	return permission.Privacy{
		WhoCanMessage:               envelope.Data.WhoCanMessage,
		WhoCanCall:                  envelope.Data.WhoCanCall,
		WhoCanAddToGroups:           envelope.Data.WhoCanAddToGroups,
		WhoCanSendConnectionRequest: envelope.Data.WhoCanSendConnectionRequest,
		WhoCanSeeOnlineStatus:       envelope.Data.WhoCanSeeOnlineStatus,
		WhoCanSeeReadReceipts:       envelope.Data.WhoCanSeeReadReceipts,
		WhoCanSeeLastSeen:           envelope.Data.WhoCanSeeLastSeen,
		WhoCanSeeProfilePhoto:       envelope.Data.WhoCanSeeProfilePhoto,
		ChatAvailability:            envelope.Data.ChatAvailability,
	}, nil
}
