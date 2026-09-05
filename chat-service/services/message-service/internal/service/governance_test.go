package service

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
)

// governanceFake is an in-memory ConversationStore + groupStore + policyStore
// for the production-chat governance rules. Only the methods the tested paths
// touch have real behaviour; the rest satisfy the interface.
type governanceFake struct {
	conversations  map[uuid.UUID]*postgres.Conversation
	roles          map[uuid.UUID]map[uuid.UUID]string // conv -> user -> role
	policies       map[uuid.UUID]*postgres.UserPolicy
	invitations    map[uuid.UUID]*postgres.GroupInvitation
	latestRequest  *postgres.MessageRequest
	memberAdds     int
	outboxEvents   []string
	genCounter     int64
	genUnavailable bool
	systemSevers   int
	cappedAdds     int
	// durable revocation intents, keyed conv|user (Blocker-2 correction)
	intents map[string]int64
	// direct-pair sever fixture (SeverDirectConversationOnBlock)
	directConvID  uuid.UUID
	requestStatus map[uuid.UUID]string
	// invite links, keyed by code (chat-app pass)
	inviteLinks map[string]*postgres.GroupInviteLink
}

func intentKey(convID, userID uuid.UUID) string { return convID.String() + "|" + userID.String() }

func newGovernanceFake() *governanceFake {
	return &governanceFake{
		conversations: map[uuid.UUID]*postgres.Conversation{},
		roles:         map[uuid.UUID]map[uuid.UUID]string{},
		policies:      map[uuid.UUID]*postgres.UserPolicy{},
		invitations:   map[uuid.UUID]*postgres.GroupInvitation{},
	}
}

func (f *governanceFake) addGroup(convID uuid.UUID, owner uuid.UUID, members map[uuid.UUID]string) {
	f.conversations[convID] = &postgres.Conversation{ID: convID, Type: "group"}
	f.roles[convID] = map[uuid.UUID]string{owner: "owner"}
	for u, r := range members {
		f.roles[convID][u] = r
	}
}

// --- ConversationStore ---

func (f *governanceFake) CreateDirectConversation(ctx context.Context, a, b, c uuid.UUID) (uuid.UUID, bool, error) {
	return uuid.New(), true, nil
}
func (f *governanceFake) MarkConversationAsRequest(context.Context, uuid.UUID) error { return nil }
func (f *governanceFake) CreateMessageRequest(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	return nil
}
func (f *governanceFake) GetMessageRequestByConversation(ctx context.Context, convID uuid.UUID) (*postgres.MessageRequest, error) {
	if f.latestRequest != nil && f.latestRequest.ConversationID == convID {
		return f.latestRequest, nil
	}
	return nil, nil
}
func (f *governanceFake) SetMessageRequestPreview(context.Context, uuid.UUID, string) error {
	return nil
}
func (f *governanceFake) UpdateMessageRequestStatus(ctx context.Context, convID uuid.UUID, status string) error {
	if f.requestStatus == nil {
		f.requestStatus = map[uuid.UUID]string{}
	}
	f.requestStatus[convID] = status
	if f.latestRequest != nil && f.latestRequest.ConversationID == convID {
		f.latestRequest.Status = status
	}
	return nil
}

