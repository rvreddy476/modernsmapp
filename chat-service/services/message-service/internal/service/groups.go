package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	sharedEvents "github.com/atpost/chat-shared/events"
	"github.com/atpost/chat-shared/roomauth"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Production chat pass — group governance (directive §3.4).
//
// Launch cap: 1,024 ACTIVE members. Roles: exactly one owner, any number of
// admins, members. Adds obey the target's who_can_add_to_groups via
// graph-service; consent-required targets get an INVITATION, never silent
// membership.

const (
	// GroupMemberCap is the launch cap on active members (directive §3.4).
	GroupMemberCap = 1024
	// groupTitleMaxRunes bounds the group title.
	groupTitleMaxRunes = 100

	// requestDeclineCooldown is how long a declined/blocked/reported request
	// bars the same sender from re-requesting the same receiver
	// (directive §3.3 — rotating idempotency keys must not bypass it).
	requestDeclineCooldown = 7 * 24 * time.Hour
)

var (
	// ErrNotPermittedRole is returned when the actor's role does not allow
	// the attempted group mutation.
	ErrNotPermittedRole = errors.New("your role does not permit this action")
	// ErrOwnerMustTransfer is returned when the owner tries to leave without
	// an explicit ownership transfer while other members remain.
	ErrOwnerMustTransfer = errors.New("transfer ownership before leaving the group")
	// ErrRequestCooldown is returned when a declined request is still inside
	// its re-request cooldown window.
	ErrRequestCooldown = errors.New("a previous request to this person was declined recently")
)

// groupStore is the governance surface of the postgres store.
type groupStore interface {
	CountActiveMembers(ctx context.Context, conversationID uuid.UUID) (int, error)
	AddMemberCapped(ctx context.Context, conversationID, userID uuid.UUID, role string, cap int) error
	RemoveMemberGoverned(ctx context.Context, conversationID, actorID, targetID uuid.UUID) (int64, error)
	LeaveGoverned(ctx context.Context, conversationID, userID uuid.UUID) (bool, int64, error)
	SeverMemberSystem(ctx context.Context, conversationID, userID uuid.UUID) (bool, int64, error)
	GetMemberGen(ctx context.Context, conversationID, userID uuid.UUID) (int64, error)
	NextMembershipGen(ctx context.Context) (int64, error)
	UpsertRevocationIntent(ctx context.Context, conversationID, userID uuid.UUID, severGen int64) error
	FetchPendingRevocationIntents(ctx context.Context, limit int) ([]postgres.RevocationIntent, error)
	DeleteRevocationIntent(ctx context.Context, conversationID, userID uuid.UUID, armedGen int64) error
	SetMemberRole(ctx context.Context, conversationID, userID uuid.UUID, role string) (bool, error)
	TransferOwnership(ctx context.Context, conversationID, fromUserID, toUserID uuid.UUID) error
	UpdateGroupInfo(ctx context.Context, conversationID uuid.UUID, title *string, avatarMediaID *uuid.UUID) error
	CreateGroupInvitation(ctx context.Context, conversationID, inviterID, inviteeID uuid.UUID) (*postgres.GroupInvitation, bool, error)
	GetGroupInvitation(ctx context.Context, invitationID uuid.UUID) (*postgres.GroupInvitation, error)
	ListPendingInvitationsForUser(ctx context.Context, userID uuid.UUID, limit int) ([]postgres.GroupInvitation, error)
	ResolveGroupInvitation(ctx context.Context, invitationID uuid.UUID, status string) (bool, error)
	GetLatestRequestBetween(ctx context.Context, senderID, receiverID uuid.UUID) (*postgres.MessageRequest, error)
	UpsertReadCursor(ctx context.Context, conversationID, userID, messageID uuid.UUID, readAt time.Time) error
	GetReadCursors(ctx context.Context, userID uuid.UUID, conversationIDs []uuid.UUID) (map[uuid.UUID]postgres.ReadCursor, error)
}

func (s *Service) groupStore() groupStore {
	return s.convStore.(groupStore)
}

// AddOutcome reports how one target was handled during a group create/add.
type AddOutcome struct {
	UserID string `json:"user_id"`
	// added | invited | skipped
	Outcome string `json:"outcome"`
	Reason  string `json:"reason,omitempty"`
}

