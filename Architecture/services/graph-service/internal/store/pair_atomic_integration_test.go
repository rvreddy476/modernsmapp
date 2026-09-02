//go:build integration

package store

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/atpost/shared/events"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Module 3 M3-P0-6 / SR-2 acceptance criteria, against live PostgreSQL.
//
//	POSTGRES_DSN=postgres://... go test -tags integration ./internal/store/ -run PairAtomic -v
//
// WHAT WAS WRONG WITH THE PREVIOUS VERSION OF THIS FILE
//
// It passed with the advisory lock removed, so it was not evidence. Three
// independent reasons, all fixed here:
//
//  1. The racers issued RAW `INSERT INTO follows ...` statements. Raw SQL
//     bypasses every guard in the production code, so the test exercised
//     PostgreSQL's own row locking, not this service's. It could not detect a
//     change to the service at all.
//  2. Only BlockAtomic took the pair lock; the follow path did not. Mutual
//     exclusion that one side skips excludes nothing, so removing the lock
//     changed no observable behaviour.
//  3. The test ran a SECOND, idempotent BlockAtomic after the concurrent
//     phase and before asserting. That convergence sweep deleted anything
//     that had landed late — the cleanup hid the very defect being tested.
//
// This version drives the racers through the production store methods, takes
// the lock on both sides, and asserts on the state left by the concurrent
// phase with NOTHING run in between.

// pairFixture creates two seeded users and returns them. Cleanup runs after
// the assertions (t.Cleanup is LIFO and fires at test end), never before.
func pairFixture(t *testing.T, pool *pgxpool.Pool) (uuid.UUID, uuid.UUID) {
	t.Helper()
	a, b := uuid.New(), uuid.New()
	seedUsers(t, pool, a, b)
	t.Cleanup(func() {
		ctx := context.Background()
		for _, q := range []string{
			`DELETE FROM circle_members WHERE user_id IN ($1,$2) OR circle_id IN (SELECT id FROM circles WHERE owner_id IN ($1,$2))`,
			`DELETE FROM circles WHERE owner_id IN ($1,$2)`,
			`DELETE FROM close_friends WHERE user_id IN ($1,$2) OR friend_id IN ($1,$2)`,
			`DELETE FROM favorites WHERE user_id IN ($1,$2) OR target_id IN ($1,$2)`,
			`DELETE FROM relationship_labels WHERE user_id IN ($1,$2) OR target_id IN ($1,$2)`,
			`DELETE FROM connection_requests WHERE sender_id IN ($1,$2) OR receiver_id IN ($1,$2)`,
			`DELETE FROM follow_requests WHERE requester_id IN ($1,$2) OR target_id IN ($1,$2)`,
			`DELETE FROM connections WHERE user_a IN ($1,$2) OR user_b IN ($1,$2)`,
			`DELETE FROM follows WHERE follower_id IN ($1,$2) OR followee_id IN ($1,$2)`,
			`DELETE FROM blocks WHERE blocker_id IN ($1,$2) OR blocked_id IN ($1,$2)`,
			`DELETE FROM graph_outbox_events WHERE actor_id IN ($1,$2) OR target_id IN ($1,$2)`,
			`DELETE FROM graph_pair_seq WHERE lo_id IN ($1,$2) OR hi_id IN ($1,$2)`,
			`DELETE FROM counts WHERE user_id IN ($1,$2)`,
			`DELETE FROM users WHERE id IN ($1,$2)`,
		} {
			_, _ = pool.Exec(ctx, q, a, b)
		}
	})
	return a, b
}

// assertNoRelationship reports EVERY surviving link, not just the first, so a
// failure names all the tables that leaked rather than one at a time.
func assertNoRelationship(t *testing.T, pool *pgxpool.Pool, a, b uuid.UUID, context_ string) {
	t.Helper()
	leaked := false
	for _, tbl := range allRelationshipTables {
		var n int
		if err := pool.QueryRow(context.Background(), tbl.countSQL, a, b).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", tbl.name, err)
		}
		if n > 0 {
			leaked = true
			t.Errorf("%s: %d row(s) survived in %s across a block", context_, n, tbl.name)
		}
	}
	if leaked {
		t.FailNow()
	}
}

