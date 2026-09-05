package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	sharedEvents "github.com/atpost/chat-shared/events"
	"github.com/google/uuid"
)

// --- invite-link surface on the governance fake ---

func (f *governanceFake) links() map[string]*postgres.GroupInviteLink {
	if f.inviteLinks == nil {
		f.inviteLinks = map[string]*postgres.GroupInviteLink{}
	}
	return f.inviteLinks
}

func (f *governanceFake) CreateInviteLink(ctx context.Context, convID, createdBy uuid.UUID, code string, expiresAt *time.Time, maxUses *int) (*postgres.GroupInviteLink, error) {
	now := time.Now()
	for _, l := range f.links() {
		if l.ConversationID == convID && l.RevokedAt == nil {
			l.RevokedAt = &now
		}
	}
	l := &postgres.GroupInviteLink{Code: code, ConversationID: convID, CreatedBy: createdBy, CreatedAt: now, ExpiresAt: expiresAt, MaxUses: maxUses}
	f.links()[code] = l
	return l, nil
}
func (f *governanceFake) GetLiveInviteLink(ctx context.Context, convID uuid.UUID) (*postgres.GroupInviteLink, error) {
	for _, l := range f.links() {
		if l.ConversationID == convID && l.RevokedAt == nil {
			return l, nil
		}
	}
	return nil, nil
}
func (f *governanceFake) GetInviteLinkByCode(ctx context.Context, code string) (*postgres.GroupInviteLink, error) {
	return f.links()[code], nil
}
func (f *governanceFake) RevokeInviteLink(ctx context.Context, convID uuid.UUID) (bool, error) {
	now := time.Now()
	revoked := false
	for _, l := range f.links() {
		if l.ConversationID == convID && l.RevokedAt == nil {
			l.RevokedAt = &now
			revoked = true
		}
	}
	return revoked, nil
}
func (f *governanceFake) ConsumeInviteLink(ctx context.Context, code string) (*postgres.GroupInviteLink, error) {
	l := f.links()[code]
	if l == nil || !inviteLinkIsLive(l, time.Now()) {
		return nil, postgres.ErrInviteLinkNotLive
	}
	l.Uses++
	return l, nil
}

// --- tests ---

func TestGenerateInviteCodeShape(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		code, err := GenerateInviteCode()
		if err != nil {
			t.Fatal(err)
		}
		if !ValidInviteCode(code) {
			t.Fatalf("generated code %q fails its own validator", code)
		}
		if seen[code] {
			t.Fatalf("duplicate code %q in 200 draws", code)
		}
		seen[code] = true
	}
	for _, bad := range []string{"", "ABCDEFGHI", "ABCDEFGHIJK", "abcdefghij", "ABCDEFGH01", "ABCDEFGH-J"} {
		if ValidInviteCode(bad) {
			t.Fatalf("%q should not validate", bad)
		}
	}
}

