package workers

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/membership"
	"github.com/atpost/monetization-service/internal/service"
	"github.com/atpost/monetization-service/internal/service/membershiptest"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// The membership workers against the in-memory store (same contract as the
// Postgres store's SweepStalePendingJoins, StartGraceForEndedPeriods,
// ExpireEndedMemberships and the reminder claims). None of them charges.

var _ membershipStore = (*membershiptest.Store)(nil)
var _ membershipStore = (*postgres.Store)(nil)
var _ membershipPublisher = (*membershiptest.Publisher)(nil)

var w0 = time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)

type workerRig struct {
	fake    *membershiptest.Store
	pub     *membershiptest.Publisher
	creator uuid.UUID
	tier    *postgres.CreatorTier
}

func newWorkerRig() *workerRig {
	r := &workerRig{fake: membershiptest.New(), pub: &membershiptest.Publisher{}, creator: uuid.New()}
	r.tier = r.fake.AddTier(r.creator, "Fan", 4_900)
	return r
}

func (r *workerRig) member(status string, periodEnd time.Time) *postgres.Subscription {
	s := r.fake.AddSubscription(postgres.Subscription{SubscriberID: uuid.New(), CreatorID: r.creator, TierID: r.tier.ID,
		TierName: "Fan", PricePaise: 4_900, Status: status, CurrentPeriodStart: periodEnd.AddDate(0, -1, 0), CurrentPeriodEnd: periodEnd})
	r.fake.SetCounted(s.ID)
	return s
}

func TestPeriodEndThenGraceThenExpired(t *testing.T) {
	ctx := context.Background()
	r := newWorkerRig()
	end := w0
	sub := r.member(membership.StatusActive, end)
	if r.fake.TierCount(r.tier.ID) != 1 {
		t.Fatal("setup: not counted")
	}

	// Before the end: nothing.
	runMembershipPass(ctx, r.fake, r.pub, end.Add(-time.Minute))
	if s := r.fake.Sub(sub.ID); s.Status != membership.StatusActive {
		t.Fatalf("before period end: %s", s.Status)
	}
	// Period end reached without a renewal payment: grace for 3 days, no charge.
	runMembershipPass(ctx, r.fake, r.pub, end.Add(time.Minute))
	s := r.fake.Sub(sub.ID)
	if s.Status != membership.StatusGrace || s.GracePeriodEnd == nil || !s.GracePeriodEnd.Equal(end.Add(72*time.Hour)) {
		t.Fatalf("after period end: %+v", s)
	}
	if len(r.fake.Payments) != 0 {
		t.Fatal("a worker opened a payment")
	}
	// Still in grace two days later; counted.
	runMembershipPass(ctx, r.fake, r.pub, end.Add(48*time.Hour))
	if s := r.fake.Sub(sub.ID); s.Status != membership.StatusGrace || r.fake.TierCount(r.tier.ID) != 1 {
		t.Fatalf("mid grace: %s count %d", s.Status, r.fake.TierCount(r.tier.ID))
	}
	// Grace over: expired, uncounted, ended published, entitlement revoked.
	runMembershipPass(ctx, r.fake, r.pub, end.Add(72*time.Hour+time.Minute))
	s = r.fake.Sub(sub.ID)
	if s.Status != membership.StatusExpired || s.Counted || r.fake.TierCount(r.tier.ID) != 0 {
		t.Fatalf("after grace: %+v count %d", s, r.fake.TierCount(r.tier.ID))
	}
	types := r.pub.MembershipTypes()
	if types[len(types)-1] != membership.EventEnded {
		t.Fatalf("membership events %v", types)
	}
	// A stable key: a retried publish of this expiry carries the same event id.
	if last := r.pub.Memberships[len(r.pub.Memberships)-1]; last.Key != service.EndedEventKey(sub.ID, end) || last.Key != service.EndedEventKey(sub.ID, end.UTC()) {
		t.Fatalf("membership.ended key %q", last.Key)
	}
	acts := r.pub.Actions()
	if !reflect.DeepEqual(acts, []string{"grace_started", membership.EntitlementRevoked}) {
		t.Fatalf("entitlement events %v", acts)
	}
	// Another pass changes nothing and publishes nothing more.
	n := len(r.pub.Memberships)
	runMembershipPass(ctx, r.fake, r.pub, end.Add(100*time.Hour))
	if len(r.pub.Memberships) != n || r.fake.TierCount(r.tier.ID) != 0 {
		t.Fatal("expired membership touched again")
	}
}

