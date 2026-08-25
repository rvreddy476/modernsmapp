//go:build integration

package postgres

// P0-5 correction-pass proof against REAL Postgres: the governance
// transactions must preserve "exactly one active owner" under the exact race
// the independent review constructed — an admin's removal of a member racing
// the owner's transfer of ownership TO that member. Before the fix the
// removal's authorization was an advisory read outside the transaction, so
// the interleaving severed the new owner and left the group ownerless.
//
// Run: CHAT_POSTGRES_DSN=postgres://... go test ./internal/store/postgres -tags integration

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testStore(t *testing.T) (*ConversationStore, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("CHAT_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("CHAT_POSTGRES_DSN is required — use scripts/run-pg-integration-scratch.sh; " +
			"NEVER point this suite at a database holding data anyone cares about " +
			"(the package contains a DROP SCHEMA test)")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	// Idempotent bootstrap: makes these tests independent of the DROP-SCHEMA
	// test's run order on a fresh scratch database.
	if err := BootstrapSchema(context.Background(), pool, database.SetupSQL); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	return New(pool), pool
}

func seedGroup(t *testing.T, pool *pgxpool.Pool, members map[uuid.UUID]string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	convID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO chat.conversations (id, type, title, created_by) VALUES ($1, 'group', 'p05 race proof', $2)
	`, convID, ownerOf(members)); err != nil {
		t.Fatal(err)
	}
	for userID, role := range members {
		if _, err := pool.Exec(ctx, `
			INSERT INTO chat.conversation_members (conversation_id, user_id, role, joined_at)
			VALUES ($1, $2, $3, NOW())
		`, convID, userID, role); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM chat.conversation_members WHERE conversation_id = $1`, convID)
		_, _ = pool.Exec(ctx, `DELETE FROM chat.conversations WHERE id = $1`, convID)
	})
	return convID
}

func ownerOf(members map[uuid.UUID]string) uuid.UUID {
	for id, role := range members {
		if role == "owner" {
			return id
		}
	}
	return uuid.Nil
}

