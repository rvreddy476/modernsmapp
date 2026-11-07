// Enriching a ConversationResponse with per-member read cursors and
// display profiles, fetched from the user directory or user-service when
// the local cache misses.

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
)

// attachReadCursors fills each member's LastReadAt for this viewer.
//
// Disclosure follows exactly the rule the live `read_receipt` frame follows
// (MarkRead below): readReceiptVisible (receipt_ticks.go). Unknown policy,
// chat paused or "no_one" on EITHER side discloses nothing (reciprocity);
// "connections_only" is satisfied by this accepted conversation. Anything
// else would make a reload leak a receipt the live path had withheld.
func (s *Service) attachReadCursors(ctx context.Context, viewerID uuid.UUID, resp *ConversationResponse) {
	if resp == nil || len(resp.Members) == 0 {
		return
	}
	// DM consent: a request conversation discloses no receipts (the same
	// rule as the live frame in MarkRead).
	if resp.IsRequest {
		return
	}
	// Dating mechanic M9: in a gated dating conversation the viewer sees
	// receipts only while dating allows it.
	if meta, err := s.convStore.GetConversationMeta(ctx, resp.ID); err == nil && meta != nil && meta.ReceiptsGated &&
		!s.datingReceiptsAllowed(ctx, resp.ID, viewerID) {
		return
	}
	cursors, err := s.groupStore().GetConversationReadCursors(ctx, resp.ID)
	if err != nil {
		s.log.Warn("read cursor fetch failed", "err", err, "conversation_id", resp.ID)
		return
	}
	var viewerPolicy *ChatPolicy
	for i := range resp.Members {
		reader := resp.Members[i].UserID
		// The viewer's own cursor is not a receipt and is never withheld.
		if reader == viewerID {
			continue
		}
		rc, ok := cursors[reader]
		if !ok || rc.LastReadAt.IsZero() {
			continue
		}
		// One rule for every receipt path, reciprocity included
		// (readReceiptVisible, receipt_ticks.go).
		if viewerPolicy == nil {
			p := s.GetChatPolicy(ctx, viewerID)
			viewerPolicy = &p
		}
		if !readReceiptVisible(*viewerPolicy, s.GetChatPolicy(ctx, reader)) {
			continue
		}
		readAt := rc.LastReadAt
		resp.Members[i].LastReadAt = &readAt
	}
}

// enrichMembers batch-fetches user profiles and merges them with member data.
func (s *Service) enrichMembers(ctx context.Context, members []postgres.Member) []MemberWithProfile {
	userIDs := make([]uuid.UUID, len(members))
	for i, m := range members {
		userIDs[i] = m.UserID
	}

	profiles, err := s.convStore.GetUserProfiles(ctx, userIDs)
	if err != nil {
		s.log.Warn("failed to fetch user profiles for enrichment", "err", err)
		profiles = map[uuid.UUID]postgres.UserProfile{}
	}

	missingIDs := make([]uuid.UUID, 0, len(members))
	for _, m := range members {
		p, ok := profiles[m.UserID]
		if !ok || strings.TrimSpace(p.DisplayName) == "" {
			missingIDs = append(missingIDs, m.UserID)
		}
	}
	for id, profile := range s.fetchMissingProfiles(ctx, missingIDs) {
		profiles[id] = profile
	}

	out := make([]MemberWithProfile, len(members))
	for i, m := range members {
		mwp := MemberWithProfile{
			UserID:   m.UserID,
			Role:     m.Role,
			JoinedAt: m.JoinedAt,
		}
		if p, ok := profiles[m.UserID]; ok {
			mwp.DisplayName = p.DisplayName
			mwp.AvatarMediaID = p.AvatarMediaID
		}
		out[i] = mwp
	}
	return out
}

func (s *Service) fetchMissingProfiles(ctx context.Context, userIDs []uuid.UUID) map[uuid.UUID]postgres.UserProfile {
	if len(userIDs) == 0 || s.userServiceURL == "" {
		return map[uuid.UUID]postgres.UserProfile{}
	}

	fetched := make(map[uuid.UUID]postgres.UserProfile, len(userIDs))
	seen := make(map[uuid.UUID]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}

		profile, ok := s.fetchProfileFromUserService(ctx, userID)
		if !ok {
			continue
		}
		fetched[userID] = profile
		if err := s.convStore.UpsertUserProfile(ctx, userID, profile.DisplayName, profile.AvatarMediaID); err != nil {
			s.log.Warn("failed to cache user profile after fallback fetch", "user_id", userID, "err", err)
		}
	}
	return fetched
}

func (s *Service) fetchProfileFromUserService(ctx context.Context, userID uuid.UUID) (postgres.UserProfile, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/v1/users/%s", s.userServiceURL, userID), nil)
	if err != nil {
		s.log.Warn("failed to create user-service profile request", "user_id", userID, "err", err)
		return postgres.UserProfile{}, false
	}
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.log.Warn("user-service profile request failed", "user_id", userID, "err", err)
		return postgres.UserProfile{}, false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		s.log.Warn("user-service profile lookup returned non-200", "user_id", userID, "status", resp.StatusCode, "body", string(body))
		return postgres.UserProfile{}, false
	}

	var envelope struct {
		Data struct {
			DisplayName   string  `json:"display_name"`
			AvatarMediaID *string `json:"avatar_media_id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		s.log.Warn("failed to decode user-service profile response", "user_id", userID, "err", err)
		return postgres.UserProfile{}, false
	}

	displayName := strings.TrimSpace(envelope.Data.DisplayName)
	if displayName == "" {
		return postgres.UserProfile{}, false
	}

	var avatarMediaID *uuid.UUID
	if envelope.Data.AvatarMediaID != nil {
		if parsed, err := uuid.Parse(*envelope.Data.AvatarMediaID); err == nil {
			avatarMediaID = &parsed
		}
	}

	return postgres.UserProfile{
		UserID:        userID,
		DisplayName:   displayName,
		AvatarMediaID: avatarMediaID,
		UpdatedAt:     time.Now(),
	}, true
}
