package service

// Final-verification P0-4 proofs: every membership sever must ride the
// revocation protocol, and no retry or internal consumer may acknowledge a
// removal without a durable marker. Each test targets one bypass path the
// final verification named (managed removal, request block/report, the
// graph-block consumer, the already-gone leave retry, and the nil-Redis
// misconfiguration).

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// deadRedis is a client whose every command fails — the transient-Redis
// failure the bypass paths previously swallowed.
func deadRedis(t *testing.T) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{
		Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond, MaxRetries: -1,
	})
	t.Cleanup(func() { c.Close() })
	return c
}

func expectMarkerFailure(t *testing.T, err error) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "revocation marker not persisted") {
		t.Fatalf("a failed marker write must refuse the acknowledgement, got %v", err)
	}
}

func TestManagedRemovalRefusesSuccessWithoutMarker(t *testing.T) {
	f := newGovernanceFake()
	owner, member := uuid.New(), uuid.New()
	convID := uuid.New()
	f.addGroup(convID, owner, map[uuid.UUID]string{member: "member"})

	svc := newGovernanceService(f)
	svc.rdb = deadRedis(t)
	svc.entitlementSecret = "configured"

	expectMarkerFailure(t, svc.ManagedRemoveGroupMember(context.Background(), convID, member))
	// The retry (row already severed) must also refuse until the marker lands.
	expectMarkerFailure(t, svc.ManagedRemoveGroupMember(context.Background(), convID, member))
	if f.systemSevers < 2 {
		t.Fatal("managed removal must route through the system sever protocol")
	}
}

func TestBlockAndReportRefuseSuccessWithoutMarkerAndKeepStatusPending(t *testing.T) {
	for _, action := range []string{"block", "report"} {
		f := newGovernanceFake()
		sender, receiver := uuid.New(), uuid.New()
		convID := uuid.New()
		f.conversations[convID] = &postgres.Conversation{ID: convID, Type: "direct"}
		f.roles[convID] = map[uuid.UUID]string{sender: "member", receiver: "member"}
		f.latestRequest = &postgres.MessageRequest{
			ConversationID: convID, SenderID: sender, ReceiverID: receiver, Status: "pending",
		}

		svc := newGovernanceService(f)
		svc.rdb = deadRedis(t)
		svc.entitlementSecret = "configured"

		var err error
		if action == "block" {
			err = svc.BlockRequest(context.Background(), receiver, convID)
		} else {
			err = svc.ReportRequest(context.Background(), receiver, convID)
		}
		expectMarkerFailure(t, err)
		// The status must NOT have flipped: a flipped status made the
		// idempotent early-return acknowledge a block whose memberships
		// still carried live tokens (final-verification bypass).
		if f.latestRequest.Status != "pending" {
			t.Fatalf("%s: status flipped to %q before revocation was durable", action, f.latestRequest.Status)
		}
	}
}

func TestGraphBlockSeverRefusesAckWithoutMarker(t *testing.T) {
	f := newGovernanceFake()
	blocker, blocked := uuid.New(), uuid.New()
	convID := uuid.New()
	f.directConvID = convID
	f.conversations[convID] = &postgres.Conversation{ID: convID, Type: "direct"}
	f.roles[convID] = map[uuid.UUID]string{blocker: "member", blocked: "member"}

	svc := newGovernanceService(f)
	svc.rdb = deadRedis(t)
	svc.entitlementSecret = "configured"

	_, err := svc.SeverDirectConversationOnBlock(context.Background(), blocker, blocked)
	expectMarkerFailure(t, err)
	// Redelivery lane: the rows are already severed; the retry must STILL
	// refuse the ack until the marker lands.
	_, err = svc.SeverDirectConversationOnBlock(context.Background(), blocker, blocked)
	expectMarkerFailure(t, err)
}

func TestGraphBlockSeverAcksOnceRevocationIsDurable(t *testing.T) {
	f := newGovernanceFake()
	blocker, blocked := uuid.New(), uuid.New()
	convID := uuid.New()
	f.directConvID = convID
	f.conversations[convID] = &postgres.Conversation{ID: convID, Type: "direct"}
	f.roles[convID] = map[uuid.UUID]string{blocker: "member", blocked: "member"}

	// Entitlements disabled (no secret, no redis): arming is legally skipped
	// and the sever acknowledges.
	svc := newGovernanceService(f)
	severed, err := svc.SeverDirectConversationOnBlock(context.Background(), blocker, blocked)
	if err != nil || !severed {
		t.Fatalf("sever with entitlements disabled: severed=%v err=%v", severed, err)
	}
	if len(f.roles[convID]) != 0 {
		t.Fatal("both memberships must be severed")
	}
	// Redelivery is a clean non-ack-worthy no-op.
	severed, err = svc.SeverDirectConversationOnBlock(context.Background(), blocker, blocked)
	if err != nil || severed {
		t.Fatalf("redelivery must be a no-op success, severed=%v err=%v", severed, err)
	}
}

