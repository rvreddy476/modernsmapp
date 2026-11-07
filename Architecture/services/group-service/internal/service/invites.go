package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/group-service/internal/store"
	"github.com/google/uuid"
)

// --- Invites ---

// InviteUser adds or invites one person, returning "added" or "invited" so the
// caller can say which actually happened rather than always claiming an invite.
func (s *Service) InviteUser(ctx context.Context, actorID, groupID, inviteeID uuid.UUID) (string, error) {
	acc, err := s.authorize(ctx, groupID, actorID, accessWrite)
	if err != nil {
		return "", err
	}
	g, actor := acc.g, acc.member
	if !isActive(actor) {
		return "", fmt.Errorf("forbidden: only members can invite users")
	}

	// Enforce who_can_invite
	if err := s.checkPermission(g.WhoCanInvite, actor.Role); err != nil {
		return "", fmt.Errorf("forbidden: %w", err)
	}

	alreadyMember, err := s.store.CheckMembership(ctx, groupID, inviteeID)
	if err != nil {
		return "", err
	}
	if alreadyMember {
		return "", fmt.Errorf("user is already a member of this group")
	}

	/*
		The invitee's own say, not just the group's — this is where a block is
		honoured, and it fails closed.

		Unlike the batch, one person named by one request may be told the
		request was refused: there is nobody else it could have been, so
		ErrInviteNotPermitted leaks nothing the caller did not already choose.
	*/
	outcome, err := s.addOrInvite(ctx, actorID, g, inviteeID)
	if err != nil {
		return "", err
	}
	switch outcome {
	case outcomeAdded:
		return "added", nil
	case outcomeInvited:
		return "invited", nil
	default:
		return "", ErrInviteNotPermitted
	}
}

/*
	AddPeopleResult is what actually happened, in COUNTS rather than names.

	Names are withheld on purpose, and it is the same reason the batch skips a
	refused target instead of failing: telling the inviter which of the people
	they picked was refused tells them who blocked them. Returning added and
	invited as id lists would leak that by subtraction — pick three, get two
	back, and you know exactly who the third is — so the wire carries totals
	and the client re-reads the member list, where an invited person and a
	refused one look the same until one accepts.
*/
type AddPeopleResult struct {
	// Added straight in, because their own privacy allows it.
	Added int `json:"added"`
	// Sent an invitation to accept.
	Invited int `json:"invited"`
	// Already a member, banned, blocked, or refused by their own settings.
	Skipped int `json:"skipped"`
}

type addOutcome int

const (
	outcomeSkipped addOutcome = iota
	outcomeAdded
	outcomeInvited
)

/*
	addOrInvite applies ONE person's own answer to being put in a group.

	graph-service answers with two different permissions and this used to throw
	one of them away: directAdd was computed and then everybody got an
	invitation regardless. So someone whose privacy says "people I am connected
	to can add me to groups" still had to accept an invite — the setting did
	nothing, and a creator who picked three people watched their new group sit
	at one member.

	Both paths go through here so the single invite and the batch cannot give
	the same pair different answers, which is the drift canAddToGroup's comment
	warns about.
*/
func (s *Service) addOrInvite(ctx context.Context, actorID uuid.UUID, g *store.Group, targetID uuid.UUID) (addOutcome, error) {
	if targetID == actorID {
		return outcomeSkipped, nil
	}
	isMember, err := s.store.CheckMembership(ctx, g.ID, targetID)
	if err != nil {
		return outcomeSkipped, err
	}
	if isMember {
		return outcomeSkipped, nil
	}
	isBanned, err := s.store.CheckBanned(ctx, g.ID, targetID)
	if err != nil {
		return outcomeSkipped, err
	}
	if isBanned {
		return outcomeSkipped, nil
	}

	directAdd, invite := s.canAddToGroup(ctx, actorID, targetID)
	switch {
	case directAdd:
		if err := s.store.AddMemberWithInviter(ctx, g.ID, targetID, "member", actorID); err != nil {
			return outcomeSkipped, err
		}
		return outcomeAdded, nil
	case invite:
		inv := &store.GroupInvite{GroupID: g.ID, InviterID: actorID, InviteeID: targetID}
		if err := s.store.CreateInvite(ctx, inv); err != nil {
			return outcomeSkipped, err
		}
		s.publishEvent(func() error {
			return s.producer.PublishGroupInviteSent(ctx, g.ID, actorID, targetID, inv.ID)
		})
		return outcomeInvited, nil
	default:
		// Blocked, or their settings refuse. Skipped without a reason, so the
		// inviter cannot read a block off the response.
		return outcomeSkipped, nil
	}
}