// checkGroupAddPermission resolves the target's who_can_add_to_groups policy
// through graph-service. Returns (directAdd, invite): both false means the
// target cannot be brought in at all. Unknown state FAILS CLOSED (§5.1).
func (s *Service) checkGroupAddPermission(ctx context.Context, actorID, targetID uuid.UUID) (bool, bool, error) {
	if s.graphServiceURL == "" {
		s.log.Warn("GRAPH_SERVICE_URL not configured — group adds fail closed")
		return false, false, nil
	}
	url := fmt.Sprintf("%s/v1/permissions/check?target_user_id=%s&actions=add_to_group", s.graphServiceURL, targetID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, false, err
	}
	req.Header.Set("X-User-Id", actorID.String())
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		// Dependency failure on a privacy-sensitive add: fail closed.
		s.log.Warn("group-add permission check failed — failing closed", "err", err, "target", targetID)
		return false, false, nil
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		s.log.Warn("group-add permission check non-200 — failing closed",
			"status", resp.StatusCode, "target", targetID)
		return false, false, nil
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
		return false, false, nil
	}
	d := envelope.Data.Decisions.AddToGroup
	return d.Allowed, d.Fallback == "group_invitation", nil
}

// requireGroupRole loads the conversation, asserts it is a group and that the
// actor holds one of the given roles. Returns the conversation.
func (s *Service) requireGroupRole(ctx context.Context, conversationID, actorID uuid.UUID, roles ...string) (*postgres.Conversation, string, error) {
	conv, err := s.convStore.GetConversation(ctx, conversationID)
	if err != nil {
		return nil, "", err
	}
	if conv == nil {
		return nil, "", errors.New("conversation not found")
	}
	if conv.Type != "group" {
		return nil, "", errors.New("not a group conversation")
	}
	role, err := s.convStore.GetMemberRole(ctx, conversationID, actorID)
	if err != nil {
		return nil, "", err
	}
	if role == "" {
		return nil, "", errors.New("not a conversation member")
	}
	for _, allowed := range roles {
		if role == allowed {
			return conv, role, nil
		}
	}
	return nil, role, ErrNotPermittedRole
}

// blockedAgainst asks graph which of `others` hold a block with candidateID
// in either direction — one internal call, fail closed (P0-5: a third-party
// owner/admin must never co-locate two users who block each other, so the
// candidate is checked against the FULL roster, not just the actor).
func (s *Service) blockedAgainst(ctx context.Context, candidateID uuid.UUID, others []uuid.UUID) (bool, error) {
	filtered := make([]uuid.UUID, 0, len(others))
	for _, id := range others {
		if id != candidateID {
			filtered = append(filtered, id)
		}
	}
	if len(filtered) == 0 {
		return false, nil
	}
	if s.graphServiceURL == "" {
		s.log.Warn("GRAPH_SERVICE_URL not configured — roster block check fails closed")
		return true, nil
	}
	body, err := json.Marshal(map[string]any{
		"user_id": candidateID, "candidate_ids": filtered,
	})
	if err != nil {
		return true, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.graphServiceURL+"/v1/internal/graph/blocked-any", strings.NewReader(string(body)))
	if err != nil {
		return true, err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return true, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		return true, fmt.Errorf("blocked-any check returned %d", resp.StatusCode)
	}
	var decoded struct {
		BlockedUserIDs []uuid.UUID `json:"blocked_user_ids"`
	}
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return true, err
	}
	return len(decoded.BlockedUserIDs) > 0, nil
}

// blockedWithAnyMember runs blockedAgainst over the conversation's ACTIVE
// roster.
func (s *Service) blockedWithAnyMember(ctx context.Context, conversationID, candidateID uuid.UUID) (bool, error) {
	members, err := s.convStore.GetMembers(ctx, conversationID)
	if err != nil {
		return true, err
	}
	ids := make([]uuid.UUID, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.UserID)
	}
	return s.blockedAgainst(ctx, candidateID, ids)
}