func activeOwnerCount(t *testing.T, pool *pgxpool.Pool, convID uuid.UUID) int {
	t.Helper()
	var count int
	err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM chat.conversation_members
		WHERE conversation_id = $1 AND role = 'owner' AND left_at IS NULL
	`, convID).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func TestRemovalNeverSeversOwnerInSQL(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()
	owner, admin := uuid.New(), uuid.New()
	convID := seedGroup(t, pool, map[uuid.UUID]string{owner: "owner", admin: "admin"})

	if _, err := store.RemoveMemberGoverned(ctx, convID, admin, owner); !errors.Is(err, ErrRoleNotPermitted) {
		t.Fatalf("severing the owner must fail with ErrRoleNotPermitted, got %v", err)
	}
	if got := activeOwnerCount(t, pool, convID); got != 1 {
		t.Fatalf("owner count after refused removal = %d, want 1", got)
	}
}

func TestOwnerLeaveRefusedWithMembersInSQL(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()
	owner, member := uuid.New(), uuid.New()
	convID := seedGroup(t, pool, map[uuid.UUID]string{owner: "owner", member: "member"})

	if _, _, err := store.LeaveGoverned(ctx, convID, owner); !errors.Is(err, ErrOwnerMustTransferStore) {
		t.Fatalf("owner leave with members present must fail, got %v", err)
	}
	if got := activeOwnerCount(t, pool, convID); got != 1 {
		t.Fatalf("owner count = %d, want 1", got)
	}
}

// TestTransferRemoveRaceKeepsExactlyOneOwner runs the review's interleaving
// many times: owner O transfers to member M while admin A concurrently
// removes M. Whatever order the two transactions serialize in, the group
// must end with EXACTLY one active owner.
func TestTransferRemoveRaceKeepsExactlyOneOwner(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()

	const rounds = 25
	for i := 0; i < rounds; i++ {
		owner, admin, member := uuid.New(), uuid.New(), uuid.New()
		convID := seedGroup(t, pool, map[uuid.UUID]string{
			owner: "owner", admin: "admin", member: "member",
		})

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = store.TransferOwnership(ctx, convID, owner, member)
		}()
		go func() {
			defer wg.Done()
			_, _ = store.RemoveMemberGoverned(ctx, convID, admin, member)
		}()
		wg.Wait()

		if got := activeOwnerCount(t, pool, convID); got != 1 {
			t.Fatalf("round %d: active owner count = %d, want exactly 1", i, got)
		}
	}
}

// TestGenerationOrderFollowsSerializationNotTxStart replays the final
// verification's Q2(a) interleaving byte-for-byte: a removal TRANSACTION
// STARTS FIRST (so its transaction-start NOW() is the OLDEST timestamp),
// then a later-started rejoin acquires the conversation lock and commits,
// then the older removal transaction acquires the lock, severs the rejoined
// row and arms its generation. With transaction-start timestamps the marker
// (t1) lost to the rejoin token (t2 > t1) even though the member ended
// REMOVED; with sequence generations allocated under the lock, the sever
// generation must outrank the rejoin generation whenever the sever
// serializes after the rejoin.
func TestGenerationOrderFollowsSerializationNotTxStart(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()
	owner, member := uuid.New(), uuid.New()
	convID := seedGroup(t, pool, map[uuid.UUID]string{owner: "owner", member: "member"})

	// Initial state for the interleaving: the member is severed.
	if _, _, err := store.SeverMemberSystem(ctx, convID, member); err != nil {
		t.Fatal(err)
	}

	// 1. The REMOVAL transaction starts FIRST — its transaction_timestamp()
	//    is now frozen at the earliest instant of the whole interleaving.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var removalTxStart time.Time
	if err := tx.QueryRow(ctx, `SELECT transaction_timestamp()`).Scan(&removalTxStart); err != nil {
		t.Fatal(err)
	}

	// 2. A LATER-started rejoin takes the conversation lock and commits.
	if err := store.AddMemberCapped(ctx, convID, member, "member", 1024); err != nil {
		t.Fatal(err)
	}
	rejoinGen, err := store.GetMemberGen(ctx, convID, member)
	if err != nil {
		t.Fatal(err)
	}
	var rejoinedAt time.Time
	if err := pool.QueryRow(ctx, `
		SELECT joined_at FROM chat.conversation_members
		WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL
	`, convID, member).Scan(&rejoinedAt); err != nil {
		t.Fatal(err)
	}

	// 3. The old removal transaction proceeds: same statements as
	//    RemoveMemberGoverned (conversation lock, sever, generation).
	var convType string
	if err := tx.QueryRow(ctx, `SELECT type FROM chat.conversations WHERE id = $1 FOR UPDATE`,
		convID).Scan(&convType); err != nil {
		t.Fatal(err)
	}
	var severGen int64
	if err := tx.QueryRow(ctx, `
		UPDATE chat.conversation_members SET left_at = NOW()
		WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL AND role <> 'owner'
		RETURNING nextval('chat.membership_gen_seq')
	`, convID, member).Scan(&severGen); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	// The clock REALLY inverted (the precondition of the reported defect):
	// the removal transaction is older than the rejoin, so a NOW()-valued
	// marker would have lost to the rejoin token.
	if !removalTxStart.Before(rejoinedAt) {
		t.Fatalf("interleaving precondition broken: removal tx start %v is not before rejoin %v",
			removalTxStart, rejoinedAt)
	}
	// The SEQUENCE did not invert: the sever generation outranks the rejoin
	// generation, so the rejoin-era token is denied — correctly, because
	// that membership ended removed.
	if severGen <= rejoinGen {
		t.Fatalf("sever generation %d must outrank the earlier-committed rejoin generation %d",
			severGen, rejoinGen)
	}
}

// TestSeverDirectConversationArmsSerializedGenerations proves the
// graph-block sever path yields per-member generations that any later
// rejoin outranks.
func TestSeverDirectConversationArmsSerializedGenerations(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()
	blocker, blocked := uuid.New(), uuid.New()
	convID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO chat.conversations (id, type, created_by) VALUES ($1, 'direct', $2)
	`, convID, blocker); err != nil {
		t.Fatal(err)
	}
	userA, userB := blocker, blocked
	if userB.String() < userA.String() {
		userA, userB = userB, userA
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO chat.direct_conversation_keys (user_a, user_b, conversation_id) VALUES ($1, $2, $3)
	`, userA, userB, convID); err != nil {
		t.Fatal(err)
	}
	for _, u := range []uuid.UUID{blocker, blocked} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO chat.conversation_members (conversation_id, user_id, role, joined_at, join_gen)
			VALUES ($1, $2, 'member', NOW(), nextval('chat.membership_gen_seq'))
		`, convID, u); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM chat.conversation_members WHERE conversation_id = $1`, convID)
		_, _ = pool.Exec(ctx, `DELETE FROM chat.direct_conversation_keys WHERE conversation_id = $1`, convID)
		_, _ = pool.Exec(ctx, `DELETE FROM chat.conversations WHERE id = $1`, convID)
	})

	gotConv, severed, err := store.SeverDirectConversation(ctx, blocker, blocked)
	if err != nil || gotConv != convID || len(severed) != 2 {
		t.Fatalf("sever: conv=%v severed=%d err=%v", gotConv, len(severed), err)
	}
	maxSeverGen := severed[0].Gen
	if severed[1].Gen > maxSeverGen {
		maxSeverGen = severed[1].Gen
	}
	// A later rejoin outranks both sever generations.
	if err := store.AddMemberCapped(ctx, convID, blocker, "member", 1024); err != nil {
		t.Fatal(err)
	}
	rejoinGen, err := store.GetMemberGen(ctx, convID, blocker)
	if err != nil {
		t.Fatal(err)
	}
	if rejoinGen <= maxSeverGen {
		t.Fatalf("rejoin generation %d must outrank sever generations (max %d)", rejoinGen, maxSeverGen)
	}
	// Redelivery reports the conversation with nothing left to sever.
	gotConv2, severed2, err := store.SeverDirectConversation(ctx, blocked, blocker)
	if err != nil || gotConv2 != convID || len(severed2) != 1 {
		t.Fatalf("re-sever after one rejoin: conv=%v severed=%d err=%v", gotConv2, len(severed2), err)
	}
}

func intentGen(t *testing.T, pool *pgxpool.Pool, convID, userID uuid.UUID) (int64, bool) {
	t.Helper()
	var gen int64
	err := pool.QueryRow(context.Background(), `
		SELECT sever_gen FROM chat.revocation_intents
		WHERE conversation_id = $1 AND user_id = $2
	`, convID, userID).Scan(&gen)
	if err != nil {
		return 0, false
	}
	return gen, true
}

// TestSeverTransactionsWriteDurableRevocationIntents (Blocker-2 final
// correction): every sever path must leave its marker debt in
// chat.revocation_intents IN THE SAME TRANSACTION, and the delete guard must
// never let an older armed generation clear a newer intent.
func TestSeverTransactionsWriteDurableRevocationIntents(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()
	owner, admin, member := uuid.New(), uuid.New(), uuid.New()
	convID := seedGroup(t, pool, map[uuid.UUID]string{owner: "owner", admin: "admin", member: "member"})
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM chat.revocation_intents WHERE conversation_id = $1`, convID)
	})

	// Governed removal.
	severGen, err := store.RemoveMemberGoverned(ctx, convID, owner, member)
	if err != nil {
		t.Fatal(err)
	}
	if gen, ok := intentGen(t, pool, convID, member); !ok || gen != severGen {
		t.Fatalf("governed removal intent: ok=%v gen=%d want %d", ok, gen, severGen)
	}

	// System sever.
	_, sysGen, err := store.SeverMemberSystem(ctx, convID, admin)
	if err != nil {
		t.Fatal(err)
	}
	if gen, ok := intentGen(t, pool, convID, admin); !ok || gen != sysGen {
		t.Fatalf("system sever intent: ok=%v gen=%d want %d", ok, gen, sysGen)
	}

	// Governed leave (owner now sole member).
	removed, leaveGen, err := store.LeaveGoverned(ctx, convID, owner)
	if err != nil || !removed {
		t.Fatalf("leave: removed=%v err=%v", removed, err)
	}
	if gen, ok := intentGen(t, pool, convID, owner); !ok || gen != leaveGen {
		t.Fatalf("leave intent: ok=%v gen=%d want %d", ok, gen, leaveGen)
	}

	// Delete guard: an OLDER armed generation must not clear a NEWER intent.
	if err := store.UpsertRevocationIntent(ctx, convID, owner, leaveGen+10); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteRevocationIntent(ctx, convID, owner, leaveGen); err != nil {
		t.Fatal(err)
	}
	if gen, ok := intentGen(t, pool, convID, owner); !ok || gen != leaveGen+10 {
		t.Fatalf("newer intent must survive an older delete: ok=%v gen=%d", ok, gen)
	}
	if err := store.DeleteRevocationIntent(ctx, convID, owner, leaveGen+10); err != nil {
		t.Fatal(err)
	}
	if _, ok := intentGen(t, pool, convID, owner); ok {
		t.Fatal("intent must clear once the armed generation covers it")
	}
}

