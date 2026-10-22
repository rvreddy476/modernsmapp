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
	if err := store.AddMemberCapped(context.Background(), convID, newcomer, "member", 1024); err != nil {
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

	if err := store.AddMemberCapped(ctx, convID, member, "member", 1024); err != nil {
		t.Fatal(err)
	}
	first := memberCutoff(t, store, convID, member)
	if _, err := store.RemoveMember(ctx, convID, member); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := store.AddMemberCapped(ctx, convID, member, "member", 1024); err != nil {
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

	if err := store.AddMemberCapped(context.Background(), convID, member, "member", 1024); err != nil {
		t.Fatal(err)
	}
	if c := memberCutoff(t, store, convID, member); c != nil {
		t.Fatalf("re-adding an active member must not hide their history, got cutoff %v", c)
	}
}
