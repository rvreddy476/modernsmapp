//go:build integration

package postgres

// Chat-app pass (2026-09-05) against REAL Postgres:
//
//   - a FRESH schema admits role='owner' (the CREATE TABLE CHECK in setup.sql
//     used to allow only admin/member while the service wrote owner);
//   - invite-link consumption is atomic against max_uses.
//
// Run: sh scripts/run-pg-integration-scratch.sh -run 'FreshSchema|InviteLink'

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/database"
	"github.com/google/uuid"
)

func TestFreshSchemaAdmitsOwnerRole(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()

	// Drop and rebuild so the CREATE TABLE path (not the 005 DO-block swap)
	// is what admits the role.
	if _, err := pool.Exec(ctx, `DROP SCHEMA chat CASCADE`); err != nil {
		t.Fatal(err)
	}
	if err := BootstrapSchema(ctx, pool, database.SetupSQL); err != nil {
		t.Fatalf("bootstrap on fresh schema: %v", err)
	}

	creator := uuid.New()
	convID, err := store.CreateGroupConversation(ctx, creator, "fresh owner", []uuid.UUID{uuid.New()})
	if err != nil {
		t.Fatalf("CreateGroupConversation (writes role='owner') on fresh schema: %v", err)
	}
	role, err := store.GetMemberRole(ctx, convID, creator)
	if err != nil || role != "owner" {
		t.Fatalf("creator role = %q, %v; want owner", role, err)
	}
	if err := store.AddMember(ctx, convID, uuid.New(), "admin"); err != nil {
		t.Fatalf("admin insert: %v", err)
	}
	// The CHECK still rejects anything outside the three roles.
	_, err = pool.Exec(ctx, `
		INSERT INTO chat.conversation_members (conversation_id, user_id, role) VALUES ($1, $2, 'moderator')
	`, convID, uuid.New())
	if err == nil || !strings.Contains(err.Error(), "conversation_members_role_check") {
		t.Fatalf("role='moderator' should violate the CHECK, got %v", err)
	}

	// Description column exists on a fresh schema and round-trips.
	desc := "about us"
	if err := store.UpdateGroupInfo(ctx, convID, nil, nil, &desc); err != nil {
		t.Fatal(err)
	}
	conv, err := store.GetConversation(ctx, convID)
	if err != nil || conv == nil || conv.Description != desc {
		t.Fatalf("description round-trip: %+v %v", conv, err)
	}
}

func TestInviteLinkConsumeIsAtomicAgainstMaxUses(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()

	convID := seedGroup(t, pool, map[uuid.UUID]string{uuid.New(): "owner"})
	one := 1
	expires := time.Now().Add(time.Hour)
	link, err := store.CreateInviteLink(ctx, convID, uuid.New(), "ABCDEFGH23", &expires, &one)
	if err != nil {
		t.Fatal(err)
	}

	const racers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	won := 0
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := store.ConsumeInviteLink(ctx, link.Code); err == nil {
				mu.Lock()
				won++
				mu.Unlock()
			} else if !errors.Is(err, ErrInviteLinkNotLive) {
				t.Errorf("unexpected consume error: %v", err)
			}
		}()
	}
	wg.Wait()
	if won != 1 {
		t.Fatalf("max_uses=1 link consumed %d times", won)
	}

	// Rotation revokes the previous live link; exactly one live per group.
	next, err := store.CreateInviteLink(ctx, convID, uuid.New(), "ABCDEFGH24", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	live, err := store.GetLiveInviteLink(ctx, convID)
	if err != nil || live == nil || live.Code != next.Code {
		t.Fatalf("live link after rotation: %+v %v", live, err)
	}
	old, _ := store.GetInviteLinkByCode(ctx, link.Code)
	if old == nil || old.RevokedAt == nil {
		t.Fatalf("rotated link not revoked: %+v", old)
	}
	if revoked, err := store.RevokeInviteLink(ctx, convID); err != nil || !revoked {
		t.Fatalf("revoke: %v %v", revoked, err)
	}
	if _, err := store.ConsumeInviteLink(ctx, next.Code); !errors.Is(err, ErrInviteLinkNotLive) {
		t.Fatalf("consume after revoke: %v", err)
	}
}