// InviteUsersBatch adds or invites multiple users to a group (max 50).
func (s *Service) InviteUsersBatch(ctx context.Context, actorID, groupID uuid.UUID, inviteeIDs []uuid.UUID) (*AddPeopleResult, error) {
	if len(inviteeIDs) > 50 {
		return nil, fmt.Errorf("maximum 50 invites per batch")
	}
	if len(inviteeIDs) == 0 {
		return nil, fmt.Errorf("no users to invite")
	}

	// Rate limit: 100 invites/day
	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:invite:%s", actorID), 100, 24*time.Hour) {
		return nil, fmt.Errorf("rate_limited: too many invites")
	}

	acc, err := s.authorize(ctx, groupID, actorID, accessWrite)
	if err != nil {
		return nil, err
	}
	g, actor := acc.g, acc.member
	if !isActive(actor) {
		return nil, fmt.Errorf("forbidden: only members can invite users")
	}

	if err := s.checkPermission(g.WhoCanInvite, actor.Role); err != nil {
		return nil, fmt.Errorf("forbidden: %w", err)
	}

	/*
		Each person gets their own answer, and one refusal does not fail the
		batch — that is deliberate, because a failure naming the refused target
		would tell the inviter which of fifty people blocked them.

		A real error (the database, not a policy) still fails the whole call:
		reporting "3 invited" when the write failed is worse than an error.
	*/
	result := &AddPeopleResult{}
	seen := make(map[uuid.UUID]bool, len(inviteeIDs))
	for _, id := range inviteeIDs {
		// The same id twice is one person, not two.
		if seen[id] {
			continue
		}
		seen[id] = true

		outcome, err := s.addOrInvite(ctx, actorID, g, id)
		if err != nil {
			return nil, err
		}
		switch outcome {
		case outcomeAdded:
			result.Added++
		case outcomeInvited:
			result.Invited++
		default:
			result.Skipped++
		}
	}
	return result, nil
}

// ErrInviteNoLongerAcceptable is returned at accept time when the group is
// gone or the invitee has since been banned from it. Generic by design — see
// AcceptInvite.
var ErrInviteNoLongerAcceptable = fmt.Errorf("forbidden: this invite can no longer be accepted")

