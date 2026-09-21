package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
)

// canAddToGroup resolves the target's who_can_add_to_groups policy — and,
// more importantly, the block relationship — through graph-service.
//
// WHY THIS EXISTS
//
// Social group invites previously made no permission call of any kind, so a
// blocked user could be invited into a group by the person they had blocked.
// Block is supposed to be authoritative across every private surface
// (messaging, calls, follows, connections); group invites were the one hole.
// Chat groups already close it the same way, in
// chat-service/services/message-service/internal/service/groups.go
// (checkGroupAddPermission) — this is deliberately the same shape so the two
// cannot drift into different answers for the same pair.
//
// Returns (directAdd, invite). Both false means the target cannot be brought
// in at all.
//
// UNKNOWN STATE FAILS CLOSED. A missing URL, a transport error, a non-200 or
// an unparseable body all return false. Refusing an invite that should have
// been allowed is a visible annoyance; allowing one that should have been
// refused defeats a block, which is a safety guarantee.
func (s *Service) canAddToGroup(ctx context.Context, actorID, targetID uuid.UUID) (directAdd bool, invite bool) {
	if s.graphServiceURL == "" {
		slog.Warn("GRAPH_SERVICE_URL not configured — group invites fail closed")
		return false, false
	}

	url := fmt.Sprintf("%s/v1/permissions/check?target_user_id=%s&actions=add_to_group",
		s.graphServiceURL, targetID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, false
	}
	req.Header.Set("X-User-Id", actorID.String())
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}

	resp, err := s.graphClient.Do(req)
	if err != nil {
		slog.Warn("group-invite permission check failed — failing closed",
			"error", err, "target", targetID)
		return false, false
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		slog.Warn("group-invite permission check non-200 — failing closed",
			"status", resp.StatusCode, "target", targetID)
		return false, false
	}

	var envelope struct {
		Data struct {
			Decisions struct {
				AddToGroup struct {
					Allowed  bool   `json:"allowed"`
					Fallback string `json:"fallback"`
				} `json:"add_to_group"`
			} `json:"decisions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		slog.Warn("group-invite permission check unparseable — failing closed", "target", targetID)
		return false, false
	}

	d := envelope.Data.Decisions.AddToGroup
	return d.Allowed, d.Fallback == "group_invitation"
}

// ErrInviteNotPermitted is returned when the target's privacy settings or a
// block prevent the invite. The message is deliberately generic: telling the
// inviter "they blocked you" would turn an invite into a block oracle.
var ErrInviteNotPermitted = fmt.Errorf("forbidden: this user cannot be invited to the group")