func TestLeaveRetryRefusesSuccessWithoutGeneration(t *testing.T) {
	f := newGovernanceFake()
	owner := uuid.New()
	convID := uuid.New()
	f.addGroup(convID, owner, nil)
	// The caller is NOT a member (the already-gone retry lane), and the
	// generation source is down: the final verification showed this lane
	// returning success with no marker.
	f.genUnavailable = true
	svc := newGovernanceService(f)
	svc.rdb = deadRedis(t)
	svc.entitlementSecret = "configured"

	err := svc.LeaveConversation(context.Background(), uuid.New(), convID)
	if err == nil || !strings.Contains(err.Error(), "revocation generation unavailable") {
		t.Fatalf("already-gone retry without a generation must refuse success, got %v", err)
	}
}

func TestArmRevocationFailsLoudlyOnMisconfiguration(t *testing.T) {
	f := newGovernanceFake()
	owner, member := uuid.New(), uuid.New()
	convID := uuid.New()
	f.addGroup(convID, owner, map[uuid.UUID]string{member: "member"})

	// Secret configured but NO redis client: a deploy fault, not a licence
	// to sever without revocation.
	svc := newGovernanceService(f)
	svc.entitlementSecret = "configured"
	expectMarkerFailure(t, svc.ManagedRemoveGroupMember(context.Background(), convID, member))
}

// --- Blocker-2 final correction proofs ---

// The managed add must go through the conversation-locked membership writer:
// the legacy AddMember took no lock, so a managed add could race a removal
// outside the generation serialization.
func TestManagedAddRoutesThroughLockedWriter(t *testing.T) {
	f := newGovernanceFake()
	owner := uuid.New()
	convID := uuid.New()
	f.addGroup(convID, owner, nil)
	svc := newGovernanceService(f)

	if err := svc.ManagedAddGroupMember(context.Background(), convID, uuid.New()); err != nil {
		t.Fatalf("managed add: %v", err)
	}
	if f.cappedAdds != 1 {
		t.Fatalf("managed add must use the conversation-locked AddMemberCapped writer, cappedAdds=%d", f.cappedAdds)
	}
}

// A MIXED roster — one member active, the other already severed (its earlier
// marker write failed) — must settle revocation for BOTH pair members. The
// pre-fix logic armed only the freshly severed row and acknowledged.
func TestMixedRosterBlockSettlesBothUsers(t *testing.T) {
	f := newGovernanceFake()
	blocker, blocked := uuid.New(), uuid.New()
	convID := uuid.New()
	f.directConvID = convID
	f.conversations[convID] = &postgres.Conversation{ID: convID, Type: "direct"}
	// Only the blocker is still active; the blocked user was severed earlier.
	f.roles[convID] = map[uuid.UUID]string{blocker: "member"}

	svc := newGovernanceService(f)
	svc.rdb = deadRedis(t)
	svc.entitlementSecret = "configured"

	_, err := svc.SeverDirectConversationOnBlock(context.Background(), blocker, blocked)
	expectMarkerFailure(t, err)
	// BOTH pair members must now hold a durable intent — the blocker's rode
	// the sever, the already-severed peer's was upserted defensively — so
	// the repair worker owns both markers even though the ack was refused.
	for _, userID := range []uuid.UUID{blocker, blocked} {
		if _, ok := f.intents[intentKey(convID, userID)]; !ok {
			t.Fatalf("no durable revocation intent for %v after mixed-roster block", userID)
		}
	}
}

// A committed sever whose arm fails must leave a DURABLE intent, and the
// repair worker must drain it once arming can succeed — success never
// depends on a client retry.
func TestWorkerDrainsDurableIntentWithoutClientRetry(t *testing.T) {
	f := newGovernanceFake()
	owner, member := uuid.New(), uuid.New()
	convID := uuid.New()
	f.addGroup(convID, owner, map[uuid.UUID]string{member: "member"})

	svc := newGovernanceService(f)
	svc.rdb = deadRedis(t)
	svc.entitlementSecret = "configured"

	expectMarkerFailure(t, svc.ManagedRemoveGroupMember(context.Background(), convID, member))
	key := intentKey(convID, member)
	if _, ok := f.intents[key]; !ok {
		t.Fatal("a committed sever with a failed arm must leave a durable revocation intent")
	}

	// While Redis stays down the worker keeps the intent.
	svc.repairPendingRevocations(context.Background())
	if _, ok := f.intents[key]; !ok {
		t.Fatal("the worker must not drop an intent it could not arm")
	}

	// Once arming can succeed (entitlements-off skip mode stands in for a
	// recovered Redis), one sweep settles and clears the intent — no client
	// action involved.
	svc.rdb = nil
	svc.entitlementSecret = ""
	svc.repairPendingRevocations(context.Background())
	if _, ok := f.intents[key]; ok {
		t.Fatal("the worker must clear the intent once the marker is settled")
	}
}