// TestDirectSeverWritesIntentsForBothMembers: the pair sever leaves one
// durable intent per severed member with matching generations.
func TestDirectSeverWritesIntentsForBothMembers(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()
	blocker, blocked := uuid.New(), uuid.New()
	convID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO chat.conversations (id, type, created_by) VALUES ($1, 'direct', $2)
	`, convID, blocker); err != nil {
		t.Fatal(err)
	}
	userA, userB := blocker, blocked
	if userB.String() < userA.String() {
		userA, userB = userB, userA
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO chat.direct_conversation_keys (user_a, user_b, conversation_id) VALUES ($1, $2, $3)
	`, userA, userB, convID); err != nil {
		t.Fatal(err)
	}
	for _, u := range []uuid.UUID{blocker, blocked} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO chat.conversation_members (conversation_id, user_id, role, joined_at, join_gen)
			VALUES ($1, $2, 'member', NOW(), nextval('chat.membership_gen_seq'))
		`, convID, u); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM chat.revocation_intents WHERE conversation_id = $1`, convID)
		_, _ = pool.Exec(ctx, `DELETE FROM chat.conversation_members WHERE conversation_id = $1`, convID)
		_, _ = pool.Exec(ctx, `DELETE FROM chat.direct_conversation_keys WHERE conversation_id = $1`, convID)
		_, _ = pool.Exec(ctx, `DELETE FROM chat.conversations WHERE id = $1`, convID)
	})

	_, severed, err := store.SeverDirectConversation(ctx, blocker, blocked)
	if err != nil || len(severed) != 2 {
		t.Fatalf("sever: %d rows, err=%v", len(severed), err)
	}
	for _, sm := range severed {
		if gen, ok := intentGen(t, pool, convID, sm.UserID); !ok || gen != sm.Gen {
			t.Fatalf("intent for %v: ok=%v gen=%d want %d", sm.UserID, ok, gen, sm.Gen)
		}
	}
}