// Criterion 1: randomized concurrent relationship creation racing a block,
// driven entirely through the production store methods. After the concurrent
// phase, zero prohibited relationships may remain — asserted with no
// intervening cleanup or convergence pass.
func TestPairAtomic_ConcurrentRelationshipCreationVersusBlockLeavesNothing(t *testing.T) {
	pool := graphPool(t)
	s := New(pool)
	ctx := context.Background()

	const iterations = 50
	const racersPerIteration = 20 // 50 x 20 = 1000 randomized operations

	var blockedRejections, accepted int64

	for i := 0; i < iterations; i++ {
		alice, bob := pairFixture(t, pool)

		// A circle to race circle-member adds against.
		var circleID uuid.UUID
		if err := pool.QueryRow(ctx,
			`INSERT INTO circles (owner_id, name) VALUES ($1, 'test') RETURNING id`,
			alice).Scan(&circleID); err != nil {
			t.Fatalf("create circle: %v", err)
		}

		// Stagger the block so it lands at a different point in the racer
		// sequence each iteration; a fixed ordering would only ever exercise
		// one interleaving.
		blockAfter := rand.Intn(racersPerIteration)

		var wg sync.WaitGroup
		start := make(chan struct{})

		for op := 0; op < racersPerIteration; op++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				<-start

				var err error
				switch n % 6 {
				case 0:
					_, err = s.FollowAtomic(ctx, alice, bob)
				case 1:
					_, err = s.FollowAtomic(ctx, bob, alice)
				case 2:
					err = s.SendConnectionRequestAtomic(ctx, alice, bob, "profile", "")
				case 3:
					err = s.AddCloseFriendAtomic(ctx, alice, bob, "manual")
				case 4:
					err = s.AddFavoriteAtomic(ctx, bob, alice)
				case 5:
					err = s.AddCircleMemberAtomic(ctx, circleID, alice, bob)
				}
				switch {
				case err == nil:
					atomic.AddInt64(&accepted, 1)
				case errors.Is(err, ErrBlockedPair):
					atomic.AddInt64(&blockedRejections, 1)
				default:
					t.Errorf("racer %d: unexpected error: %v", n, err)
				}
			}(op)
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			// Let some racers get in first, then block.
			for j := 0; j < blockAfter; j++ {
				runtimeYield()
			}
			if _, err := s.BlockAtomic(ctx, alice, bob); err != nil {
				t.Errorf("block: %v", err)
			}
		}()

		close(start)
		wg.Wait()

		// ── ASSERT IMMEDIATELY. Nothing runs between the concurrent phase
		// and this check: no second block, no convergence pass, no cleanup.
		// A sweep here would delete exactly the evidence of the race.
		assertNoRelationship(t, pool, alice, bob, "after concurrent phase")

		var blockExists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM blocks WHERE blocker_id=$1 AND blocked_id=$2)`,
			alice, bob).Scan(&blockExists); err != nil {
			t.Fatalf("check block: %v", err)
		}
		if !blockExists {
			t.Fatal("the block itself did not survive the concurrent phase")
		}
	}

	// The test is only meaningful if the racers actually contended with the
	// block. If nothing was ever rejected, every racer completed before the
	// block landed and the interleaving was never exercised.
	if blockedRejections == 0 {
		t.Fatal("no racer was ever rejected by the block: the concurrent phase " +
			"never actually raced, so this test proves nothing")
	}
	t.Logf("racers: %d accepted, %d rejected by the block", accepted, blockedRejections)
}

// runtimeYield gives other goroutines a chance to run without a timed sleep,
// so the interleaving varies with scheduling rather than wall clock.
func runtimeYield() {
	ch := make(chan struct{})
	go close(ch)
	<-ch
}

// Every relationship-creating path must refuse a blocked pair, in BOTH
// directions. A one-way check lets the blocked party re-establish the link.
func TestPairAtomic_EveryCreatePathRefusesABlockedPairBothWays(t *testing.T) {
	pool := graphPool(t)
	s := New(pool)
	ctx := context.Background()

	for _, direction := range []string{"blocker acts", "blocked acts"} {
		t.Run(direction, func(t *testing.T) {
			alice, bob := pairFixture(t, pool)
			var circleA, circleB uuid.UUID
			for _, p := range []struct {
				owner uuid.UUID
				dst   *uuid.UUID
			}{{alice, &circleA}, {bob, &circleB}} {
				if err := pool.QueryRow(ctx,
					`INSERT INTO circles (owner_id, name) VALUES ($1, 'c') RETURNING id`,
					p.owner).Scan(p.dst); err != nil {
					t.Fatalf("create circle: %v", err)
				}
			}

			if _, err := s.BlockAtomic(ctx, alice, bob); err != nil {
				t.Fatalf("block: %v", err)
			}

			// actor is the one attempting to create the relationship.
			actor, other, circle := alice, bob, circleA
			if direction == "blocked acts" {
				actor, other, circle = bob, alice, circleB
			}

			paths := map[string]func() error{
				"follow":             func() error { _, err := s.FollowAtomic(ctx, actor, other); return err },
				"connection request": func() error { return s.SendConnectionRequestAtomic(ctx, actor, other, "profile", "") },
				"accept connection":  func() error { return s.AcceptConnectionRequestAtomic(ctx, actor, other) },
				"close friend":       func() error { return s.AddCloseFriendAtomic(ctx, actor, other, "manual") },
				"favorite":           func() error { return s.AddFavoriteAtomic(ctx, actor, other) },
				"relationship label": func() error { return s.UpsertRelationshipLabelAtomic(ctx, actor, other, "family") },
				"circle member":      func() error { return s.AddCircleMemberAtomic(ctx, circle, actor, other) },
			}
			for name, fn := range paths {
				if err := fn(); !errors.Is(err, ErrBlockedPair) {
					t.Errorf("%s (%s): got %v, want ErrBlockedPair", name, direction, err)
				}
			}
			assertNoRelationship(t, pool, alice, bob, "after refused create attempts")
		})
	}
}

// The block must sweep EVERY relationship table, with every one seeded first.
// This is the test the incomplete schema made vacuous.
func TestPairAtomic_BlockSweepsEverySeededRelationshipTable(t *testing.T) {
	pool := graphPool(t)
	s := New(pool)
	ctx := context.Background()

	alice, bob := pairFixture(t, pool)

	var aliceCircle, bobCircle uuid.UUID
	for _, p := range []struct {
		owner uuid.UUID
		dst   *uuid.UUID
	}{{alice, &aliceCircle}, {bob, &bobCircle}} {
		if err := pool.QueryRow(ctx,
			`INSERT INTO circles (owner_id, name) VALUES ($1, 'c') RETURNING id`,
			p.owner).Scan(p.dst); err != nil {
			t.Fatalf("create circle: %v", err)
		}
	}

	// Seed a row in EVERY table, in both directions where the table allows it.
	seed := []struct {
		what string
		sql  string
		args []any
	}{
		{"follow a→b", `INSERT INTO follows (follower_id, followee_id) VALUES ($1,$2)`, []any{alice, bob}},
		{"follow b→a", `INSERT INTO follows (follower_id, followee_id) VALUES ($1,$2)`, []any{bob, alice}},
		{"connection", `INSERT INTO connections (user_a, user_b) VALUES ($1,$2)`, normalizePairArgs(alice, bob)},
		{"request a→b", `INSERT INTO connection_requests (sender_id, receiver_id, status) VALUES ($1,$2,'pending')`, []any{alice, bob}},
		{"request b→a", `INSERT INTO connection_requests (sender_id, receiver_id, status) VALUES ($1,$2,'pending')`, []any{bob, alice}},
		{"follow request a→b", `INSERT INTO follow_requests (requester_id, target_id, status) VALUES ($1,$2,'pending')`, []any{alice, bob}},
		{"follow request b→a", `INSERT INTO follow_requests (requester_id, target_id, status) VALUES ($1,$2,'pending')`, []any{bob, alice}},
		{"close friend a→b", `INSERT INTO close_friends (user_id, friend_id) VALUES ($1,$2)`, []any{alice, bob}},
		{"close friend b→a", `INSERT INTO close_friends (user_id, friend_id) VALUES ($1,$2)`, []any{bob, alice}},
		{"favorite a→b", `INSERT INTO favorites (user_id, target_id) VALUES ($1,$2)`, []any{alice, bob}},
		{"favorite b→a", `INSERT INTO favorites (user_id, target_id) VALUES ($1,$2)`, []any{bob, alice}},
		{"label a→b", `INSERT INTO relationship_labels (user_id, target_id, label) VALUES ($1,$2,'family')`, []any{alice, bob}},
		{"label b→a", `INSERT INTO relationship_labels (user_id, target_id, label) VALUES ($1,$2,'colleague')`, []any{bob, alice}},
		{"circle member b in a's circle", `INSERT INTO circle_members (circle_id, user_id) VALUES ($1,$2)`, []any{aliceCircle, bob}},
		{"circle member a in b's circle", `INSERT INTO circle_members (circle_id, user_id) VALUES ($1,$2)`, []any{bobCircle, alice}},
	}
	for _, sd := range seed {
		if _, err := pool.Exec(ctx, sd.sql, sd.args...); err != nil {
			t.Fatalf("seed %s: %v", sd.what, err)
		}
	}

	// Confirm the seeding actually landed, or the sweep assertion is vacuous.
	for _, tbl := range allRelationshipTables {
		var n int
		if err := pool.QueryRow(ctx, tbl.countSQL, alice, bob).Scan(&n); err != nil {
			t.Fatalf("pre-count %s: %v", tbl.name, err)
		}
		if n == 0 {
			t.Fatalf("%s was not seeded: the sweep assertion would prove nothing", tbl.name)
		}
	}

	res, err := s.BlockAtomic(ctx, alice, bob)
	if err != nil {
		t.Fatalf("block: %v", err)
	}
	if !res.Created {
		t.Fatal("block reported no new row")
	}

	assertNoRelationship(t, pool, alice, bob, "after block")

	// The result must report what it removed; a silent sweep gives the caller
	// no basis for adjusting counters.
	if !res.RemovedFollowForward || !res.RemovedFollowReverse {
		t.Errorf("block did not report removing both follows: %+v", res)
	}
	if !res.RemovedConnection || res.RemovedRequest == 0 || res.RemovedCloseFriend == 0 ||
		res.RemovedFavorite == 0 || res.RemovedLabel == 0 || res.RemovedCircleMember == 0 {
		t.Errorf("block under-reported its sweep: %+v", res)
	}
}

func normalizePairArgs(a, b uuid.UUID) []any {
	lo, hi := normalizePair(a, b)
	return []any{lo, hi}
}

// Exactly one unpublished outbox row per block transition, written in the same
// transaction.
func TestPairAtomic_BlockWritesExactlyOneOutboxEventTransactionally(t *testing.T) {
	pool := graphPool(t)
	s := New(pool)
	ctx := context.Background()
	alice, bob := pairFixture(t, pool)

	if _, err := s.BlockAtomic(ctx, alice, bob); err != nil {
		t.Fatalf("block: %v", err)
	}
	// A repeat block is idempotent on the edge but still a transition attempt;
	// it must not produce a second *unpublished* user.blocked row for a state
	// that did not change.
	if _, err := s.BlockAtomic(ctx, alice, bob); err != nil {
		t.Fatalf("repeat block: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM graph_outbox_events
		WHERE event_type = $3 AND actor_id = $1 AND target_id = $2 AND published = FALSE`,
		alice, bob, events.UserBlocked).Scan(&n); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	// LB-2: this asserted `n >= 1` while its name claimed EXACTLY one, so it
	// would have passed if the idempotent repeat block had emitted a second
	// event for a state that did not change. A duplicate transition event is
	// not harmless — a consumer that has not implemented dedupe applies the
	// safety effect twice, and one that has must still process and discard it.
	if n != 1 {
		t.Fatalf("got %d unpublished %s events for one state transition, want exactly 1. "+
			"The second BlockAtomic changed nothing, so it must announce nothing.",
			n, events.UserBlocked)
	}
}

