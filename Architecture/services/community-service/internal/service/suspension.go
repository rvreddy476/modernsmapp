package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/atpost/community-service/internal/store"
	"github.com/google/uuid"
)

// ─── Platform suspension (admin console, Chat) ──────────────────────────
//
// An admin suspends a community through admin-service's token family
// (http/admin_moderation.go, permission chat:communities.moderate). The row's
// status becomes 'suspended' (store/admin_suspension.go) and, while it is:
//
//   - Discover drops it (status = 'active' only) and search hides it:
//     community.updated carries status 'suspended', which search-service's
//     applyCommunitySnapshot tombstones like any non-active status;
//   - nobody joins it (JoinCommunity refuses a non-active community), so no
//     new join requests or invite-only requests either;
//   - its owner and members keep READ access and the card shows the
//     suspension; every write answers ErrCommunitySuspended: posts, replies,
//     sparks, stashes, edits, spaces, wiki, events, role changes, bans,
//     join-request approvals and deleting the community itself (an owner
//     cannot delete a suspended community out from under the moderator);
//   - everyone else gets the missing-community answer.
//
// Leaving stays open: it touches only the leaver's own row.
//
// Unsuspending sets it active again and announces it, so search re-lists a
// public community and every route works as before.

// ErrCommunitySuspended: the community is suspended by the platform; it is
// read-only for its members until an admin lifts it.
var ErrCommunitySuspended = &AuthError{Reason: "community_suspended"}

// refuseWhileSuspended is for writes the gate admits in read mode (a post's
// author deleting it, accepting an answer, spark, stash): the gate refuses
// every accessWrite on a suspended community itself.
func refuseWhileSuspended(c *store.Community) error {
	if c != nil && c.Status == store.CommunityStatusSuspended {
		return ErrCommunitySuspended
	}
	return nil
}

// SuspensionReasonCodes are the reason codes an admin may suspend with. The
// admin console shows them as a fixed list; the note carries the detail.
var SuspensionReasonCodes = []string{
	"spam",
	"scam_or_fraud",
	"harassment",
	"hate_speech",
	"violence",
	"sexual_content",
	"child_safety",
	"illegal_goods",
	"impersonation",
	"intellectual_property",
	"misinformation",
	"terms_violation",
	"legal_request",
	"other",
}

// suspensionNoteMaxRunes caps the admin's note (as the report decision).
const suspensionNoteMaxRunes = 1000

func isSuspensionReasonCode(code string) bool {
	for _, c := range SuspensionReasonCodes {
		if c == code {
			return true
		}
	}
	return false
}

// normaliseSuspension validates a suspend (suspend=true) or unsuspend body
// and returns it trimmed. Pure.
//
//	suspend:   reason_code required and from SuspensionReasonCodes; note optional
//	unsuspend: note required (why it is lifted); no reason_code
//	note:      at most 1000 characters
func normaliseSuspension(suspend bool, reasonCode, note string) (string, string, error) {
	reasonCode = strings.ToLower(strings.TrimSpace(reasonCode))
	note = strings.TrimSpace(note)
	if len([]rune(note)) > suspensionNoteMaxRunes {
		return "", "", fmt.Errorf("invalid: note must be at most %d characters", suspensionNoteMaxRunes)
	}
	if !suspend {
		if note == "" {
			return "", "", fmt.Errorf("invalid: note is required to unsuspend a community")
		}
		return "", note, nil
	}
	if reasonCode == "" {
		return "", "", fmt.Errorf("invalid: reason_code is required")
	}
	if !isSuspensionReasonCode(reasonCode) {
		return "", "", fmt.Errorf("invalid: reason_code must be one of %s", strings.Join(SuspensionReasonCodes, ", "))
	}
	return reasonCode, note, nil
}

// SuspendCommunity suspends a community on behalf of an admin. actorID is the
// signed act claim of the admin-service token, never a header. Idempotent: a
// community already suspended stays as it is (Changed=false). Every call is
// audited by the store, and the row is announced on community.updated.
func (s *Service) SuspendCommunity(ctx context.Context, actorID, communityID uuid.UUID, reasonCode, note string) (*store.CommunitySuspensionChange, error) {
	return s.setSuspended(ctx, actorID, communityID, true, reasonCode, note)
}

// UnsuspendCommunity lifts a suspension. Idempotent: an active community
// stays as it is (Changed=false). An archived or deleted one is a conflict.
func (s *Service) UnsuspendCommunity(ctx context.Context, actorID, communityID uuid.UUID, note string) (*store.CommunitySuspensionChange, error) {
	return s.setSuspended(ctx, actorID, communityID, false, "", note)
}

func (s *Service) setSuspended(ctx context.Context, actorID, communityID uuid.UUID, suspend bool, reasonCode, note string) (*store.CommunitySuspensionChange, error) {
	if actorID == uuid.Nil {
		return nil, store.ErrAdminActorRequired
	}
	reasonCode, note, err := normaliseSuspension(suspend, reasonCode, note)
	if err != nil {
		return nil, err
	}
	out, err := s.store.AdminSetCommunitySuspended(ctx, actorID, communityID, suspend, reasonCode, note)
	if err != nil {
		return nil, err
	}
	s.announceSuspension(ctx, out, actorID)
	return out, nil
}

// announceSuspension publishes community.updated with the committed row.
// A repeat (Changed=false) is announced as well: it carries the same
// version, which search drops as not newer, and it repairs a lost event.
func (s *Service) announceSuspension(ctx context.Context, out *store.CommunitySuspensionChange, actorID uuid.UUID) {
	if out == nil || out.Community == nil {
		return
	}
	if s.producer == nil {
		slog.Warn("community suspension changed without a community.updated event: no producer",
			"community_id", out.CommunityID, "status", out.Status)
		return
	}
	if err := s.producer.PublishCommunityUpdated(ctx, out.Community, actorID); err != nil {
		slog.Warn("failed to publish community.updated for a suspension change",
			"community_id", out.CommunityID, "status", out.Status, "error", err)
	}
}
