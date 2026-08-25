package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Production chat pass — the local privacy-policy projection (directive §5.1).
//
// The HOT paths (send, typing, mark-read) must not call identity or graph
// over HTTP per message. They read chat.user_policy, a projected snapshot of
// the chat-relevant settings, which is:
//
//   - populated LAZILY: a miss fetches the canonical snapshot from identity
//     user-service once and stores it;
//   - refreshed when older than policyMaxAge (the TTL fallback);
//   - invalidated EAGERLY by the user.settings_changed event (the identity
//     consumer deletes the row, so the next read re-fetches).
//
// Failure posture (P0-1 correction, tightened by the re-verification): a
// fetch failure with NO projected row — or with only a row past
// policyStaleGrace — returns `Known=false`, and every caller fails CLOSED:
// disclosure (typing, receipts) stays silent, and sends into existing
// conversations are refused alongside creation. The projection row in
// Postgres carries availability through a SHORT identity outage; it is not a
// licence to keep acting on settings the user may have changed.

// policyMaxAge is the TTL fallback for a lost invalidation event.
const policyMaxAge = 5 * time.Minute

// policyStaleGrace bounds the stale-row fallback when the authority is
// unreachable. The re-verification showed the previous 24h bound was itself
// the privacy hole: a missed pause invalidation plus an identity outage kept
// a pre-pause "enabled" row authoritative for almost a day. Fifteen minutes
// is the privacy budget now — it rides out a deploy or a brief outage, and
// beyond it a stale row is treated as unknown and the caller fails closed.
// Availability degrades only when the authority is down LONGER than this AND
// the row is older than policyMaxAge; privacy exposure from a missed event
// is bounded to these minutes, never hours.
const policyStaleGrace = 15 * time.Minute

// ChatPolicy is the resolved per-user policy the hot paths consult.
type ChatPolicy struct {
	// Known is false when neither a projection nor a live fetch was
	// available. Every field below is then a conservative default.
	Known                  bool
	ChatPaused             bool
	SendTypingIndicators   bool
	ReadReceiptsVisibility string
}

type policyStore interface {
	GetUserPolicy(ctx context.Context, userID uuid.UUID) (*postgres.UserPolicy, error)
	UpsertUserPolicy(ctx context.Context, p postgres.UserPolicy) error
	InvalidateUserPolicy(ctx context.Context, userID uuid.UUID) error
}

func (s *Service) policyStore() policyStore {
	return s.convStore.(policyStore)
}

// GetChatPolicy resolves the user's chat policy from the projection,
// refreshing from the identity authority when missing or stale.
func (s *Service) GetChatPolicy(ctx context.Context, userID uuid.UUID) ChatPolicy {
	row, err := s.policyStore().GetUserPolicy(ctx, userID)
	if err != nil {
		s.log.Warn("policy projection read failed", "err", err, "user_id", userID)
		row = nil
	}
	if row != nil && time.Since(row.RefreshedAt) < policyMaxAge {
		return ChatPolicy{
			Known:                  true,
			ChatPaused:             row.ChatPaused,
			SendTypingIndicators:   row.SendTypingIndicators,
			ReadReceiptsVisibility: row.ReadReceiptsVisibility,
		}
	}

	fresh, ok := s.fetchPolicyFromIdentity(ctx, userID)
	if ok {
		if err := s.policyStore().UpsertUserPolicy(ctx, fresh); err != nil {
			s.log.Warn("policy projection upsert failed", "err", err, "user_id", userID)
		}
		return ChatPolicy{
			Known:                  true,
			ChatPaused:             fresh.ChatPaused,
			SendTypingIndicators:   fresh.SendTypingIndicators,
			ReadReceiptsVisibility: fresh.ReadReceiptsVisibility,
		}
	}

	// Fetch failed: a STALE projection beats no answer, but only within the
	// short grace window — an old row must not stay authoritative.
	if row != nil && time.Since(row.RefreshedAt) < policyStaleGrace {
		return ChatPolicy{
			Known:                  true,
			ChatPaused:             row.ChatPaused,
			SendTypingIndicators:   row.SendTypingIndicators,
			ReadReceiptsVisibility: row.ReadReceiptsVisibility,
		}
	}
	return ChatPolicy{
		Known:                  false,
		ChatPaused:             false,
		SendTypingIndicators:   false,    // disclosure fails closed
		ReadReceiptsVisibility: "no_one", // disclosure fails closed
	}
}

// InvalidateChatPolicy drops the projection — the identity-events consumer's
// entry point for user.settings_changed.
func (s *Service) InvalidateChatPolicy(ctx context.Context, userID uuid.UUID) error {
	return s.policyStore().InvalidateUserPolicy(ctx, userID)
}

func (s *Service) fetchPolicyFromIdentity(ctx context.Context, userID uuid.UUID) (postgres.UserPolicy, bool) {
	if s.identityUserURL == "" {
		return postgres.UserPolicy{}, false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/v1/users/%s/settings", s.identityUserURL, userID), nil)
	if err != nil {
		return postgres.UserPolicy{}, false
	}
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.log.Warn("policy fetch failed", "err", err, "user_id", userID)
		return postgres.UserPolicy{}, false
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		s.log.Warn("policy fetch non-200", "status", resp.StatusCode, "user_id", userID)
		return postgres.UserPolicy{}, false
	}
	var envelope struct {
		Data struct {
			ChatAvailability      string `json:"chat_availability"`
			SendTypingIndicators  *bool  `json:"send_typing_indicators"`
			WhoCanSeeReadReceipts string `json:"who_can_see_read_receipts"`
			PrivacyVersion        int    `json:"privacy_version"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return postgres.UserPolicy{}, false
	}
	// The authority always includes chat_availability. An empty value means
	// this is NOT the settings authority (e.g. a 200 from the legacy user
	// directory) — treating it as "enabled" would silently disable the pause.
	if envelope.Data.ChatAvailability == "" {
		s.log.Warn("policy fetch returned no chat_availability — wrong authority?", "user_id", userID)
		return postgres.UserPolicy{}, false
	}
	typing := true
	if envelope.Data.SendTypingIndicators != nil {
		typing = *envelope.Data.SendTypingIndicators
	}
	receipts := envelope.Data.WhoCanSeeReadReceipts
	if receipts == "" {
		receipts = "connections_only"
	}
	return postgres.UserPolicy{
		UserID:                 userID,
		ChatPaused:             envelope.Data.ChatAvailability == "paused",
		SendTypingIndicators:   typing,
		ReadReceiptsVisibility: receipts,
		PrivacyVersion:         envelope.Data.PrivacyVersion,
	}, true
}