// LB-2: the event type must be the CANONICAL shared constant.
//
// This wrote the string "user.blocked" while every consumer switches on
// events.UserBlocked ("UserBlocked"). The relay published successfully and
// marked the row published — and every consumer ignored it. The durable path
// reported success at every step while delivering nothing.
func TestPairAtomic_OutboxUsesCanonicalEventNamesAndPayloads(t *testing.T) {
	pool := graphPool(t)
	s := New(pool)
	ctx := context.Background()
	alice, bob := pairFixture(t, pool)

	if _, err := s.FollowAtomic(ctx, alice, bob); err != nil {
		t.Fatalf("follow: %v", err)
	}
	if _, err := s.BlockAtomic(ctx, alice, bob); err != nil {
		t.Fatalf("block: %v", err)
	}
	if err := s.UnblockAtomic(ctx, alice, bob); err != nil {
		t.Fatalf("unblock: %v", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT event_type, payload FROM graph_outbox_events
		WHERE actor_id = $1 AND target_id = $2 ORDER BY occurred_at`, alice, bob)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	defer rows.Close()

	seen := map[string]json.RawMessage{}
	for rows.Next() {
		var typ string
		var payload json.RawMessage
		if err := rows.Scan(&typ, &payload); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen[typ] = payload
	}

	for _, want := range []string{events.UserFollowed, events.UserBlocked, events.UserUnblocked} {
		payload, ok := seen[want]
		if !ok {
			t.Errorf("no outbox row with the canonical event type %q. Consumers switch "+
				"on this constant; anything else is delivered and ignored. Got: %v",
				want, keysOf(seen))
			continue
		}
		// The payload must decode into the canonical shared type with its
		// identity fields populated — an empty struct means the field names
		// did not match and a consumer would read zero values.
		switch want {
		case events.UserBlocked:
			var p events.UserBlockedPayload
			if err := json.Unmarshal(payload, &p); err != nil {
				t.Errorf("%s payload does not decode: %v", want, err)
			} else if p.BlockerID != alice.String() || p.BlockedID != bob.String() {
				t.Errorf("%s payload fields do not match the shared type: %+v", want, p)
			}
		case events.UserUnblocked:
			var p events.UserUnblockedPayload
			if err := json.Unmarshal(payload, &p); err != nil {
				t.Errorf("%s payload does not decode: %v", want, err)
			} else if p.BlockerID != alice.String() || p.BlockedID != bob.String() {
				t.Errorf("%s payload fields do not match the shared type: %+v", want, p)
			}
		case events.UserFollowed:
			var p events.UserFollowedPayload
			if err := json.Unmarshal(payload, &p); err != nil {
				t.Errorf("%s payload does not decode: %v", want, err)
			} else if p.FollowerID != alice.String() || p.FolloweeID != bob.String() {
				t.Errorf("%s payload fields do not match the shared type: %+v", want, p)
			}
		}
	}

	// Guard against a lowercase regression sneaking back in alongside.
	for typ := range seen {
		if strings.HasPrefix(typ, "user.") {
			t.Errorf("outbox row with non-canonical event type %q: consumers switch on "+
				"the CamelCase shared constants and would ignore this", typ)
		}
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestPairAtomic_SelfBlockRejectedAndWritesNothing(t *testing.T) {
	pool := graphPool(t)
	s := New(pool)
	ctx := context.Background()
	alice, _ := pairFixture(t, pool)

	if _, err := s.BlockAtomic(ctx, alice, alice); err == nil {
		t.Fatal("self-block was accepted")
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM blocks WHERE blocker_id = $1 AND blocked_id = $1`, alice).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatal("self-block wrote a row")
	}
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM graph_outbox_events WHERE actor_id = $1 AND target_id = $1`, alice).Scan(&n); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if n != 0 {
		t.Fatal("self-block wrote an outbox event")
	}
}

// Both directions of a pair share one monotonic sequence, so a consumer can
// order block/unblock for the pair regardless of who acted.
func TestPairAtomic_PairSequenceIsMonotonicAcrossBothDirections(t *testing.T) {
	pool := graphPool(t)
	s := New(pool)
	ctx := context.Background()
	alice, bob := pairFixture(t, pool)

	var seqs []int64
	res, err := s.BlockAtomic(ctx, alice, bob)
	if err != nil {
		t.Fatalf("block a→b: %v", err)
	}
	seqs = append(seqs, res.PairSeq)

	if err := s.UnblockAtomic(ctx, alice, bob); err != nil {
		t.Fatalf("unblock: %v", err)
	}
	res, err = s.BlockAtomic(ctx, bob, alice)
	if err != nil {
		t.Fatalf("block b→a: %v", err)
	}
	seqs = append(seqs, res.PairSeq)

	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			t.Fatalf("pair sequence not monotonic across directions: %v", seqs)
		}
	}
}