// AddMemberWithPolicy is the production add path: role check, target policy,
// roster block check, cap, invitation fallback. Replaces the legacy AddMember
// consumer surface.
func (s *Service) AddMemberWithPolicy(ctx context.Context, actorID, conversationID, targetID uuid.UUID) (*AddOutcome, error) {
	if _, _, err := s.requireGroupRole(ctx, conversationID, actorID, "owner", "admin"); err != nil {
		return nil, err
	}
	targetIsMember, err := s.convStore.CheckMembership(ctx, conversationID, targetID)
	if err != nil {
		return nil, err
	}
	if targetIsMember {
		return &AddOutcome{UserID: targetID.String(), Outcome: "added", Reason: "already_member"}, nil
	}

	directAdd, invite, err := s.checkGroupAddPermission(ctx, actorID, targetID)
	if err != nil {
		return nil, err
	}
	if directAdd || invite {
		// Roster block check (P0-5). The reason stays "privacy" — reporting
		// "blocked" would disclose block state to the actor.
		blocked, err := s.blockedWithAnyMember(ctx, conversationID, targetID)
		if err != nil {
			return nil, err
		}
		if blocked {
			return &AddOutcome{UserID: targetID.String(), Outcome: "skipped", Reason: "privacy"}, nil
		}
	}
	switch {
	case directAdd:
		if err := s.groupStore().AddMemberCapped(ctx, conversationID, targetID, "member", GroupMemberCap); err != nil {
			return nil, err
		}
		_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.MemberAdded, sharedEvents.MemberAddedPayload{
			ConversationID: conversationID.String(),
			UserID:         targetID.String(),
			AddedBy:        actorID.String(),
			Role:           "member",
			AddedAt:        time.Now(),
		})
		s.publishControlFrame(ctx, targetID, map[string]any{
			"type": "conversation_joined", "conversation_id": conversationID.String(),
		})
		return &AddOutcome{UserID: targetID.String(), Outcome: "added"}, nil
	case invite:
		inv, created, err := s.groupStore().CreateGroupInvitation(ctx, conversationID, actorID, targetID)
		if err != nil {
			return nil, err
		}
		if created {
			_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.GroupInviteCreated, sharedEvents.GroupInvitePayload{
				InvitationID:   inv.ID.String(),
				ConversationID: conversationID.String(),
				InviterID:      actorID.String(),
				InviteeID:      targetID.String(),
				OccurredAt:     time.Now(),
			})
			s.publishControlFrame(ctx, targetID, map[string]any{
				"type": "group_invitation", "invitation_id": inv.ID.String(),
				"conversation_id": conversationID.String(),
			})
		}
		return &AddOutcome{UserID: targetID.String(), Outcome: "invited"}, nil
	default:
		return &AddOutcome{UserID: targetID.String(), Outcome: "skipped", Reason: "privacy"}, nil
	}
}

// CreateGroupWithPolicy creates the group and resolves every candidate
// through the target's policy: direct adds join immediately, consent targets
// get invitations, denied targets are reported as skipped (never silently
// dropped). Idempotent via the caller-supplied key.
type CreateGroupResult struct {
	Conversation *ConversationResponse `json:"conversation"`
	Outcomes     []AddOutcome          `json:"outcomes"`
}

func (s *Service) CreateGroupWithPolicy(ctx context.Context, creatorID uuid.UUID, title string, memberIDs []uuid.UUID, idempotencyKey string) (*CreateGroupResult, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, errors.New("group title is required")
	}
	if utf8.RuneCountInString(title) > groupTitleMaxRunes {
		return nil, fmt.Errorf("group title exceeds %d characters", groupTitleMaxRunes)
	}
	if len(memberIDs) < 1 {
		return nil, errors.New("at least one other member is required")
	}
	if len(memberIDs) >= GroupMemberCap {
		return nil, postgres.ErrGroupFull
	}

	req := struct {
		UserID    uuid.UUID   `json:"user_id"`
		Title     string      `json:"title"`
		MemberIDs []uuid.UUID `json:"member_ids"`
	}{UserID: creatorID, Title: title, MemberIDs: memberIDs}
	return withIdempotency(ctx, s, idempotencyKey, req, func() (*CreateGroupResult, error) {
		// Resolve policies BEFORE creating, so the group is born with its
		// honest roster instead of being patched after.
		type resolved struct {
			id     uuid.UUID
			direct bool
			invite bool
		}
		seen := map[uuid.UUID]bool{creatorID: true}
		resolutions := make([]resolved, 0, len(memberIDs))
		// Roster block check (P0-5): each candidate is checked against the
		// creator plus every earlier direct-add, so two candidates who block
		// each other are never born into the same roster. Invitees are
		// re-checked at accept time against the roster that exists then.
		activeSoFar := []uuid.UUID{creatorID}
		for _, target := range memberIDs {
			if seen[target] {
				continue
			}
			seen[target] = true
			direct, invite, err := s.checkGroupAddPermission(ctx, creatorID, target)
			if err != nil {
				return nil, err
			}
			if direct || invite {
				blocked, err := s.blockedAgainst(ctx, target, activeSoFar)
				if err != nil {
					return nil, err
				}
				if blocked {
					direct, invite = false, false
				}
			}
			if direct {
				activeSoFar = append(activeSoFar, target)
			}
			resolutions = append(resolutions, resolved{id: target, direct: direct, invite: invite})
		}

		directIDs := make([]uuid.UUID, 0, len(resolutions))
		for _, r := range resolutions {
			if r.direct {
				directIDs = append(directIDs, r.id)
			}
		}

		convID, err := s.convStore.CreateGroupConversation(ctx, creatorID, title, directIDs)
		if err != nil {
			return nil, err
		}

		outcomes := make([]AddOutcome, 0, len(resolutions))
		allMembers := []string{creatorID.String()}
		for _, r := range resolutions {
			switch {
			case r.direct:
				outcomes = append(outcomes, AddOutcome{UserID: r.id.String(), Outcome: "added"})
				allMembers = append(allMembers, r.id.String())
			case r.invite:
				inv, created, err := s.groupStore().CreateGroupInvitation(ctx, convID, creatorID, r.id)
				if err != nil {
					return nil, err
				}
				if created {
					_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.GroupInviteCreated, sharedEvents.GroupInvitePayload{
						InvitationID:   inv.ID.String(),
						ConversationID: convID.String(),
						InviterID:      creatorID.String(),
						InviteeID:      r.id.String(),
						OccurredAt:     time.Now(),
					})
					s.publishControlFrame(ctx, r.id, map[string]any{
						"type": "group_invitation", "invitation_id": inv.ID.String(),
						"conversation_id": convID.String(),
					})
				}
				outcomes = append(outcomes, AddOutcome{UserID: r.id.String(), Outcome: "invited"})
			default:
				outcomes = append(outcomes, AddOutcome{UserID: r.id.String(), Outcome: "skipped", Reason: "privacy"})
			}
		}

		_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.ConversationCreated, sharedEvents.ConversationCreatedPayload{
			ConversationID: convID.String(),
			Type:           "group",
			Title:          title,
			CreatedBy:      creatorID.String(),
			MemberIDs:      allMembers,
			CreatedAt:      time.Now(),
		})

		conv, err := s.getConversationResponse(ctx, convID)
		if err != nil {
			return nil, err
		}
		return &CreateGroupResult{Conversation: conv, Outcomes: outcomes}, nil
	})
}

