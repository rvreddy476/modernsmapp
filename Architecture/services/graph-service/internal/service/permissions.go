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
	return permission.Facts{
		Blocked:            full.Blocked || actorBlockedTarget,
		IsConnection:       full.IsConnection,
		ActorFollowsTarget: full.Follows,
		TargetFollowsActor: full.FollowedBy,
	}, nil
}

// strictPrivacyDefaults is the fail-safe privacy posture used when the target's
// real settings cannot be fetched — it never silently opens messaging up.
func strictPrivacyDefaults() permission.Privacy {
	return permission.Privacy{
		WhoCanMessage:               "connections_only",
		WhoCanCall:                  "connections_only",
		WhoCanAddToGroups:           "connections_only",
		WhoCanSendConnectionRequest: "friends_of_friends_or_contacts",
		WhoCanSeeOnlineStatus:       "connections_only",
		WhoCanSeeReadReceipts:       "connections_only",
		WhoCanSeeProfilePhoto:       "everyone",
	}
}

// fetchPrivacy returns the target's privacy settings, cached in Redis for 60s
// (spec §6.2 freshness budget). Any fetch failure falls back to strict
// defaults rather than failing the permission check.
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
		s.rdb.Set(ctx, cacheKey, data, 60*time.Second)
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
			WhoCanSeeProfilePhoto       string `json:"who_can_see_profile_photo"`
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
		WhoCanSeeProfilePhoto:       envelope.Data.WhoCanSeeProfilePhoto,
	}, nil
}