func TestCancelledAtPeriodEndExpiresWithoutGrace(t *testing.T) {
	ctx := context.Background()
	r := newWorkerRig()
	sub := r.member(membership.StatusCancelledAtPeriodEnd, w0)
	runMembershipPass(ctx, r.fake, r.pub, w0.Add(-time.Minute))
	if s := r.fake.Sub(sub.ID); s.Status != membership.StatusCancelledAtPeriodEnd {
		t.Fatalf("before end: %s", s.Status)
	}
	runMembershipPass(ctx, r.fake, r.pub, w0.Add(time.Minute))
	if s := r.fake.Sub(sub.ID); s.Status != membership.StatusExpired || r.fake.TierCount(r.tier.ID) != 0 {
		t.Fatalf("at end: %+v", s)
	}
	if got := r.pub.MembershipTypes(); !reflect.DeepEqual(got, []string{membership.EventEnded}) {
		t.Fatalf("events %v (no reminder for a cancelled membership)", got)
	}
}

func TestRemindersThreeDaysBeforeAndOnTheDayOncePerPeriod(t *testing.T) {
	ctx := context.Background()
	r := newWorkerRig()
	end := w0
	sub := r.member(membership.StatusActive, end)
	// Four days out: nothing.
	runMembershipPass(ctx, r.fake, r.pub, end.Add(-96*time.Hour))
	if len(r.pub.Memberships) != 0 {
		t.Fatalf("early reminder: %v", r.pub.MembershipTypes())
	}
	// Three days out, twice: one reminder.
	runMembershipPass(ctx, r.fake, r.pub, end.Add(-71*time.Hour))
	runMembershipPass(ctx, r.fake, r.pub, end.Add(-50*time.Hour))
	if len(r.pub.Memberships) != 1 {
		t.Fatalf("3-day reminders: %v", r.pub.MembershipTypes())
	}
	m := r.pub.Memberships[0]
	if m.Type != membership.EventRenewalDue || m.Payload.DaysLeft == nil || *m.Payload.DaysLeft != 3 ||
		m.Payload.SubscriptionID != sub.ID.String() || m.Payload.AmountPaise != 4_900 || m.Payload.PeriodEnd != end.Format(time.RFC3339) {
		t.Fatalf("3-day reminder = %+v", m)
	}
	// On the day, twice (and once more in grace): one more reminder.
	runMembershipPass(ctx, r.fake, r.pub, end.Add(-2*time.Hour))
	runMembershipPass(ctx, r.fake, r.pub, end.Add(-time.Hour))
	runMembershipPass(ctx, r.fake, r.pub, end.Add(time.Hour))
	var due []int
	var keys []string
	for _, e := range r.pub.Memberships {
		if e.Type == membership.EventRenewalDue {
			due = append(due, *e.Payload.DaysLeft)
			keys = append(keys, e.Key)
		}
	}
	if !reflect.DeepEqual(due, []int{3, 0}) || keys[0] == keys[1] {
		t.Fatalf("reminders = %v keys %v, want [3 0] with distinct keys", due, keys)
	}
}

// A reminder whose publish fails is not lost: the claim is released and the
// next pass sends it, with the SAME stable key (event id).
func TestReminderPublishFailureIsRetriedNotDropped(t *testing.T) {
	ctx := context.Background()
	r := newWorkerRig()
	r.member(membership.StatusActive, w0)
	r.pub.FailMembership = 1
	runMembershipPass(ctx, r.fake, r.pub, w0.Add(-70*time.Hour))
	if len(r.pub.Memberships) != 0 {
		t.Fatal("failed publish recorded")
	}
	runMembershipPass(ctx, r.fake, r.pub, w0.Add(-69*time.Hour))
	if len(r.pub.Memberships) != 1 || *r.pub.Memberships[0].Payload.DaysLeft != 3 {
		t.Fatalf("retry: %v", r.pub.MembershipTypes())
	}
}