// SeverDirectConversation mirrors the pair-sever store call
// (final-verification P0-4): reports the conversation id and every sever it
// performed, with sequence generations.
func (f *governanceFake) SeverDirectConversation(ctx context.Context, blockerID, blockedID uuid.UUID) (uuid.UUID, []postgres.SeveredMembership, error) {
	if f.directConvID == uuid.Nil {
		return uuid.Nil, nil, nil
	}
	var severed []postgres.SeveredMembership
	for _, userID := range []uuid.UUID{blockerID, blockedID} {
		if _, ok := f.roles[f.directConvID][userID]; ok {
			delete(f.roles[f.directConvID], userID)
			gen := f.nextGen()
			f.upsertIntent(f.directConvID, userID, gen)
			severed = append(severed, postgres.SeveredMembership{
				ConversationID: f.directConvID, UserID: userID, Gen: gen,
			})
		}
	}
	return f.directConvID, severed, nil
}
func (f *governanceFake) CreateGroupConversation(ctx context.Context, creator uuid.UUID, title string, members []uuid.UUID) (uuid.UUID, error) {
	id := uuid.New()
	f.addGroup(id, creator, nil)
	for _, m := range members {
		f.roles[id][m] = "member"
	}
	return id, nil
}
func (f *governanceFake) GetConversation(ctx context.Context, id uuid.UUID) (*postgres.Conversation, error) {
	return f.conversations[id], nil
}
func (f *governanceFake) TouchConversation(context.Context, uuid.UUID, time.Time) error { return nil }
func (f *governanceFake) ListConversationsByUser(context.Context, uuid.UUID, int, *time.Time, *uuid.UUID) ([]postgres.Conversation, error) {
	return nil, nil
}
func (f *governanceFake) CheckMembership(ctx context.Context, convID, userID uuid.UUID) (bool, error) {
	role, ok := f.roles[convID][userID]
	return ok && role != "", nil
}
func (f *governanceFake) GetMembers(ctx context.Context, convID uuid.UUID) ([]postgres.Member, error) {
	var out []postgres.Member
	for u, r := range f.roles[convID] {
		out = append(out, postgres.Member{UserID: u, Role: r})
	}
	return out, nil
}
func (f *governanceFake) GetMemberRole(ctx context.Context, convID, userID uuid.UUID) (string, error) {
	return f.roles[convID][userID], nil
}
func (f *governanceFake) AddMember(ctx context.Context, convID, userID uuid.UUID, role string) error {
	f.roles[convID][userID] = role
	f.memberAdds++
	return nil
}
func (f *governanceFake) RemoveMember(ctx context.Context, convID, userID uuid.UUID) (bool, error) {
	if _, ok := f.roles[convID][userID]; !ok {
		return false, nil
	}
	delete(f.roles[convID], userID)
	return true, nil
}
func (f *governanceFake) UpdateTitle(context.Context, uuid.UUID, string) error { return nil }

// nextGen mirrors the store's membership sequence.
func (f *governanceFake) nextGen() int64 {
	f.genCounter++
	return f.genCounter
}

// RemoveMemberGoverned mirrors the store's in-transaction ladder (P0-5) and
// returns a sequence sever generation like the real store (P0-4).
func (f *governanceFake) RemoveMemberGoverned(ctx context.Context, convID, actorID, targetID uuid.UUID) (int64, error) {
	actorRole := f.roles[convID][actorID]
	targetRole, ok := f.roles[convID][targetID]
	if actorRole == "" {
		return 0, postgres.ErrRoleNotPermitted
	}
	if !ok {
		return 0, postgres.ErrNotAMember
	}
	if targetRole == "owner" {
		return 0, postgres.ErrRoleNotPermitted
	}
	if actorRole != "owner" && !(actorRole == "admin" && targetRole == "member") {
		return 0, postgres.ErrRoleNotPermitted
	}
	delete(f.roles[convID], targetID)
	gen := f.nextGen()
	f.upsertIntent(convID, targetID, gen) // intent rides the sever tx, like the real store
	return gen, nil
}

// LeaveGoverned mirrors the store's transactional self-removal (P0-5).
func (f *governanceFake) LeaveGoverned(ctx context.Context, convID, userID uuid.UUID) (bool, int64, error) {
	role, ok := f.roles[convID][userID]
	if !ok {
		return false, 0, nil
	}
	if role == "owner" && len(f.roles[convID]) > 1 {
		return false, 0, postgres.ErrOwnerMustTransferStore
	}
	delete(f.roles[convID], userID)
	gen := f.nextGen()
	f.upsertIntent(convID, userID, gen)
	return true, gen, nil
}

// SeverMemberSystem mirrors the system-authority sever (final-verification
// P0-4): no ladder, idempotent, generation on success.
func (f *governanceFake) SeverMemberSystem(ctx context.Context, convID, userID uuid.UUID) (bool, int64, error) {
	f.systemSevers++
	if _, ok := f.roles[convID][userID]; !ok {
		return false, 0, nil
	}
	delete(f.roles[convID], userID)
	gen := f.nextGen()
	f.upsertIntent(convID, userID, gen)
	return true, gen, nil
}

