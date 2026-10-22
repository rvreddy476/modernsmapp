//go:build integration

package postgres

// A newcomer to a group sees only what is sent after they join, as on
// WhatsApp (founder, 22 Oct 2026). AddMemberCapped is the one way into a
// group (invite link, admin add, accepted invitation, group-service mirror),
// so it stamps cleared_before with the join moment — on a rejoin too — and
// the history read, search and inbox preview all start there.
//
// Run: CHAT_POSTGRES_DSN=postgres://... go test ./internal/store/postgres -tags integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func memberCutoff(t *testing.T, s *ConversationStore, convID, userID uuid.UUID) *time.Time {
	t.Helper()
	cutoff, err := s.GetMemberCutoff(context.Background(), convID, userID)
	if err != nil {
		t.Fatal(err)
	}
	return cutoff
}

func TestGroupNewcomerSeesNoEarlierHistory(t *testing.T) {
	store, pool := testStore(t)
	owner, newcomer := uuid.New(), uuid.New()
	convID := seedGroup(t, pool, map[uuid.UUID]string{owner: "owner"})

	before := time.Now().Add(-time.Second)
	if err := store.AddMemberCapped(context.Background(), convID, newcomer, "member", 1024, JoinOrigin{Method: JoinMethodInviteLink}); err != nil {
		t.Fatal(err)
	}
	cutoff := memberCutoff(t, store, convID, newcomer)
	if cutoff == nil || cutoff.Before(before) {
		t.Fatalf("a newcomer's history must start at the join, got cutoff %v", cutoff)
	}
	// The existing owner keeps their whole history.
	if c := memberCutoff(t, store, convID, owner); c != nil {
		t.Fatalf("an existing member's history must be untouched, got cutoff %v", c)
	}
}

func TestGroupRejoinStartsHistoryAtTheRejoin(t *testing.T) {
	store, pool := testStore(t)
	owner, member := uuid.New(), uuid.New()
	convID := seedGroup(t, pool, map[uuid.UUID]string{owner: "owner"})
	ctx := context.Background()

	if err := store.AddMemberCapped(ctx, convID, member, "member", 1024, JoinOrigin{Method: JoinMethodInviteLink}); err != nil {
		t.Fatal(err)
	}
	first := memberCutoff(t, store, convID, member)
	if _, err := store.RemoveMember(ctx, convID, member); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := store.AddMemberCapped(ctx, convID, member, "member", 1024, JoinOrigin{Method: JoinMethodInviteLink}); err != nil {
		t.Fatal(err)
	}
	second := memberCutoff(t, store, convID, member)
	if first == nil || second == nil || !second.After(*first) {
		t.Fatalf("a rejoin must start history at the rejoin: first %v, second %v", first, second)
	}
}

func TestGroupReAddOfAnActiveMemberChangesNothing(t *testing.T) {
	store, pool := testStore(t)
	owner, member := uuid.New(), uuid.New()
	convID := seedGroup(t, pool, map[uuid.UUID]string{owner: "owner", member: "member"})

	if err := store.AddMemberCapped(context.Background(), convID, member, "member", 1024, JoinOrigin{Method: JoinMethodInviteLink}); err != nil {
		t.Fatal(err)
	}
	if c := memberCutoff(t, store, convID, member); c != nil {
		t.Fatalf("re-adding an active member must not hide their history, got cutoff %v", c)
	}
}

// The join note (founder, 22 Oct 2026): AddMemberCapped records how the
// member came in, and GetMemberJoin hands back a cutoff equal to joined_at
// exactly while that cutoff is the join's own.
func TestGroupJoinRecordsMethodAndAdder(t *testing.T) {
	store, pool := testStore(t)
	owner, member, linker := uuid.New(), uuid.New(), uuid.New()
	convID := seedGroup(t, pool, map[uuid.UUID]string{owner: "owner"})
	ctx := context.Background()

	if err := store.AddMemberCapped(ctx, convID, member, "member", 1024,
		JoinOrigin{Method: JoinMethodAdded, AddedBy: &owner}); err != nil {
		t.Fatal(err)
	}
	j, err := store.GetMemberJoin(ctx, convID, member)
	if err != nil || j == nil {
		t.Fatalf("join row: %v %v", j, err)
	}
	if j.JoinMethod == nil || *j.JoinMethod != JoinMethodAdded || j.AddedBy == nil || *j.AddedBy != owner {
		t.Fatalf("method/adder not recorded: %+v", j)
	}
	if j.ClearedBefore == nil || !j.ClearedBefore.Equal(j.JoinedAt) {
		t.Fatalf("cutoff must equal joined_at on a join: joined %v cut %v", j.JoinedAt, j.ClearedBefore)
	}
	// The seeded owner has no method and no cutoff: no note.
	if o, err := store.GetMemberJoin(ctx, convID, owner); err != nil || o == nil || o.JoinMethod != nil || o.ClearedBefore != nil {
		t.Fatalf("seeded owner: %+v %v", o, err)
	}

	// A rejoin by link replaces the origin; the adder is gone.
	if _, err := store.RemoveMember(ctx, convID, member); err != nil {
		t.Fatal(err)
	}
	if err := store.AddMemberCapped(ctx, convID, member, "member", 1024,
		JoinOrigin{Method: JoinMethodInviteLink}); err != nil {
		t.Fatal(err)
	}
	j, _ = store.GetMemberJoin(ctx, convID, member)
	if j == nil || j.JoinMethod == nil || *j.JoinMethod != JoinMethodInviteLink || j.AddedBy != nil {
		t.Fatalf("rejoin origin: %+v", j)
	}

	// Clearing the chat later moves the cutoff past the join.
	time.Sleep(20 * time.Millisecond)
	if err := store.ClearForMe(ctx, convID, member, time.Now(), false); err != nil {
		t.Fatal(err)
	}
	j, _ = store.GetMemberJoin(ctx, convID, member)
	if j == nil || j.ClearedBefore == nil || j.ClearedBefore.Equal(j.JoinedAt) {
		t.Fatalf("a clear must move the cutoff past the join: %+v", j)
	}

	// A re-add of an ACTIVE member changes nothing, origin included.
	if err := store.AddMemberCapped(ctx, convID, member, "member", 1024,
		JoinOrigin{Method: JoinMethodAdded, AddedBy: &linker}); err != nil {
		t.Fatal(err)
	}
	j, _ = store.GetMemberJoin(ctx, convID, member)
	if j == nil || *j.JoinMethod != JoinMethodInviteLink || j.AddedBy != nil {
		t.Fatalf("re-add of an active member rewrote the origin: %+v", j)
	}
}