func TestStalePendingJoinsAreSwept(t *testing.T) {
	ctx := context.Background()
	r := newWorkerRig()
	fresh := r.fake.AddSubscription(postgres.Subscription{SubscriberID: uuid.New(), CreatorID: r.creator, TierID: r.tier.ID,
		Status: membership.StatusPendingPayment, CreatedAt: w0.Add(-10 * time.Minute), CurrentPeriodEnd: w0.AddDate(0, 1, 0)})
	stale := r.fake.AddSubscription(postgres.Subscription{SubscriberID: uuid.New(), CreatorID: r.creator, TierID: r.tier.ID,
		Status: membership.StatusPendingPayment, CreatedAt: w0.Add(-31 * time.Minute), CurrentPeriodEnd: w0.AddDate(0, 1, 0)})
	runMembershipPass(ctx, r.fake, r.pub, w0)
	if r.fake.Sub(fresh.ID).Status != membership.StatusPendingPayment || r.fake.Sub(stale.ID).Status != membership.StatusExpired {
		t.Fatalf("fresh=%s stale=%s", r.fake.Sub(fresh.ID).Status, r.fake.Sub(stale.ID).Status)
	}
	if len(r.pub.Memberships) != 0 || len(r.pub.Events) != 0 || r.fake.TierCount(r.tier.ID) != 0 {
		t.Fatal("sweeping a never-paid join published or uncounted something")
	}
}

func TestPausedPastPeriodEndExpires(t *testing.T) {
	ctx := context.Background()
	r := newWorkerRig()
	sub := r.member(membership.StatusPaused, w0)
	runMembershipPass(ctx, r.fake, r.pub, w0.Add(time.Minute))
	if s := r.fake.Sub(sub.ID); s.Status != membership.StatusExpired || r.fake.TierCount(r.tier.ID) != 0 {
		t.Fatalf("paused past end: %+v", s)
	}
}

func TestAutoResumePublishesEntitlementChanged(t *testing.T) {
	ctx := context.Background()
	r := newWorkerRig()
	until := w0.Add(-time.Hour)
	sub := r.fake.AddSubscription(postgres.Subscription{SubscriberID: uuid.New(), CreatorID: r.creator, TierID: r.tier.ID,
		Status: "paused", PauseUntil: &until, CurrentPeriodEnd: w0.Add(24 * time.Hour)})
	resumePausedSubscriptions(ctx, r.fake, r.pub, w0)
	if r.fake.Sub(sub.ID).Status != "active" || !reflect.DeepEqual(r.pub.Actions(), []string{"resumed"}) {
		t.Fatalf("status %s actions %v", r.fake.Sub(sub.ID).Status, r.pub.Actions())
	}
}

// staleDue answers a due list read before another replica claimed it.
type staleDue struct {
	*membershiptest.Store
	due []postgres.ReminderDue
}

func (s staleDue) DueRenewalReminders(context.Context, time.Time) ([]postgres.ReminderDue, error) {
	return s.due, nil
}

// Two replicas read the same due reminder: the claim, not the read, decides
// who sends it. The loser sends nothing.
func TestReminderClaimDecidesWhoSends(t *testing.T) {
	ctx := context.Background()
	r := newWorkerRig()
	r.member(membership.StatusActive, w0)
	now := w0.Add(-70 * time.Hour)
	due, _ := r.fake.DueRenewalReminders(ctx, now)
	if len(due) != 1 {
		t.Fatalf("due %d", len(due))
	}
	// The other replica claimed and sent it.
	if ok, _ := r.fake.ClaimRenewalReminder(ctx, due[0].ID, due[0].CurrentPeriodEnd, due[0].DaysLeft); !ok {
		t.Fatal("claim")
	}
	sendRenewalReminders(ctx, staleDue{r.fake, due}, r.pub, now)
	if len(r.pub.Memberships) != 0 {
		t.Fatalf("sent twice: %v", r.pub.MembershipTypes())
	}
}
