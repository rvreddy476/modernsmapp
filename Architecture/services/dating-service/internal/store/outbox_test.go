package store

import (
	"context"
	"encoding/json"
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/atpost/dating-service/database"
	"github.com/atpost/shared/events"
	"github.com/atpost/shared/outbox"
	"github.com/google/uuid"
)

// The outbox table the closures write to is the one a numbered migration
// creates, with every column the leased relay needs (audit H2).
func TestOutboxMigrationCreatesTheRelayTable(t *testing.T) {
	var all strings.Builder
	err := fs.WalkDir(database.Migrations, "migrations", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(database.Migrations, path)
		all.Write(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	sql := all.String()
	if !strings.Contains(sql, "CREATE TABLE IF NOT EXISTS "+OutboxTable+" (") {
		t.Fatalf("no migration creates %s", OutboxTable)
	}
	if !strings.Contains(sql, "CREATE SCHEMA IF NOT EXISTS "+OutboxSchema) {
		t.Fatalf("no migration creates schema %s", OutboxSchema)
	}
	for _, col := range append([]string{"event_type", "partition_key", "payload", "published_at"}, outbox.ClaimColumns...) {
		if !regexp.MustCompile(`(?m)^\s+` + col + `\s`).MatchString(sql) {
			t.Errorf("outbox table lacks column %s", col)
		}
	}
}

// What the outbox stores is the envelope chat-service's dating consumer
// decodes: event_type at the top, the payload's ids inside.
func TestEncodeOutboxEnvelopeMatchesChatConsumerShape(t *testing.T) {
	mid, a, b := uuid.New(), uuid.New(), uuid.New()
	raw, err := EncodeOutboxEnvelope(context.Background(), events.EventDatingMatchClosed, &a, MatchClosedEvent{
		MatchID: mid.String(), ClosedBy: a.String(), UserA: a.String(), UserB: b.String(), Reason: CloseReasonSuspended,
	})
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		EventID     string          `json:"event_id"`
		EventType   string          `json:"event_type"`
		ActorUserID *string         `json:"actor_user_id"`
		Payload     json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if env.EventType != "dating.match.closed" || env.EventID == "" {
		t.Fatalf("envelope = %+v", env)
	}
	var p struct {
		MatchID string `json:"match_id"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.MatchID != mid.String() || p.Reason != CloseReasonSuspended {
		t.Fatalf("payload = %+v", p)
	}

	raw, err = EncodeOutboxEnvelope(context.Background(), events.EventDatingUserBlocked, &a, UserBlockedEvent{BlockerID: a.String(), BlockedID: b.String()})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	var bp struct {
		BlockerID string `json:"blocker_id"`
		BlockedID string `json:"blocked_id"`
	}
	if err := json.Unmarshal(env.Payload, &bp); err != nil {
		t.Fatal(err)
	}
	if env.EventType != "dating.user.blocked" || bp.BlockerID != a.String() || bp.BlockedID != b.String() {
		t.Fatalf("user.blocked envelope = %s", raw)
	}
}

func TestPairPartitionKeyIgnoresOrder(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	if PairPartitionKey(a, b) != PairPartitionKey(b, a) {
		t.Fatal("a pair's events must share one partition key whoever blocks")
	}
}

func TestChatClosingStatusesAndRiskLevels(t *testing.T) {
	for status, want := range map[string]string{
		ProfileStatusSuspended:     CloseReasonSuspended,
		ProfileStatusDeleted:       CloseReasonDeleted,
		ProfileStatusActive:        "",
		ProfileStatusPaused:        "",
		ProfileStatusRestricted:    CloseReasonRestricted, // includes under 18
		ProfileStatusPendingReview: "",
	} {
		if got := chatClosureReasonForStatus(status); got != want {
			t.Errorf("status %s closes chats with %q, want %q", status, got, want)
		}
	}
	for level, want := range map[string]bool{
		RiskLevelAllow: false, RiskLevelReduceReach: false, RiskLevelRequireRecheck: false,
		RiskLevelHideFromDiscovery: false, RiskLevelChatHold: true, RiskLevelAdminReview: true, RiskLevelSuspend: true,
	} {
		if got := RiskLevelBlocksChat(level); got != want {
			t.Errorf("risk level %s blocks chat = %v, want %v", level, got, want)
		}
	}
}

func TestDecideMatchChat(t *testing.T) {
	open := matchChatFacts{Status: "conversing", ProfileStatus: [2]string{"active", "paused"}, RiskLevel: [2]string{"", "allow"}}
	cases := []struct {
		name   string
		mutate func(*matchChatFacts)
		open   bool
		reason string
	}{
		{"open match, both fine", func(*matchChatFacts) {}, true, ""},
		{"quiet match", func(f *matchChatFacts) { f.Status = "quiet" }, true, ""},
		{"closed match", func(f *matchChatFacts) { f.Status = "closed" }, false, ChatClosedReasonMatch},
		{"expired match", func(f *matchChatFacts) { f.Status = "expired" }, false, ChatClosedReasonMatch},
		{"pending saga", func(f *matchChatFacts) { f.Status = "pending" }, false, ChatClosedReasonMatch},
		{"blocked pair", func(f *matchChatFacts) { f.Blocked = true }, false, ChatClosedReasonBlocked},
		{"a suspended", func(f *matchChatFacts) { f.ProfileStatus[0] = ProfileStatusSuspended }, false, ChatClosedReasonSuspended},
		{"b suspended", func(f *matchChatFacts) { f.ProfileStatus[1] = ProfileStatusSuspended }, false, ChatClosedReasonSuspended},
		{"a restricted (under 18)", func(f *matchChatFacts) { f.ProfileStatus[0] = ProfileStatusRestricted }, false, ChatClosedReasonRestricted},
		{"b restricted (under 18)", func(f *matchChatFacts) { f.ProfileStatus[1] = ProfileStatusRestricted }, false, ChatClosedReasonRestricted},
		{"a pending review (hold)", func(f *matchChatFacts) { f.ProfileStatus[0] = ProfileStatusPendingReview }, false, ChatClosedReasonPendingReview},
		{"b pending review (hold)", func(f *matchChatFacts) { f.ProfileStatus[1] = ProfileStatusPendingReview }, false, ChatClosedReasonPendingReview},
		{"a pending review, b suspended: the closure wins", func(f *matchChatFacts) {
			f.ProfileStatus[0] = ProfileStatusPendingReview
			f.ProfileStatus[1] = ProfileStatusSuspended
		}, false, ChatClosedReasonSuspended},
		{"a pending review, pair blocked: the block wins", func(f *matchChatFacts) {
			f.ProfileStatus[0] = ProfileStatusPendingReview
			f.Blocked = true
		}, false, ChatClosedReasonBlocked},
		{"b deleted status", func(f *matchChatFacts) { f.ProfileStatus[1] = ProfileStatusDeleted }, false, ChatClosedReasonDeleted},
		{"a soft-deleted", func(f *matchChatFacts) { f.Deleted[0] = true }, false, ChatClosedReasonDeleted},
		{"a has no profile", func(f *matchChatFacts) { f.ProfileStatus[0] = "" }, false, ChatClosedReasonDeleted},
		{"a on chat_hold", func(f *matchChatFacts) { f.RiskLevel[0] = RiskLevelChatHold }, false, ChatClosedReasonRiskHold},
		{"b in admin review", func(f *matchChatFacts) { f.RiskLevel[1] = RiskLevelAdminReview }, false, ChatClosedReasonRiskHold},
		{"b risk suspend", func(f *matchChatFacts) { f.RiskLevel[1] = RiskLevelSuspend }, false, ChatClosedReasonRiskHold},
		{"b hidden from discovery only", func(f *matchChatFacts) { f.RiskLevel[1] = RiskLevelHideFromDiscovery }, true, ""},
	}
	for _, tc := range cases {
		f := open
		tc.mutate(&f)
		gotOpen, gotReason := decideMatchChat(f)
		if gotOpen != tc.open || gotReason != tc.reason {
			t.Errorf("%s: open=%v reason=%q, want open=%v reason=%q", tc.name, gotOpen, gotReason, tc.open, tc.reason)
		}
	}
}