func (f *governanceFake) GetMemberGen(ctx context.Context, convID, userID uuid.UUID) (int64, error) {
	if _, ok := f.roles[convID][userID]; !ok {
		return 0, postgres.ErrNotAMember
	}
	return f.nextGen(), nil
}

func (f *governanceFake) NextMembershipGen(ctx context.Context) (int64, error) {
	if f.genUnavailable {
		return 0, errors.New("sequence unavailable")
	}
	return f.nextGen(), nil
}

// --- durable revocation intents (Blocker-2 final correction) ---

func (f *governanceFake) upsertIntent(convID, userID uuid.UUID, gen int64) {
	if f.intents == nil {
		f.intents = map[string]int64{}
	}
	if cur, ok := f.intents[intentKey(convID, userID)]; !ok || gen > cur {
		f.intents[intentKey(convID, userID)] = gen
	}
}

func (f *governanceFake) UpsertRevocationIntent(ctx context.Context, convID, userID uuid.UUID, gen int64) error {
	f.upsertIntent(convID, userID, gen)
	return nil
}

func (f *governanceFake) FetchPendingRevocationIntents(ctx context.Context, limit int) ([]postgres.RevocationIntent, error) {
	var out []postgres.RevocationIntent
	for key, gen := range f.intents {
		convID := uuid.MustParse(key[:36])
		userID := uuid.MustParse(key[37:])
		out = append(out, postgres.RevocationIntent{ConversationID: convID, UserID: userID, SeverGen: gen})
	}
	return out, nil
}