// RemoveMemberGoverned enforces the role ladder: owner removes anyone but
// themselves; admins remove ordinary members only; nobody removes the owner.
// The ladder is decided INSIDE the store transaction (P0-5) — a concurrent
// ownership transfer between an advisory read here and the sever would
// otherwise let a stale authorization sever the new owner.
func (s *Service) RemoveMemberGoverned(ctx context.Context, actorID, conversationID, targetID uuid.UUID) error {
	if actorID == targetID {
		return s.LeaveConversation(ctx, actorID, conversationID)
	}
	severGen, err := s.groupStore().RemoveMemberGoverned(ctx, conversationID, actorID, targetID)
	if err != nil {
		switch {
		case errors.Is(err, postgres.ErrRoleNotPermitted):
			return ErrNotPermittedRole
		case errors.Is(err, postgres.ErrNotAMember):
			// The retry lane for "sever committed, marker write failed":
			// the retried request finds the target already severed, so arm
			// the marker with a FRESH sequence generation before surfacing
			// the terminal answer. Any failure here propagates — the
			// final verification showed a swallowed failure let this lane
			// acknowledge without a marker. Harmless for a user who never
			// was a member (they hold no valid tokens, and any later rejoin
			// mints a newer generation than this marker).
			gen, genErr := s.groupStore().NextMembershipGen(ctx)
			if genErr != nil {
				return fmt.Errorf("revocation generation unavailable: %w", genErr)
			}
			if armErr := s.settleRevocation(ctx, conversationID, targetID, gen, false); armErr != nil {
				return armErr
			}
			return errors.New("target user is not a conversation member")
		}
		return err
	}
	// Marker BEFORE success (re-verification P0-4): the removal is not
	// reported done until the revocation fact is durably in Redis. The
	// intent rode the sever transaction, so even an arm failure here leaves
	// the marker debt with the repair worker — the response stays
	// fail-closed, but persistence no longer depends on a retry.
	if err := s.settleRevocation(ctx, conversationID, targetID, severGen, true); err != nil {
		return err
	}
	_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.MemberRemoved, sharedEvents.MemberRemovedPayload{
		ConversationID: conversationID.String(),
		UserID:         targetID.String(),
		RemovedBy:      actorID.String(),
		RemovedAt:      time.Now(),
	})
	s.publishRevocationFrame(ctx, conversationID, targetID)
	return nil
}

