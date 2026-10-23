package events

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/notification-service/internal/store/scylla"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

var (
	msSub     = uuid.MustParse("00000000-0000-4000-8000-0000000ab001")
	msMember  = uuid.MustParse("00000000-0000-4000-8000-0000000ab002")
	msCreator = uuid.MustParse("00000000-0000-4000-8000-0000000ab003")
	msTier    = uuid.MustParse("00000000-0000-4000-8000-0000000ab004")
	msAt      = time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)
)

// msPayload is the contract's common payload, as monetization-service writes it.
func msPayload(extra map[string]any) map[string]any {
	p := map[string]any{
		"subscription_id": msSub.String(), "subscriber_id": msMember.String(), "creator_id": msCreator.String(),
		"tier_id": msTier.String(), "tier_name": "Gold", "amount_paise": 9900, "currency": "INR",
		"period_end": "2026-11-23T09:30:00Z",
	}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

// msEnvelope builds the shared envelope exactly as the monetization producer
// does (events.NewEnvelope + json.Marshal), with a fixed id and time.
func msEnvelope(t *testing.T, eventType, eventID string, payload any) events.EventEnvelope {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	env := events.NewEnvelope(context.Background(), eventType, nil, raw)
	env.EventID, env.OccurredAt = eventID, msAt
	return env
}

func msMessage(t *testing.T, env events.EventEnvelope, offset int64) kafka.Message {
	t.Helper()
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return kafka.Message{Topic: MonetizationTopic, Key: []byte(env.EventID), Value: b, Offset: offset}
}

type msWant struct {
	typ              string
	recipient, actor uuid.UUID
	link             string
}

func TestPlanMembershipNoticesRecipientsAndLinks(t *testing.T) {
	memberLink := "/memberships/" + msSub.String()
	cases := []struct {
		event string
		want  []msWant
	}{
		{EventMembershipStarted, []msWant{
			{service.MembershipTypeNewMember, msCreator, msMember, "/monetization/tiers"},
			{service.MembershipTypeJoined, msMember, msCreator, memberLink},
		}},
		{EventMembershipRenewed, []msWant{{service.MembershipTypeRenewed, msMember, msCreator, memberLink}}},
		{EventMembershipRenewalDue, []msWant{{service.MembershipTypeRenewalDue, msMember, msCreator, memberLink}}},
		{EventMembershipPaymentFailed, []msWant{{service.MembershipTypePaymentFailed, msMember, msCreator, memberLink}}},
		{EventMembershipEnded, []msWant{{service.MembershipTypeEnded, msMember, msCreator, memberLink}}},
	}
	for _, c := range cases {
		got, err := planMembershipNotices(msEnvelope(t, c.event, "evt-1", msPayload(nil)), "evt-1", time.Now())
		if err != nil {
			t.Fatalf("%s: %v", c.event, err)
		}
		if len(got) != len(c.want) {
			t.Fatalf("%s: %d notices, want %d: %+v", c.event, len(got), len(c.want), got)
		}
		for i, w := range c.want {
			n := got[i]
			if n.Type != w.typ || n.RecipientID != w.recipient || n.ActorID != w.actor || n.DeepLink != w.link ||
				n.SubscriptionID != msSub || n.TierName != "Gold" || n.EventID != "evt-1" || !n.CreatedAt.Equal(msAt) ||
				!n.PeriodEnd.Equal(time.Date(2026, 11, 23, 9, 30, 0, 0, time.UTC)) || n.DaysLeft != service.MembershipDaysLeftUnknown {
				t.Errorf("%s notice %d = %+v", c.event, i, n)
			}
		}
	}
}

func TestPlanMembershipRenewalDueCarriesDaysLeft(t *testing.T) {
	for _, days := range []int{3, 0} {
		got, err := planMembershipNotices(msEnvelope(t, EventMembershipRenewalDue, "evt-r", msPayload(map[string]any{"days_left": days})), "evt-r", time.Now())
		if err != nil || len(got) != 1 {
			t.Fatalf("days %d: %v %+v", days, err, got)
		}
		if got[0].DaysLeft != days {
			t.Errorf("days_left %d planned as %d", days, got[0].DaysLeft)
		}
		got[0].ActorName = "Blue Hour Films"
		want := map[int]string{
			3: "Your Blue Hour Films membership ends in 3 days — renew to keep access",
			0: "Your Blue Hour Films membership ends today — renew to keep access",
		}[days]
		if r := service.RenderMembershipNotification(got[0]); r.Title != want {
			t.Errorf("days %d title %q", days, r.Title)
		}
	}
}

func TestPlanMembershipIgnoresOtherTopicEvents(t *testing.T) {
	for _, typ := range []string{"entitlement.changed", "subscription.created", "subscription.renewed",
		"creator_ledger.credited", "wallet.credited", "membership.something_new", ""} {
		got, err := planMembershipNotices(msEnvelope(t, typ, "evt-x", msPayload(map[string]any{"action": "granted"})), "evt-x", time.Now())
		if err != nil || got != nil {
			t.Errorf("%q: %+v %v, want ignored", typ, got, err)
		}
	}
}

func TestPlanMembershipRejectsUndeliverablePayloads(t *testing.T) {
	bad := map[string]events.EventEnvelope{
		"not an object":   {EventType: EventMembershipStarted, EventID: "e", Payload: json.RawMessage(`"oops"`)},
		"no payload":      {EventType: EventMembershipEnded, EventID: "e"},
		"wrong type":      {EventType: EventMembershipEnded, EventID: "e", Payload: json.RawMessage(`{"subscription_id": 7}`)},
		"no subscription": msEnvelope(t, EventMembershipEnded, "e", msPayload(map[string]any{"subscription_id": ""})),
		"bad subscriber":  msEnvelope(t, EventMembershipStarted, "e", msPayload(map[string]any{"subscriber_id": "nope"})),
		"nil creator":     msEnvelope(t, EventMembershipRenewed, "e", msPayload(map[string]any{"creator_id": uuid.Nil.String()})),
		"self":            msEnvelope(t, EventMembershipStarted, "e", msPayload(map[string]any{"creator_id": msMember.String()})),
	}
	for name, env := range bad {
		got, err := planMembershipNotices(env, "e", time.Now())
		if !errors.Is(err, errMembershipPermanent) || got != nil {
			t.Errorf("%s: %+v %v, want permanent", name, got, err)
		}
	}
	// An odd period_end or days_left is not fatal.
	got, err := planMembershipNotices(msEnvelope(t, EventMembershipRenewalDue, "e", msPayload(map[string]any{"period_end": "soon", "days_left": -4})), "e", time.Now())
	if err != nil || len(got) != 1 || !got[0].PeriodEnd.IsZero() || got[0].DaysLeft != service.MembershipDaysLeftUnknown {
		t.Fatalf("lenient fields: %+v %v", got, err)
	}
}

func TestMembershipDedupIDFallbacks(t *testing.T) {
	m := kafka.Message{Key: []byte("key-1"), Value: []byte(`{"x":1}`)}
	if got := membershipDedupID(events.EventEnvelope{EventID: "evt-9"}, m); got != "evt-9" {
		t.Errorf("event id: %q", got)
	}
	if got := membershipDedupID(events.EventEnvelope{}, m); got != "key-1" {
		t.Errorf("key fallback: %q", got)
	}
	m.Key = nil
	a := membershipDedupID(events.EventEnvelope{}, m)
	if a == "" || a != membershipDedupID(events.EventEnvelope{}, m) {
		t.Errorf("hash fallback not stable: %q", a)
	}
}

type fakeMembershipNotifier struct {
	mu      sync.Mutex
	got     []service.MembershipNotification
	failFor int // fail this many calls first
}

func (f *fakeMembershipNotifier) CreateMembershipNotification(_ context.Context, n service.MembershipNotification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failFor > 0 {
		f.failFor--
		return errors.New("scylla unavailable")
	}
	f.got = append(f.got, n)
	return nil
}

// fakeMembershipReader serves msgs, then blocks until ctx ends; it records commits.
type fakeMembershipReader struct {
	mu        sync.Mutex
	msgs      []kafka.Message
	committed []int64
	drained   chan struct{}
}

func (r *fakeMembershipReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	r.mu.Lock()
	if len(r.msgs) > 0 {
		m := r.msgs[0]
		r.msgs = r.msgs[1:]
		r.mu.Unlock()
		return m, nil
	}
	r.mu.Unlock()
	select {
	case r.drained <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return kafka.Message{}, ctx.Err()
}

func (r *fakeMembershipReader) CommitMessages(_ context.Context, msgs ...kafka.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range msgs {
		r.committed = append(r.committed, m.Offset)
	}
	return nil
}

func (r *fakeMembershipReader) Close() error { return nil }

func runMembershipConsumer(t *testing.T, msgs []kafka.Message, notify *fakeMembershipNotifier) *fakeMembershipReader {
	t.Helper()
	r := &fakeMembershipReader{msgs: msgs, drained: make(chan struct{}, 1)}
	c := &MembershipConsumer{reader: r, notify: notify}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Start(ctx); close(done) }()
	select {
	case <-r.drained:
	case <-time.After(10 * time.Second):
		t.Fatal("consumer did not drain the partition")
	}
	cancel()
	<-done
	return r
}

// Garbage, a malformed membership payload and an unknown type are committed
// and skipped; the valid events behind them are delivered; nothing stalls.
func TestMembershipConsumerMalformedDoesNotStallThePartition(t *testing.T) {
	malformed := msEnvelope(t, EventMembershipStarted, "evt-bad", nil)
	malformed.Payload = json.RawMessage(`{"subscription_id": ["x"]}`)
	msgs := []kafka.Message{
		{Offset: 0, Value: []byte("not json at all")},
		msMessage(t, malformed, 1),
		msMessage(t, msEnvelope(t, "entitlement.changed", "evt-ent", msPayload(map[string]any{"action": "granted"})), 2),
		msMessage(t, msEnvelope(t, EventMembershipStarted, "evt-start", msPayload(nil)), 3),
		msMessage(t, msEnvelope(t, EventMembershipEnded, "evt-end", msPayload(nil)), 4),
	}
	notify := &fakeMembershipNotifier{}
	r := runMembershipConsumer(t, msgs, notify)
	if want := []int64{0, 1, 2, 3, 4}; len(r.committed) != len(want) {
		t.Fatalf("committed %v, want %v", r.committed, want)
	}
	types := []string{}
	for _, n := range notify.got {
		types = append(types, n.Type)
	}
	if len(types) != 3 || types[0] != service.MembershipTypeNewMember || types[1] != service.MembershipTypeJoined || types[2] != service.MembershipTypeEnded {
		t.Fatalf("delivered %v", types)
	}
}

// A redelivered event plans the same notices with the same identities, so the
// conditional inbox insert (same deterministic notification id and ts)
// makes the replay a no-op.
func TestMembershipConsumerReplayKeepsTheIdentity(t *testing.T) {
	env := msEnvelope(t, EventMembershipStarted, "evt-start", msPayload(nil))
	notify := &fakeMembershipNotifier{}
	runMembershipConsumer(t, []kafka.Message{msMessage(t, env, 0), msMessage(t, env, 1)}, notify)
	if len(notify.got) != 4 {
		t.Fatalf("delivered %d notices", len(notify.got))
	}
	for i := 0; i < 2; i++ {
		a, b := notify.got[i], notify.got[i+2]
		if a.Identity() != b.Identity() ||
			scylla.DeterministicNotificationID(a.Identity()) != scylla.DeterministicNotificationID(b.Identity()) ||
			scylla.DeterministicTS(a.CreatedAt, a.Identity()) != scylla.DeterministicTS(b.CreatedAt, b.Identity()) {
			t.Fatalf("replay changed the identity: %q vs %q", a.Identity(), b.Identity())
		}
	}
	if notify.got[0].Identity() == notify.got[1].Identity() {
		t.Fatal("creator and member notices share an identity")
	}
}

// A delivery error is retried before the offset commits.
func TestMembershipConsumerRetriesDeliveryErrors(t *testing.T) {
	notify := &fakeMembershipNotifier{failFor: 1}
	r := runMembershipConsumer(t, []kafka.Message{
		msMessage(t, msEnvelope(t, EventMembershipPaymentFailed, "evt-pf", msPayload(nil)), 7),
	}, notify)
	if len(notify.got) != 1 || notify.got[0].Type != service.MembershipTypePaymentFailed {
		t.Fatalf("delivered %+v", notify.got)
	}
	if len(r.committed) != 1 || r.committed[0] != 7 {
		t.Fatalf("committed %v", r.committed)
	}
}