func (f *governanceFake) DeleteRevocationIntent(ctx context.Context, convID, userID uuid.UUID, armedGen int64) error {
	if cur, ok := f.intents[intentKey(convID, userID)]; ok && cur <= armedGen {
		delete(f.intents, intentKey(convID, userID))
	}
	return nil
}
func (f *governanceFake) InsertOutboxEvent(ctx context.Context, eventType string, payload interface{}) error {
	f.outboxEvents = append(f.outboxEvents, eventType)
	return nil
}
func (f *governanceFake) FetchUnpublishedOutboxEvents(context.Context, int) ([]postgres.OutboxEvent, error) {
	return nil, nil
}
func (f *governanceFake) MarkOutboxEventPublished(context.Context, int64) error { return nil }
func (f *governanceFake) CheckIdempotencyKey(context.Context, string) (*postgres.IdempotencyResult, error) {
	return nil, nil
}
func (f *governanceFake) CreateIdempotencyKey(context.Context, string, string) (bool, error) {
	return true, nil
}
func (f *governanceFake) SaveIdempotencyResponse(context.Context, string, string, interface{}) error {
	return nil
}
func (f *governanceFake) ReleaseIdempotencyKey(context.Context, string, string) error { return nil }
func (f *governanceFake) UpsertUserProfile(context.Context, uuid.UUID, string, *uuid.UUID) error {
	return nil
}
func (f *governanceFake) GetUserProfiles(context.Context, []uuid.UUID) (map[uuid.UUID]postgres.UserProfile, error) {
	return map[uuid.UUID]postgres.UserProfile{}, nil
}
func (f *governanceFake) CreateDatingMatchConversation(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (uuid.UUID, bool, error) {
	return uuid.Nil, false, errors.New("unused")
}
func (f *governanceFake) MarkConversationClosedByMatch(context.Context, uuid.UUID) error { return nil }
func (f *governanceFake) GetConversationMeta(context.Context, uuid.UUID) (*postgres.ConversationMeta, error) {
	return nil, nil
}

func (f *governanceFake) ReplaceLastMessage(context.Context, uuid.UUID, time.Time, string, *uuid.UUID, *time.Time) error {
	return nil
}

// --- groupStore ---

func (f *governanceFake) CountActiveMembers(ctx context.Context, convID uuid.UUID) (int, error) {
	return len(f.roles[convID]), nil
}
func (f *governanceFake) AddMemberCapped(ctx context.Context, convID, userID uuid.UUID, role string, cap int) error {
	f.cappedAdds++
	if len(f.roles[convID]) >= cap {
		return postgres.ErrGroupFull
	}
	return f.AddMember(ctx, convID, userID, role)
}
func (f *governanceFake) SetMemberRole(ctx context.Context, convID, userID uuid.UUID, role string) (bool, error) {
	if f.roles[convID][userID] == "" || f.roles[convID][userID] == "owner" {
		return false, nil
	}
	f.roles[convID][userID] = role
	return true, nil
}
func (f *governanceFake) TransferOwnership(ctx context.Context, convID, from, to uuid.UUID) error {
	if f.roles[convID][from] != "owner" {
		return postgres.ErrOwnerRequired
	}
	if f.roles[convID][to] == "" {
		return errors.New("new owner must be an active member")
	}
	f.roles[convID][from] = "admin"
	f.roles[convID][to] = "owner"
	return nil
}
func (f *governanceFake) UpdateGroupInfo(context.Context, uuid.UUID, *string, *uuid.UUID, *string) error {
	return nil
}
func (f *governanceFake) CreateGroupInvitation(ctx context.Context, convID, inviter, invitee uuid.UUID) (*postgres.GroupInvitation, bool, error) {
	for _, inv := range f.invitations {
		if inv.ConversationID == convID && inv.InviteeID == invitee && inv.Status == "pending" {
			return inv, false, nil
		}
	}
	inv := &postgres.GroupInvitation{
		ID: uuid.New(), ConversationID: convID, InviterID: inviter, InviteeID: invitee,
		Status: "pending", CreatedAt: time.Now(),
	}
	f.invitations[inv.ID] = inv
	return inv, true, nil
}
func (f *governanceFake) GetGroupInvitation(ctx context.Context, id uuid.UUID) (*postgres.GroupInvitation, error) {
	return f.invitations[id], nil
}
func (f *governanceFake) ListPendingInvitationsForUser(context.Context, uuid.UUID, int) ([]postgres.GroupInvitation, error) {
	return nil, nil
}
func (f *governanceFake) ResolveGroupInvitation(ctx context.Context, id uuid.UUID, status string) (bool, error) {
	inv := f.invitations[id]
	if inv == nil || inv.Status != "pending" {
		return false, nil
	}
	inv.Status = status
	return true, nil
}
func (f *governanceFake) GetLatestRequestBetween(context.Context, uuid.UUID, uuid.UUID) (*postgres.MessageRequest, error) {
	return f.latestRequest, nil
}
func (f *governanceFake) UpsertReadCursor(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, time.Time) error {
	return nil
}
func (f *governanceFake) GetReadCursors(context.Context, uuid.UUID, []uuid.UUID) (map[uuid.UUID]postgres.ReadCursor, error) {
	return map[uuid.UUID]postgres.ReadCursor{}, nil
}

// --- policyStore ---

func (f *governanceFake) GetUserPolicy(ctx context.Context, userID uuid.UUID) (*postgres.UserPolicy, error) {
	return f.policies[userID], nil
}
func (f *governanceFake) UpsertUserPolicy(ctx context.Context, p postgres.UserPolicy) error {
	f.policies[p.UserID] = &p
	return nil
}
func (f *governanceFake) InvalidateUserPolicy(ctx context.Context, userID uuid.UUID) error {
	delete(f.policies, userID)
	return nil
}

func (f *governanceFake) freshPolicy(userID uuid.UUID, paused bool) {
	f.policies[userID] = &postgres.UserPolicy{
		UserID: userID, ChatPaused: paused, SendTypingIndicators: true,
		ReadReceiptsVisibility: "everyone", RefreshedAt: time.Now(),
	}
}

func newGovernanceService(f *governanceFake) *Service {
	return &Service{convStore: f, log: slog.Default(), httpClient: nil}
}

// --- Tests ---

func TestOwnerCannotLeaveWithMembersPresent(t *testing.T) {
	f := newGovernanceFake()
	owner, member := uuid.New(), uuid.New()
	convID := uuid.New()
	f.addGroup(convID, owner, map[uuid.UUID]string{member: "member"})
	svc := newGovernanceService(f)

	if err := svc.LeaveConversation(context.Background(), owner, convID); !errors.Is(err, ErrOwnerMustTransfer) {
		t.Fatalf("owner leave with members present: got %v, want ErrOwnerMustTransfer", err)
	}
	// A plain member always leaves.
	if err := svc.LeaveConversation(context.Background(), member, convID); err != nil {
		t.Fatalf("member leave failed: %v", err)
	}
	// Sole remaining owner may now leave.
	if err := svc.LeaveConversation(context.Background(), owner, convID); err != nil {
		t.Fatalf("sole owner leave failed: %v", err)
	}
}

func TestRemoveMemberRoleLadder(t *testing.T) {
	f := newGovernanceFake()
	owner, admin, admin2, member := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	convID := uuid.New()
	f.addGroup(convID, owner, map[uuid.UUID]string{admin: "admin", admin2: "admin", member: "member"})
	svc := newGovernanceService(f)
	ctx := context.Background()

	// Admin removes a member: allowed.
	if err := svc.RemoveMemberGoverned(ctx, admin, convID, member); err != nil {
		t.Fatalf("admin removing member: %v", err)
	}
	// Admin removes another admin: refused.
	if err := svc.RemoveMemberGoverned(ctx, admin, convID, admin2); !errors.Is(err, ErrNotPermittedRole) {
		t.Fatalf("admin removing admin: got %v, want ErrNotPermittedRole", err)
	}
	// Owner removes an admin: allowed.
	if err := svc.RemoveMemberGoverned(ctx, owner, convID, admin2); err != nil {
		t.Fatalf("owner removing admin: %v", err)
	}
	// Nobody removes the owner.
	if err := svc.RemoveMemberGoverned(ctx, admin, convID, owner); !errors.Is(err, ErrNotPermittedRole) {
		t.Fatalf("admin removing owner: got %v, want ErrNotPermittedRole", err)
	}
}

func TestOwnershipTransferRules(t *testing.T) {
	f := newGovernanceFake()
	owner, admin, stranger := uuid.New(), uuid.New(), uuid.New()
	convID := uuid.New()
	f.addGroup(convID, owner, map[uuid.UUID]string{admin: "admin"})
	svc := newGovernanceService(f)
	ctx := context.Background()

	// Non-owner cannot transfer.
	if err := svc.TransferOwnershipGoverned(ctx, admin, convID, owner); !errors.Is(err, ErrNotPermittedRole) {
		t.Fatalf("admin transferring: got %v", err)
	}
	// Owner cannot transfer to a non-member.
	if err := svc.TransferOwnershipGoverned(ctx, owner, convID, stranger); err == nil {
		t.Fatal("transfer to non-member must fail")
	}
	// Valid transfer: exactly one owner afterwards.
	if err := svc.TransferOwnershipGoverned(ctx, owner, convID, admin); err != nil {
		t.Fatalf("valid transfer failed: %v", err)
	}
	if f.roles[convID][admin] != "owner" || f.roles[convID][owner] != "admin" {
		t.Fatalf("roles after transfer: %v", f.roles[convID])
	}
}

func TestRoleChangeOwnerOnly(t *testing.T) {
	f := newGovernanceFake()
	owner, admin, member := uuid.New(), uuid.New(), uuid.New()
	convID := uuid.New()
	f.addGroup(convID, owner, map[uuid.UUID]string{admin: "admin", member: "member"})
	svc := newGovernanceService(f)
	ctx := context.Background()

	if err := svc.SetMemberRoleGoverned(ctx, admin, convID, member, "admin"); !errors.Is(err, ErrNotPermittedRole) {
		t.Fatalf("admin promoting: got %v", err)
	}
	if err := svc.SetMemberRoleGoverned(ctx, owner, convID, member, "admin"); err != nil {
		t.Fatalf("owner promoting: %v", err)
	}
	if f.roles[convID][member] != "admin" {
		t.Fatal("promotion did not apply")
	}
	// Idempotent re-apply.
	if err := svc.SetMemberRoleGoverned(ctx, owner, convID, member, "admin"); err != nil {
		t.Fatalf("idempotent promote: %v", err)
	}
}

func TestInvitationAcceptRules(t *testing.T) {
	f := newGovernanceFake()
	owner, invitee, other := uuid.New(), uuid.New(), uuid.New()
	convID := uuid.New()
	f.addGroup(convID, owner, nil)
	// Accept now consults graph's roster block check (P0-5); an unblocked
	// stub keeps this test about the accept rules themselves.
	svc := newGovernanceServiceWithGraph(f, stubGraph(t, nil))
	ctx := context.Background()

	inv, created, err := f.CreateGroupInvitation(ctx, convID, owner, invitee)
	if err != nil || !created {
		t.Fatalf("seed invitation: %v created=%v", err, created)
	}
	// A retried create collapses onto the pending row.
	_, createdAgain, _ := f.CreateGroupInvitation(ctx, convID, owner, invitee)
	if createdAgain {
		t.Fatal("duplicate pending invitation was created")
	}
	// Only the invitee accepts.
	if err := svc.AcceptGroupInvitation(ctx, other, inv.ID); err == nil {
		t.Fatal("non-invitee accept must fail")
	}
	if err := svc.AcceptGroupInvitation(ctx, invitee, inv.ID); err != nil {
		t.Fatalf("invitee accept: %v", err)
	}
	if f.roles[convID][invitee] != "member" {
		t.Fatal("accept did not create membership")
	}
	adds := f.memberAdds
	// Re-accept is an idempotent success and adds nothing.
	if err := svc.AcceptGroupInvitation(ctx, invitee, inv.ID); err != nil {
		t.Fatalf("re-accept: %v", err)
	}
	if f.memberAdds != adds {
		t.Fatal("re-accept performed a second membership add")
	}
}

func TestDirectCreateFailsClosedOnOwnPolicy(t *testing.T) {
	f := newGovernanceFake()
	actor, target := uuid.New(), uuid.New()
	svc := newGovernanceService(f)
	ctx := context.Background()

	// UNKNOWN own policy (no projection, no identity URL): creation is a
	// sensitive action and fails closed.
	if _, err := svc.CreateDirectConversation(ctx, actor, target, "key-1"); !errors.Is(err, ErrMessagingNotAllowed) {
		t.Fatalf("unknown policy: got %v, want ErrMessagingNotAllowed", err)
	}
	// PAUSED actor: denied.
	f.freshPolicy(actor, true)
	if _, err := svc.CreateDirectConversation(ctx, actor, target, "key-2"); !errors.Is(err, ErrMessagingNotAllowed) {
		t.Fatalf("paused actor: got %v, want ErrMessagingNotAllowed", err)
	}
}

func TestTypingGateFailsClosed(t *testing.T) {
	f := newGovernanceFake()
	user := uuid.New()
	convID := uuid.New()
	f.addGroup(convID, user, nil)
	svc := newGovernanceService(f)
	ctx := context.Background()

	// Unknown policy: silently succeeds WITHOUT publishing (rdb is nil — a
	// publish attempt would panic, so success proves the gate fired).
	if err := svc.SetTyping(ctx, user, convID); err != nil {
		t.Fatalf("typing with unknown policy: %v", err)
	}
	// Toggle off: same silence.
	f.policies[user] = &postgres.UserPolicy{
		UserID: user, SendTypingIndicators: false,
		ReadReceiptsVisibility: "everyone", RefreshedAt: time.Now(),
	}
	if err := svc.SetTyping(ctx, user, convID); err != nil {
		t.Fatalf("typing with toggle off: %v", err)
	}
}

func TestRequestCooldownBlocksRotatedKeys(t *testing.T) {
	f := newGovernanceFake()
	actor, target := uuid.New(), uuid.New()
	f.freshPolicy(actor, false)
	respondedAt := time.Now().Add(-time.Hour)
	f.latestRequest = &postgres.MessageRequest{
		SenderID: actor, ReceiverID: target,
		Status: "ignored", RespondedAt: &respondedAt,
	}
	svc := newGovernanceService(f)
	// graphServiceURL is empty → checkMessagePermission degrades to
	// request-only (allowed=false, asRequest=true), which hits the cooldown.
	if _, err := svc.CreateDirectConversation(context.Background(), actor, target, "rotated-key"); !errors.Is(err, ErrRequestCooldown) {
		t.Fatalf("cooldown: got %v, want ErrRequestCooldown", err)
	}
}