// LeaveConversation is self-removal. The owner must transfer first unless
// they are the last active member (then the group closes with them).
func (s *Service) LeaveConversation(ctx context.Context, userID, conversationID uuid.UUID) error {
	conv, err := s.convStore.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	if conv == nil {
		return errors.New("conversation not found")
	}
	if conv.Type != "group" {
		return errors.New("leave applies to group conversations")
	}
	// Role, member count and the sever are one store transaction (P0-5): a
	// join that lands between an advisory count and the sever would let the
	// owner exit a group that is no longer sole-membered.
	removed, severGen, err := s.groupStore().LeaveGoverned(ctx, conversationID, userID)
	if err != nil {
		if errors.Is(err, postgres.ErrOwnerMustTransferStore) {
			return ErrOwnerMustTransfer
		}
		return err
	}
	if removed {
		// Marker BEFORE success (re-verification P0-4); the intent rode the
		// sever transaction, so the repair worker owns persistence even if
		// this arm fails and nobody retries.
		if err := s.settleRevocation(ctx, conversationID, userID, severGen, true); err != nil {
			return err
		}
		_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.MemberLeft, sharedEvents.MemberLeftPayload{
			ConversationID: conversationID.String(),
			UserID:         userID.String(),
			OccurredAt:     time.Now(),
		})
		s.publishRevocationFrame(ctx, conversationID, userID)
		return nil
	}
	// Already gone: the retry lane for "sever committed, marker write
	// failed" — re-arm with a fresh generation before confirming the no-op.
	// EVERY failure propagates (final-verification hole 3: a swallowed
	// GetDBNow failure let this lane return success without a marker).
	gen, genErr := s.groupStore().NextMembershipGen(ctx)
	if genErr != nil {
		return fmt.Errorf("revocation generation unavailable: %w", genErr)
	}
	return s.settleRevocation(ctx, conversationID, userID, gen, false)
}

// TransferOwnership is the explicit owner hand-off (directive §3.4).
func (s *Service) TransferOwnershipGoverned(ctx context.Context, actorID, conversationID, newOwnerID uuid.UUID) error {
	// Role first: a non-owner asking to "transfer to self" must hear
	// "not permitted", not a message implying they own the group.
	if _, _, err := s.requireGroupRole(ctx, conversationID, actorID, "owner"); err != nil {
		return err
	}
	if actorID == newOwnerID {
		return errors.New("you already own this group")
	}
	if err := s.groupStore().TransferOwnership(ctx, conversationID, actorID, newOwnerID); err != nil {
		return err
	}
	_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.OwnershipTransferred, sharedEvents.OwnershipTransferredPayload{
		ConversationID: conversationID.String(),
		FromUserID:     actorID.String(),
		ToUserID:       newOwnerID.String(),
		OccurredAt:     time.Now(),
	})
	return nil
}

// SetMemberRoleGoverned promotes/demotes between admin and member. Owner only.
func (s *Service) SetMemberRoleGoverned(ctx context.Context, actorID, conversationID, targetID uuid.UUID, role string) error {
	if _, _, err := s.requireGroupRole(ctx, conversationID, actorID, "owner"); err != nil {
		return err
	}
	if targetID == actorID {
		return errors.New("use ownership transfer to change the owner")
	}
	oldRole, err := s.convStore.GetMemberRole(ctx, conversationID, targetID)
	if err != nil {
		return err
	}
	if oldRole == "" {
		return errors.New("target user is not a conversation member")
	}
	if oldRole == role {
		return nil // idempotent
	}
	changed, err := s.groupStore().SetMemberRole(ctx, conversationID, targetID, role)
	if err != nil {
		return err
	}
	if changed {
		_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.MemberRoleChanged, sharedEvents.MemberRoleChangedPayload{
			ConversationID: conversationID.String(),
			UserID:         targetID.String(),
			OldRole:        oldRole,
			NewRole:        role,
			ChangedBy:      actorID.String(),
			OccurredAt:     time.Now(),
		})
	}
	return nil
}

// UpdateGroupInfoGoverned edits title/avatar. Owner or admin.
func (s *Service) UpdateGroupInfoGoverned(ctx context.Context, actorID, conversationID uuid.UUID, title *string, avatarMediaID *uuid.UUID) error {
	if _, _, err := s.requireGroupRole(ctx, conversationID, actorID, "owner", "admin"); err != nil {
		return err
	}
	if title != nil {
		trimmed := strings.TrimSpace(*title)
		if trimmed == "" {
			return errors.New("group title cannot be empty")
		}
		if utf8.RuneCountInString(trimmed) > groupTitleMaxRunes {
			return fmt.Errorf("group title exceeds %d characters", groupTitleMaxRunes)
		}
		title = &trimmed
	}
	if err := s.groupStore().UpdateGroupInfo(ctx, conversationID, title, avatarMediaID); err != nil {
		return err
	}
	payload := sharedEvents.GroupInfoUpdatedPayload{
		ConversationID: conversationID.String(),
		UpdatedBy:      actorID.String(),
		OccurredAt:     time.Now(),
	}
	if title != nil {
		payload.Title = *title
	}
	if avatarMediaID != nil {
		payload.AvatarMediaID = avatarMediaID.String()
	}
	_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.GroupInfoUpdated, payload)
	return nil
}