// AcceptInvite accepts a pending invite and adds the user to the group.
func (s *Service) AcceptInvite(ctx context.Context, actorID uuid.UUID, inviteID uuid.UUID) error {
	inv, err := s.store.GetInviteByID(ctx, inviteID)
	if err != nil {
		return err
	}
	if inv == nil {
		return fmt.Errorf("invite not found")
	}
	if inv.InviteeID != actorID {
		return fmt.Errorf("forbidden: this invite is not for you")
	}
	if inv.Status != "pending" {
		return fmt.Errorf("invite is no longer pending")
	}
	// Audit CG3: invites carry an ExpiresAt set at creation (~7 days)
	// but the previous code never checked it — a leaked or shared
	// invite link replayed years later still worked. Reject expired
	// invites and bump the status so the row is closed out.
	if inv.ExpiresAt != nil && time.Now().After(*inv.ExpiresAt) {
		// 'rejected', not 'expired': group_invites.status is CHECK-constrained
		// to pending | accepted | rejected, so the previous "expired" write
		// failed every time and its error was discarded — the row stayed
		// pending for ever and the CG3 close-out never actually happened.
		_ = s.store.UpdateInviteStatus(ctx, inviteID, "rejected")
		return fmt.Errorf("invite expired")
	}

	/*
		Re-check at admission what was true at invitation.

		An invite is a promise made earlier, and two things can change
		between then and the accept: the group can be deleted or archived,
		and the invitee can be banned from it. Before these checks an invite
		issued before a ban admitted the banned user the moment they
		accepted, and an invite to a since-deleted group still wrote a
		membership row. Both are checked here, before the invite is marked
		accepted and before any membership is written.

		The refusal is one generic error for both cases, on purpose: which
		of the two applied is not the invitee's to learn from this endpoint.
	*/
	g, err := s.store.GetGroupByID(ctx, inv.GroupID)
	if err != nil {
		return err
	}
	if g == nil || g.Status == "deleted" || g.Status == "archived" {
		_ = s.store.UpdateInviteStatus(ctx, inviteID, "rejected")
		return ErrInviteNoLongerAcceptable
	}
	banned, err := s.store.CheckBanned(ctx, inv.GroupID, actorID)
	if err != nil {
		return err
	}
	if banned {
		_ = s.store.UpdateInviteStatus(ctx, inviteID, "rejected")
		return ErrInviteNoLongerAcceptable
	}

	if err := s.store.UpdateInviteStatus(ctx, inviteID, "accepted"); err != nil {
		return err
	}

	if err := s.store.AddMemberWithInviter(ctx, inv.GroupID, actorID, "member", inv.InviterID); err != nil {
		return err
	}

	if g.ChatConversationID != nil {
		if err := s.syncMemberToChat(ctx, *g.ChatConversationID, actorID, true); err != nil {
			return err
		}
	}

	s.publishEvent(func() error {
		return s.producer.PublishGroupInviteAccepted(ctx, inv.GroupID, inviteID, actorID)
	})
	s.publishEvent(func() error {
		return s.producer.PublishGroupMemberJoined(ctx, inv.GroupID, actorID, "member")
	})

	s.invalidateGroupCache(ctx, inv.GroupID)
	return nil
}

// RejectInvite rejects a pending invite.
func (s *Service) RejectInvite(ctx context.Context, actorID uuid.UUID, inviteID uuid.UUID) error {
	inv, err := s.store.GetInviteByID(ctx, inviteID)
	if err != nil {
		return err
	}
	if inv == nil {
		return fmt.Errorf("invite not found")
	}
	if inv.InviteeID != actorID {
		return fmt.Errorf("forbidden: this invite is not for you")
	}
	if inv.Status != "pending" {
		return fmt.Errorf("invite is no longer pending")
	}

	if err := s.store.UpdateInviteStatus(ctx, inviteID, "rejected"); err != nil {
		return err
	}

	s.publishEvent(func() error {
		return s.producer.PublishGroupInviteRejected(ctx, inv.GroupID, inviteID, actorID)
	})

	return nil
}

// ListGroupInvites returns pending invites for a group.
func (s *Service) ListGroupInvites(ctx context.Context, actorID uuid.UUID, groupID uuid.UUID) ([]store.GroupInvite, error) {
	if _, err := s.authorize(ctx, groupID, actorID, accessWrite); err != nil {
		return nil, err
	}

	return s.store.ListGroupInvites(ctx, groupID)
}

// --- Join Requests ---

// CreateJoinRequest creates a join request for a request-to-join group.
func (s *Service) CreateJoinRequest(ctx context.Context, actorID, groupID uuid.UUID) (*store.GroupJoinRequest, error) {
	// Rate limit: 20 requests/hr
	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:join_req:%s", actorID), 20, time.Hour) {
		return nil, fmt.Errorf("rate_limited: too many join requests")
	}

	// Card mode: a private group answers as missing; the ban is read from the
	// same member row.
	acc, err := s.authorize(ctx, groupID, actorID, accessCard)
	if err != nil {
		return nil, err
	}
	if isBanned(acc.member) {
		return nil, ErrGroupBanned
	}

	if acc.g.JoinMode != "request" {
		return nil, fmt.Errorf("this group does not accept join requests")
	}

	if isActive(acc.member) {
		return nil, fmt.Errorf("already a member of this group")
	}

	jr := &store.GroupJoinRequest{
		GroupID: groupID,
		UserID:  actorID,
	}
	if err := s.store.CreateJoinRequest(ctx, jr); err != nil {
		return nil, err
	}

	s.store.IncrementPendingRequestCount(ctx, groupID)

	s.publishEvent(func() error {
		return s.producer.PublishGroupJoinRequested(ctx, groupID, actorID, jr.ID)
	})

	return jr, nil
}

