package service

// Correction-pass proofs (independent review P0-1, P0-2, P0-4, P0-5).
//
// Each test here is the load-bearing negative control for one corrected
// failure mode: reverting the corresponding fix makes the test fail.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// stubGraph serves the two graph-service endpoints governance consults:
// every permissions/check answers "allowed direct add", and blocked-any
// answers from the provided map (candidate id -> blocked counterpart ids).
func stubGraph(t *testing.T, blockedFor map[uuid.UUID][]uuid.UUID) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/permissions/check", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"decisions": map[string]any{
					"add_to_group": map[string]any{"allowed": true, "fallback": ""},
				},
			},
		})
	})
	mux.HandleFunc("/v1/internal/graph/blocked-any", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UserID       uuid.UUID   `json:"user_id"`
			CandidateIDs []uuid.UUID `json:"candidate_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		blockedSet := map[uuid.UUID]bool{}
		for _, b := range blockedFor[req.UserID] {
			blockedSet[b] = true
		}
		var out []uuid.UUID
		for _, c := range req.CandidateIDs {
			if blockedSet[c] {
				out = append(out, c)
			}
		}
		if out == nil {
			out = []uuid.UUID{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"blocked_user_ids": out})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newGovernanceServiceWithGraph(f *governanceFake, srv *httptest.Server) *Service {
	return &Service{
		convStore:       f,
		log:             slog.Default(),
		httpClient:      srv.Client(),
		graphServiceURL: srv.URL,
	}
}

// --- P0-1: unknown/stale policy fails closed on EXISTING sends ---

func TestSendFailsClosedOnUnknownSenderPolicy(t *testing.T) {
	f := newGovernanceFake()
	sender, other := uuid.New(), uuid.New()
	convID := uuid.New()
	f.addGroup(convID, sender, map[uuid.UUID]string{other: "member"})
	// No policy row for the sender and no identity URL: Known=false.
	svc := newGovernanceService(f)

	_, err := svc.SendMessage(context.Background(), sender, convID, "text", "hi", nil, nil, "p01-key-1")
	if !errors.Is(err, ErrMessagingNotAllowed) {
		t.Fatalf("unknown sender policy must deny the send, got %v", err)
	}
}

func TestSendFailsClosedOnStaleAndUnknownRecipientPolicy(t *testing.T) {
	f := newGovernanceFake()
	sender, recipient := uuid.New(), uuid.New()
	convID := uuid.New()
	f.conversations[convID] = &postgres.Conversation{ID: convID, Type: "direct"}
	f.roles[convID] = map[uuid.UUID]string{sender: "member", recipient: "member"}

	// The recipient is KNOWN and unpaused for the stale-sender probes, so
	// the only denial source under test is the sender's stale row — without
	// this, an unknown recipient masks a widened grace window and the
	// negative control cannot bite.
	f.freshPolicy(recipient, false)

	// Sender: a row past the stale grace is no longer authoritative. The
	// re-verification named the previous 24h bound the privacy hole, so the
	// boundary under test sits just past policyStaleGrace (15 min).
	f.policies[sender] = &postgres.UserPolicy{
		UserID: sender, ChatPaused: false, SendTypingIndicators: true,
		ReadReceiptsVisibility: "everyone",
		RefreshedAt:            time.Now().Add(-16 * time.Minute),
	}
	svc := newGovernanceService(f)
	_, err := svc.SendMessage(context.Background(), sender, convID, "text", "hi", nil, nil, "p01-key-2")
	if !errors.Is(err, ErrMessagingNotAllowed) {
		t.Fatalf("policy row older than the stale grace must deny the send, got %v", err)
	}
	f.policies[sender].RefreshedAt = time.Now().Add(-25 * time.Hour)
	_, err = svc.SendMessage(context.Background(), sender, convID, "text", "hi", nil, nil, "p01-key-2b")
	if !errors.Is(err, ErrMessagingNotAllowed) {
		t.Fatalf("an ancient policy row must deny the send, got %v", err)
	}

	// Sender fresh-but-stale (inside the grace) passes the sender gate;
	// with the recipient policy now UNKNOWN again, the recipient gate must
	// deny the direct send.
	delete(f.policies, recipient)
	f.policies[sender].RefreshedAt = time.Now().Add(-6 * time.Minute)
	_, err = svc.SendMessage(context.Background(), sender, convID, "text", "hi", nil, nil, "p01-key-3")
	if !errors.Is(err, ErrMessagingNotAllowed) {
		t.Fatalf("unknown recipient policy must deny the direct send, got %v", err)
	}

	// Control: both policies known and unpaused — the send must clear the
	// policy gates (it fails later on the absent message store, which is a
	// DIFFERENT error).
	f.freshPolicy(sender, false)
	f.freshPolicy(recipient, false)
	func() {
		defer func() { _ = recover() }() // absent downstream stores may panic; the gate is what's under test
		_, err = svc.SendMessage(context.Background(), sender, convID, "text", "hi", nil, nil, "p01-key-4")
		if errors.Is(err, ErrMessagingNotAllowed) {
			t.Fatal("known unpaused policies must not be denied by the policy gate")
		}
	}()
}

// --- P0-2: presence rosters are per-target gated, fail closed ---

func TestPresenceRosterFilterFailsClosed(t *testing.T) {
	requester, other := uuid.New(), uuid.New()
	// No graph URL, no redis: every disclosure check fails closed, so only
	// the requester survives the filter.
	svc := &Service{log: slog.Default()}
	got := svc.filterPresenceDisclosure(context.Background(), requester,
		[]string{requester.String(), other.String()})
	if len(got) != 1 || got[0] != requester.String() {
		t.Fatalf("fail-closed roster filter must keep only the requester, got %v", got)
	}
}

// --- P0-5: blocked pairs never co-added, checked against the ROSTER ---

func TestGroupAddSkipsCandidateBlockedWithExistingMember(t *testing.T) {
	f := newGovernanceFake()
	owner, member, candidate := uuid.New(), uuid.New(), uuid.New()
	convID := uuid.New()
	f.addGroup(convID, owner, map[uuid.UUID]string{member: "member"})

	// candidate and the EXISTING member block each other; the actor (owner)
	// has no block with the candidate.
	srv := stubGraph(t, map[uuid.UUID][]uuid.UUID{candidate: {member}})
	svc := newGovernanceServiceWithGraph(f, srv)

	out, err := svc.AddMemberWithPolicy(context.Background(), owner, convID, candidate)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if out.Outcome != "skipped" {
		t.Fatalf("candidate blocked with a roster member must be skipped, got %q", out.Outcome)
	}
	if out.Reason != "privacy" {
		t.Fatalf("skip reason must not disclose block state, got %q", out.Reason)
	}

	// Control: with no blocks the same add succeeds.
	srv2 := stubGraph(t, nil)
	svc2 := newGovernanceServiceWithGraph(f, srv2)
	out, err = svc2.AddMemberWithPolicy(context.Background(), owner, convID, candidate)
	if err != nil || out.Outcome != "added" {
		t.Fatalf("unblocked candidate must be added, got %v / %v", out, err)
	}
}

func TestInvitationAcceptRefusedWhenRosterGainedBlockedMember(t *testing.T) {
	f := newGovernanceFake()
	owner, invitee, later := uuid.New(), uuid.New(), uuid.New()
	convID := uuid.New()
	f.addGroup(convID, owner, map[uuid.UUID]string{later: "member"})
	inv, _, _ := f.CreateGroupInvitation(context.Background(), convID, owner, invitee)

	// `later` joined after the invitation and blocks the invitee.
	srv := stubGraph(t, map[uuid.UUID][]uuid.UUID{invitee: {later}})
	svc := newGovernanceServiceWithGraph(f, srv)
	if err := svc.AcceptGroupInvitation(context.Background(), invitee, inv.ID); err == nil {
		t.Fatal("accept must be refused while a blocked counterpart is in the roster")
	}
	if f.roles[convID][invitee] != "" {
		t.Fatal("refused accept must not have created membership")
	}

	// Control: without the block the accept succeeds.
	srv2 := stubGraph(t, nil)
	svc2 := newGovernanceServiceWithGraph(f, srv2)
	if err := svc2.AcceptGroupInvitation(context.Background(), invitee, inv.ID); err != nil {
		t.Fatalf("unblocked accept: %v", err)
	}
}

// --- P0-5: the removal ladder cannot sever the owner ---

func TestRemoveMemberNeverSeversOwner(t *testing.T) {
	f := newGovernanceFake()
	owner, admin := uuid.New(), uuid.New()
	convID := uuid.New()
	f.addGroup(convID, owner, map[uuid.UUID]string{admin: "admin"})
	svc := newGovernanceService(f)

	if err := svc.RemoveMemberGoverned(context.Background(), admin, convID, owner); !errors.Is(err, ErrNotPermittedRole) {
		t.Fatalf("severing the owner must be refused, got %v", err)
	}
	if f.roles[convID][owner] != "owner" {
		t.Fatal("owner row must be untouched after the refused removal")
	}
}

// --- Re-verification P0-4: removal success requires the persisted marker ---

func TestRemovalNotReportedSuccessWithoutRevocationMarker(t *testing.T) {
	f := newGovernanceFake()
	owner, member := uuid.New(), uuid.New()
	convID := uuid.New()
	f.addGroup(convID, owner, map[uuid.UUID]string{member: "member"})

	// Redis that cannot be reached: the marker write fails, so the removal
	// must surface an ERROR even though the sever committed — the client
	// retry re-arms via the not-a-member lane.
	deadRedis := redis.NewClient(&redis.Options{
		Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond, MaxRetries: -1,
	})
	t.Cleanup(func() { deadRedis.Close() })
	svc := newGovernanceService(f)
	svc.rdb = deadRedis

	err := svc.RemoveMemberGoverned(context.Background(), owner, convID, member)
	if err == nil || !strings.Contains(err.Error(), "revocation marker not persisted") {
		t.Fatalf("removal with a failed marker write must not report success, got %v", err)
	}
	// The retry (target now already severed) must STILL refuse to answer
	// until the marker lands.
	err = svc.RemoveMemberGoverned(context.Background(), owner, convID, member)
	if err == nil || !strings.Contains(err.Error(), "revocation marker not persisted") {
		t.Fatalf("retry with a still-failing marker write must keep failing, got %v", err)
	}
}