// --- Invitations (invitee side) ---

// ListMyInvitations returns the caller's pending group invitations.
func (s *Service) ListMyInvitations(ctx context.Context, userID uuid.UUID) ([]postgres.GroupInvitation, error) {
	return s.groupStore().ListPendingInvitationsForUser(ctx, userID, 50)
}

// AcceptGroupInvitation joins the invitee under the cap. Idempotent: a
// re-accept of an already-accepted invitation succeeds without a second add.
func (s *Service) AcceptGroupInvitation(ctx context.Context, userID, invitationID uuid.UUID) error {
	inv, err := s.groupStore().GetGroupInvitation(ctx, invitationID)
	if err != nil {
		return err
	}
	if inv == nil {
		return errors.New("invitation not found")
	}
	if inv.InviteeID != userID {
		return errors.New("only the invitee can accept this invitation")
	}
	switch inv.Status {
	case "accepted":
		return nil // idempotent
	case "pending":
	default:
		return fmt.Errorf("invitation is %s", inv.Status)
	}

	// Roster block check at ACCEPT time (P0-5): the roster may have gained a
	// blocked counterpart since the invitation was issued.
	blocked, err := s.blockedWithAnyMember(ctx, inv.ConversationID, userID)
	if err != nil {
		return err
	}
	if blocked {
		return errors.New("this group cannot be joined")
	}

	// Membership FIRST, then the status flip: a crash between the two leaves
	// a pending invitation whose accept retries into the idempotent add.
	if err := s.groupStore().AddMemberCapped(ctx, inv.ConversationID, userID, "member", GroupMemberCap); err != nil {
		return err
	}
	if _, err := s.groupStore().ResolveGroupInvitation(ctx, invitationID, "accepted"); err != nil {
		return err
	}
	_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.GroupInviteAccepted, sharedEvents.GroupInvitePayload{
		InvitationID:   inv.ID.String(),
		ConversationID: inv.ConversationID.String(),
		InviterID:      inv.InviterID.String(),
		InviteeID:      inv.InviteeID.String(),
		OccurredAt:     time.Now(),
	})
	_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.MemberAdded, sharedEvents.MemberAddedPayload{
		ConversationID: inv.ConversationID.String(),
		UserID:         inv.InviteeID.String(),
		AddedBy:        inv.InviterID.String(),
		Role:           "member",
		AddedAt:        time.Now(),
	})
	return nil
}

// DeclineGroupInvitation refuses the offer. Idempotent on re-decline.
func (s *Service) DeclineGroupInvitation(ctx context.Context, userID, invitationID uuid.UUID) error {
	inv, err := s.groupStore().GetGroupInvitation(ctx, invitationID)
	if err != nil {
		return err
	}
	if inv == nil {
		return errors.New("invitation not found")
	}
	if inv.InviteeID != userID {
		return errors.New("only the invitee can decline this invitation")
	}
	if inv.Status == "declined" {
		return nil
	}
	if inv.Status != "pending" {
		return fmt.Errorf("invitation is %s", inv.Status)
	}
	if _, err := s.groupStore().ResolveGroupInvitation(ctx, invitationID, "declined"); err != nil {
		return err
	}
	_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.GroupInviteDeclined, sharedEvents.GroupInvitePayload{
		InvitationID:   inv.ID.String(),
		ConversationID: inv.ConversationID.String(),
		InviterID:      inv.InviterID.String(),
		InviteeID:      inv.InviteeID.String(),
		OccurredAt:     time.Now(),
	})
	return nil
}

// --- realtime control helpers ---

// publishControlFrame pushes a small typed frame to a user's personal
// channel. Best-effort: control frames are an acceleration, never the truth.
func (s *Service) publishControlFrame(ctx context.Context, userID uuid.UUID, frame map[string]any) {
	if s.rdb == nil {
		return
	}
	payload, err := json.Marshal(frame)
	if err != nil {
		return
	}
	if err := s.rdb.Publish(ctx, "chat:"+userID.String(), payload).Err(); err != nil {
		s.log.Warn("control frame publish failed", "err", err, "user_id", userID, "type", frame["type"])
	}
}