// ListJoinRequests returns pending join requests for a group.
func (s *Service) ListJoinRequests(ctx context.Context, actorID, groupID uuid.UUID, limit, offset int) ([]store.GroupJoinRequest, error) {
	if _, err := s.authorizeRank(ctx, groupID, actorID, rankModerator,
		errors.New("forbidden: only admins or moderators can view join requests")); err != nil {
		return nil, err
	}

	return s.store.ListJoinRequests(ctx, groupID, limit, offset)
}

// ApproveJoinRequest approves a join request.
func (s *Service) ApproveJoinRequest(ctx context.Context, actorID uuid.UUID, requestID uuid.UUID) error {
	jr, err := s.store.GetJoinRequestByID(ctx, requestID)
	if err != nil {
		return err
	}
	if jr == nil {
		return fmt.Errorf("join request not found")
	}
	if jr.Status != "pending" {
		return fmt.Errorf("join request is no longer pending")
	}

	// Check admin/mod permission
	actor, err := s.store.GetActiveMember(ctx, jr.GroupID, actorID)
	if err != nil {
		return err
	}
	if actor == nil || (actor.Role != "admin" && actor.Role != "moderator") {
		return fmt.Errorf("forbidden: only admins or moderators can approve join requests")
	}

	if err := s.store.UpdateJoinRequestStatus(ctx, requestID, "approved", actorID); err != nil {
		return err
	}

	s.store.DecrementPendingRequestCount(ctx, jr.GroupID)

	if err := s.store.AddMember(ctx, jr.GroupID, jr.UserID, "member"); err != nil {
		return err
	}

	g, err := s.store.GetGroupByID(ctx, jr.GroupID)
	if err != nil {
		return err
	}
	if g != nil && g.ChatConversationID != nil {
		if err := s.syncMemberToChat(ctx, *g.ChatConversationID, jr.UserID, true); err != nil {
			return err
		}
	}

	s.publishEvent(func() error {
		return s.producer.PublishGroupJoinApproved(ctx, jr.GroupID, jr.UserID, requestID, actorID)
	})
	s.publishEvent(func() error {
		return s.producer.PublishGroupMemberJoined(ctx, jr.GroupID, jr.UserID, "member")
	})

	s.invalidateGroupCache(ctx, jr.GroupID)
	return nil
}

// RejectJoinRequest rejects a join request.
func (s *Service) RejectJoinRequest(ctx context.Context, actorID uuid.UUID, requestID uuid.UUID) error {
	jr, err := s.store.GetJoinRequestByID(ctx, requestID)
	if err != nil {
		return err
	}
	if jr == nil {
		return fmt.Errorf("join request not found")
	}
	if jr.Status != "pending" {
		return fmt.Errorf("join request is no longer pending")
	}

	actor, err := s.store.GetActiveMember(ctx, jr.GroupID, actorID)
	if err != nil {
		return err
	}
	if actor == nil || (actor.Role != "admin" && actor.Role != "moderator") {
		return fmt.Errorf("forbidden: only admins or moderators can reject join requests")
	}

	if err := s.store.UpdateJoinRequestStatus(ctx, requestID, "rejected", actorID); err != nil {
		return err
	}

	s.store.DecrementPendingRequestCount(ctx, jr.GroupID)

	s.publishEvent(func() error {
		return s.producer.PublishGroupJoinRejected(ctx, jr.GroupID, jr.UserID, requestID, actorID)
	})

	return nil
}

// ListMyInvites returns the actor's pending group invites with group display
// fields attached.
func (s *Service) ListMyInvites(ctx context.Context, actorID uuid.UUID) ([]store.GroupInviteDetail, error) {
	return s.store.ListInvitesForUserDetailed(ctx, actorID)
}