func TestInviteLinkMintRequiresOwnerOrAdmin(t *testing.T) {
	f := newGovernanceFake()
	owner, admin, member := uuid.New(), uuid.New(), uuid.New()
	convID := uuid.New()
	f.addGroup(convID, owner, map[uuid.UUID]string{admin: "admin", member: "member"})
	svc := newGovernanceService(f)
	ctx := context.Background()

	if _, err := svc.CreateInviteLink(ctx, member, convID, 0, nil); !errors.Is(err, ErrNotPermittedRole) {
		t.Fatalf("member mint: got %v, want ErrNotPermittedRole", err)
	}
	link, err := svc.CreateInviteLink(ctx, admin, convID, 0, nil)
	if err != nil {
		t.Fatalf("admin mint: %v", err)
	}
	if !ValidInviteCode(link.Code) || link.ExpiresAt == nil {
		t.Fatalf("minted link malformed: %+v", link)
	}
	if got := time.Until(*link.ExpiresAt); got < DefaultInviteLinkTTL-time.Minute || got > DefaultInviteLinkTTL {
		t.Fatalf("default expiry not ~7d: %v", got)
	}
	// Rotation: a second mint revokes the first.
	second, err := svc.CreateInviteLink(ctx, owner, convID, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	if current, err := svc.GetInviteLink(ctx, owner, convID); err != nil || current.Code != second.Code {
		t.Fatalf("live link after rotation: %+v %v (want %s)", current, err, second.Code)
	}
	if _, err := svc.JoinByInvite(ctx, uuid.New(), link.Code); !errors.Is(err, ErrInviteLinkNotLive) {
		t.Fatalf("rotated link should be dead: %v", err)
	}
	// Revoke, then nothing is live.
	if err := svc.RevokeInviteLink(ctx, owner, convID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetInviteLink(ctx, owner, convID); !errors.Is(err, ErrInviteLinkNotFound) {
		t.Fatalf("after revoke: got %v, want ErrInviteLinkNotFound", err)
	}
}

func TestJoinByInviteAddsMemberAndIsIdempotent(t *testing.T) {
	f := newGovernanceFake()
	owner, joiner := uuid.New(), uuid.New()
	convID := uuid.New()
	f.addGroup(convID, owner, nil)
	title := "Weekend riders"
	f.conversations[convID].Title = &title
	svc := newGovernanceServiceWithGraph(f, stubGraph(t, nil))
	ctx := context.Background()

	link, err := svc.CreateInviteLink(ctx, owner, convID, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := svc.PreviewInvite(ctx, joiner, link.Code)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Title != title || preview.MemberCount != 1 || !preview.IsLive || preview.IsMember {
		t.Fatalf("preview: %+v", preview)
	}

	conv, err := svc.JoinByInvite(ctx, joiner, link.Code)
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	if conv.ID != convID || f.roles[convID][joiner] != "member" {
		t.Fatalf("joiner not a member after join: role=%q", f.roles[convID][joiner])
	}
	if f.links()[link.Code].Uses != 1 {
		t.Fatalf("uses after join = %d, want 1", f.links()[link.Code].Uses)
	}
	var sawAdded bool
	for _, e := range f.outboxEvents {
		if e == sharedEvents.MemberAdded {
			sawAdded = true
		}
	}
	if !sawAdded {
		t.Fatal("join did not emit MemberAdded")
	}
	// Re-join is a no-op that consumes nothing.
	if _, err := svc.JoinByInvite(ctx, joiner, link.Code); err != nil {
		t.Fatalf("idempotent join: %v", err)
	}
	if f.links()[link.Code].Uses != 1 || f.cappedAdds != 1 {
		t.Fatalf("idempotent join consumed a use or re-added: uses=%d adds=%d", f.links()[link.Code].Uses, f.cappedAdds)
	}
	if p, _ := svc.PreviewInvite(ctx, joiner, link.Code); !p.IsMember || p.MemberCount != 2 {
		t.Fatalf("preview after join: %+v", p)
	}
}

func TestJoinByInviteRefusesDeadLinks(t *testing.T) {
	f := newGovernanceFake()
	owner := uuid.New()
	convID := uuid.New()
	f.addGroup(convID, owner, nil)
	svc := newGovernanceServiceWithGraph(f, stubGraph(t, nil))
	ctx := context.Background()

	if _, err := svc.JoinByInvite(ctx, uuid.New(), "ZZZZZZZZZZ"); !errors.Is(err, ErrInviteLinkNotFound) {
		t.Fatalf("unknown code: %v", err)
	}
	if _, err := svc.JoinByInvite(ctx, uuid.New(), "not-a-code"); !errors.Is(err, ErrInviteLinkNotFound) {
		t.Fatalf("malformed code: %v", err)
	}

	// Exhausted: max_uses=1.
	one := 1
	link, err := svc.CreateInviteLink(ctx, owner, convID, 0, &one)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.JoinByInvite(ctx, uuid.New(), link.Code); err != nil {
		t.Fatalf("first use: %v", err)
	}
	if _, err := svc.JoinByInvite(ctx, uuid.New(), link.Code); !errors.Is(err, ErrInviteLinkNotLive) {
		t.Fatalf("second use of max_uses=1: %v", err)
	}
	if p, _ := svc.PreviewInvite(ctx, uuid.New(), link.Code); p.IsLive {
		t.Fatal("exhausted link previews as live")
	}

	// Expired.
	expired, _ := svc.CreateInviteLink(ctx, owner, convID, time.Minute, nil)
	past := time.Now().Add(-time.Second)
	f.links()[expired.Code].ExpiresAt = &past
	if _, err := svc.JoinByInvite(ctx, uuid.New(), expired.Code); !errors.Is(err, ErrInviteLinkNotLive) {
		t.Fatalf("expired: %v", err)
	}

	// TTL bound.
	if _, err := svc.CreateInviteLink(ctx, owner, convID, 400*24*time.Hour, nil); err == nil {
		t.Fatal("400-day ttl accepted")
	}
}

func TestJoinByInviteHonoursRosterBlocks(t *testing.T) {
	f := newGovernanceFake()
	owner, member, joiner := uuid.New(), uuid.New(), uuid.New()
	convID := uuid.New()
	f.addGroup(convID, owner, map[uuid.UUID]string{member: "member"})
	// joiner and an existing member block each other.
	svc := newGovernanceServiceWithGraph(f, stubGraph(t, map[uuid.UUID][]uuid.UUID{joiner: {member}}))
	ctx := context.Background()

	link, _ := svc.CreateInviteLink(ctx, owner, convID, 0, nil)
	if _, err := svc.JoinByInvite(ctx, joiner, link.Code); !errors.Is(err, ErrInviteJoinBlocked) {
		t.Fatalf("blocked join: got %v, want ErrInviteJoinBlocked", err)
	}
	if _, ok := f.roles[convID][joiner]; ok {
		t.Fatal("blocked joiner was added")
	}
	if f.links()[link.Code].Uses != 0 {
		t.Fatal("blocked join consumed a use")
	}
}

func TestGroupDescriptionBounded(t *testing.T) {
	f := newGovernanceFake()
	owner := uuid.New()
	convID := uuid.New()
	f.addGroup(convID, owner, nil)
	svc := newGovernanceService(f)
	ctx := context.Background()

	ok := strings.Repeat("é", 300)
	if err := svc.UpdateGroupInfoGoverned(ctx, owner, convID, nil, nil, &ok); err != nil {
		t.Fatalf("300-rune description rejected: %v", err)
	}
	tooLong := strings.Repeat("é", 301)
	if err := svc.UpdateGroupInfoGoverned(ctx, owner, convID, nil, nil, &tooLong); err == nil {
		t.Fatal("301-rune description accepted")
	}
	if err := svc.UpdateGroupInfoGoverned(ctx, uuid.New(), convID, nil, nil, &ok); err == nil {
		t.Fatal("non-member edited the description")
	}
}