// denyRatchetScript writes the sever-generation marker but only ever RAISES
// it: a delayed arm from an older removal can never lower (and thereby
// re-open) a newer removal's revocation. The marker is never deleted — a
// legitimately rejoined member is admitted because their fresh token carries
// a joined_at generation NEWER than any prior sever (re-verification P0-4:
// the previous clear-on-rejoin DEL raced a concurrent second removal).
var denyRatchetScript = redis.NewScript(`
local cur = redis.call('GET', KEYS[1])
if (not cur) or (tonumber(ARGV[1]) >= tonumber(cur)) then
  redis.call('SET', KEYS[1], ARGV[1], 'EX', ARGV[2])
end
return 1
`)

// armRevocation persists the revocation fact (re-verification P0-4): the
// deny marker, valued with the sever GENERATION (membership sequence, drawn
// under the conversation lock), MUST be in Redis before any removal is
// reported as a success. A write failure propagates — the caller returns an
// error and the client/consumer retries, so "removed" is never acknowledged
// while a still-live token could replay. Skipping is legal ONLY while the
// entitlement feature is off entirely (no secret ⇒ the gateway subscribes
// nobody); a nil Redis WITH the secret configured is a deploy fault and
// fails loudly (final-verification hole 3).
func (s *Service) armRevocation(ctx context.Context, conversationID, userID uuid.UUID, severGen int64) error {
	if s.rdb == nil {
		if s.entitlementSecret == "" {
			return nil // room entitlements do not exist in this deployment
		}
		return fmt.Errorf("revocation marker not persisted: entitlement secret configured but no redis client")
	}
	err := denyRatchetScript.Run(ctx, s.rdb,
		[]string{roomauth.DenyKey(conversationID.String(), userID.String())},
		severGen, int(roomauth.DenyTTL.Seconds()),
	).Err()
	if err != nil {
		s.log.Error("entitlement deny marker write failed — refusing to acknowledge the sever",
			"err", err, "conversation_id", conversationID, "user_id", userID)
		return fmt.Errorf("revocation marker not persisted: %w", err)
	}
	return nil
}

// SeverDirectConversationOnBlock severs BOTH sides of the pair's direct
// conversation and arms revocation for every severed membership before
// acknowledging (final-verification P0-4: the graph-block consumer severed
// through a legacy statement with no marker and no frame; a blocked pair's
// live room subscriptions and tokens outlived the sever). The consumer must
// treat any error as a non-ack so the at-least-once redelivery retries; the
// already-severed retry lane re-arms with a fresh generation.
func (s *Service) SeverDirectConversationOnBlock(ctx context.Context, blockerID, blockedID uuid.UUID) (bool, error) {
	convID, severed, err := s.convStore.SeverDirectConversation(ctx, blockerID, blockedID)
	if err != nil {
		return false, err
	}
	if convID == uuid.Nil {
		return false, nil // the pair shares no direct conversation
	}
	// EVERY resolved block settles revocation for BOTH pair members,
	// regardless of how many rows this call severed (Blocker-2 final
	// correction): a mixed roster — one active, one severed earlier with a
	// failed marker — previously armed only the freshly severed row and
	// acknowledged, leaving the other member's stale token live.
	//
	// Two phases, deliberately: DURABILITY for both users first (members
	// severed in THIS call already carry an in-transaction intent; the rest
	// get a fresh generation and intent here), THEN arming — so an arm
	// failure on one user can never prevent the other user's marker debt
	// from reaching the repair worker.
	severedGen := make(map[uuid.UUID]int64, len(severed))
	for _, sm := range severed {
		severedGen[sm.UserID] = sm.Gen
	}
	gens := make(map[uuid.UUID]int64, 2)
	for _, userID := range []uuid.UUID{blockerID, blockedID} {
		if gen, ok := severedGen[userID]; ok {
			gens[userID] = gen
			continue
		}
		gen, genErr := s.groupStore().NextMembershipGen(ctx)
		if genErr != nil {
			return false, fmt.Errorf("revocation generation unavailable: %w", genErr)
		}
		if err := s.groupStore().UpsertRevocationIntent(ctx, convID, userID, gen); err != nil {
			return false, fmt.Errorf("revocation intent not persisted: %w", err)
		}
		gens[userID] = gen
	}
	var firstArmErr error
	for _, userID := range []uuid.UUID{blockerID, blockedID} {
		if err := s.settleRevocation(ctx, convID, userID, gens[userID], true); err != nil {
			if firstArmErr == nil {
				firstArmErr = err
			}
		}
	}
	if firstArmErr != nil {
		return false, firstArmErr
	}
	for _, sm := range severed {
		s.publishRevocationFrame(ctx, sm.ConversationID, sm.UserID)
	}
	return len(severed) > 0, nil
}

// settleRevocation drives one (conversation, user, generation) revocation to
// its durable state (Blocker-2 final correction). The INTENT — the durable
// PostgreSQL record that a marker is owed — either rode the sever
// transaction (intentDurable) or is upserted here first, so from this point
// the repair worker guarantees eventual marker persistence even if this
// process dies. Then the marker is armed; on success the intent is cleared
// (best effort — a leftover intent is re-armed harmlessly by the ratchet).
// An arm failure still propagates so the caller's response stays
// fail-closed, but durability no longer depends on anyone retrying.
func (s *Service) settleRevocation(ctx context.Context, conversationID, userID uuid.UUID, severGen int64, intentDurable bool) error {
	if !intentDurable {
		if err := s.groupStore().UpsertRevocationIntent(ctx, conversationID, userID, severGen); err != nil {
			return fmt.Errorf("revocation intent not persisted: %w", err)
		}
	}
	if err := s.armRevocation(ctx, conversationID, userID, severGen); err != nil {
		return err // the durable intent remains; the worker retries the marker
	}
	if err := s.groupStore().DeleteRevocationIntent(ctx, conversationID, userID, severGen); err != nil {
		s.log.Warn("revocation intent cleanup failed — worker will re-arm harmlessly",
			"err", err, "conversation_id", conversationID, "user_id", userID)
	}
	return nil
}

// StartRevocationRepairWorker drains pending revocation intents: every
// committed sever leaves one until its deny marker is provably in Redis, so
// a crash or Redis outage between commit and arm is repaired here — never by
// a client choosing to retry (Blocker-2 final correction).
func (s *Service) StartRevocationRepairWorker(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.repairPendingRevocations(ctx)
		}
	}
}

// repairPendingRevocations is one worker sweep, separated so the drain logic
// is provable without the ticker loop.
func (s *Service) repairPendingRevocations(ctx context.Context) {
	intents, err := s.groupStore().FetchPendingRevocationIntents(ctx, 100)
	if err != nil {
		s.log.Error("failed to fetch pending revocation intents", "err", err)
		return
	}
	if len(intents) > 0 {
		// Operational signal (P0-4 closure P1 note): backlog size and oldest
		// age per sweep, so repair capacity is observable before it becomes
		// a latency incident. Ids only — never message content.
		s.log.Info("revocation repair backlog",
			"pending", len(intents),
			"oldest_age_seconds", int(time.Since(intents[0].CreatedAt).Seconds()))
	}
	for _, intent := range intents {
		if err := s.armRevocation(ctx, intent.ConversationID, intent.UserID, intent.SeverGen); err != nil {
			s.log.Error("revocation repair arm failed", "err", err,
				"conversation_id", intent.ConversationID, "user_id", intent.UserID)
			continue
		}
		if err := s.groupStore().DeleteRevocationIntent(ctx, intent.ConversationID, intent.UserID, intent.SeverGen); err != nil {
			s.log.Warn("revocation repair cleanup failed", "err", err,
				"conversation_id", intent.ConversationID, "user_id", intent.UserID)
		}
	}
}

// severMemberWithRevocation is the ONE protocol every system-authority sever
// goes through (final-verification P0-4: managed removal, request
// block/report and graph-block reconciliation previously severed without
// revocation). It severs, settles revocation with the serialization-ordered
// generation, and publishes the eager frame; when the row was ALREADY gone
// it still re-arms with a fresh generation — that is the retry lane for a
// prior attempt that severed but failed to arm. No caller may acknowledge
// success unless this returns nil.
func (s *Service) severMemberWithRevocation(ctx context.Context, conversationID, userID uuid.UUID) error {
	severed, severGen, err := s.groupStore().SeverMemberSystem(ctx, conversationID, userID)
	if err != nil {
		return err
	}
	if !severed {
		gen, genErr := s.groupStore().NextMembershipGen(ctx)
		if genErr != nil {
			return fmt.Errorf("revocation generation unavailable: %w", genErr)
		}
		return s.settleRevocation(ctx, conversationID, userID, gen, false)
	}
	if err := s.settleRevocation(ctx, conversationID, userID, severGen, true); err != nil {
		return err
	}
	s.publishRevocationFrame(ctx, conversationID, userID)
	return nil
}

// publishRevocationFrame is the EAGER half of revocation: connected gateways
// drop the room now instead of at the next reconciliation sweep. Best-effort
// by design — the durable marker (armRevocation) plus the gateway's periodic
// subscription reconciliation are what guarantee the revocation.
func (s *Service) publishRevocationFrame(ctx context.Context, conversationID, userID uuid.UUID) {
	s.publishControlFrame(ctx, userID, map[string]any{
		"type":            "subscription_revoked",
		"conversation_id": conversationID.String(),
	})
}
