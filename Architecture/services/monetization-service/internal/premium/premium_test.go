package premium

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

var t0 = time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestPricesFromEnv(t *testing.T) {
	p, err := PricesFromEnv(env(nil))
	if err != nil || len(p) != 0 {
		t.Fatalf("unset prices: %v %v", p, err)
	}
	if _, err := p.Price(Plan30d); err != ErrPriceNotSet {
		t.Fatalf("unset price must fail closed, got %v", err)
	}
	p, err = PricesFromEnv(env(map[string]string{EnvPrice30d: " 14900 "}))
	if err != nil {
		t.Fatal(err)
	}
	if n, err := p.Price(Plan30d); err != nil || n != 14900 {
		t.Fatalf("30d = %d %v", n, err)
	}
	if _, err := p.Price(Plan365d); err != ErrPriceNotSet {
		t.Fatalf("365d unset must fail closed, got %v", err)
	}
	for _, bad := range []string{"149.00", "abc", "0", "-100", "99", "10000001"} {
		if _, err := PricesFromEnv(env(map[string]string{EnvPrice365d: bad})); err == nil {
			t.Fatalf("price %q must be refused", bad)
		}
	}
}

func TestCatalogueMarksUnpricedPlansUnavailable(t *testing.T) {
	rows := []PlanRow{
		{Code: Plan365d, Name: "y", DurationDays: 365, Active: true, SortOrder: 2},
		{Code: Plan30d, Name: "m", DurationDays: 30, Active: true, SortOrder: 1},
		{Code: "retired", Name: "r", DurationDays: 7, Active: false},
	}
	got := Catalogue(rows, Prices{Plan30d: 14900}, EstimatedTax(), time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC))
	if len(got) != 2 || got[0].Code != Plan30d || got[1].Code != Plan365d {
		t.Fatalf("catalogue = %+v", got)
	}
	if !got[0].Available || got[0].PricePaise == nil || *got[0].PricePaise != 14900 {
		t.Fatalf("30d = %+v", got[0])
	}
	if got[1].Available || got[1].PricePaise != nil {
		t.Fatalf("an unpriced plan must not be available: %+v", got[1])
	}
}

func paidInput(p Period, ent *Entitlement, now time.Time) Input {
	return Input{Period: p, Entitlement: ent, Now: now, Event: Event{EventID: "e1", EventType: "payment.succeeded",
		PeriodID: p.ID, PayerID: p.UserID, AmountMinor: p.AmountPaise, Currency: "INR", IntentID: p.IntentID}}
}

func newPeriod(days int) Period {
	return Period{ID: uuid.New(), UserID: uuid.New(), PlanCode: Plan30d, DurationDays: days, AmountPaise: 14900,
		Currency: "INR", Status: StatusCreated, IntentID: uuid.NewString()}
}

func TestCaptureActivatesWithGrace(t *testing.T) {
	p := newPeriod(30)
	d := Decide(paidInput(p, nil, t0))
	if d.Outcome != OutcomeActivated || d.Notify != EventActivated {
		t.Fatalf("decision %+v", d)
	}
	wantEnd := t0.AddDate(0, 0, 30)
	if !d.Period.PeriodStart.Equal(t0) || !d.Period.PeriodEnd.Equal(wantEnd) || d.Period.Status != StatusPaid {
		t.Fatalf("period %+v", d.Period)
	}
	if !d.Entitlement.EndsAt.Equal(wantEnd) || !d.Entitlement.AccessUntil.Equal(wantEnd.Add(72*time.Hour)) {
		t.Fatalf("entitlement %+v", d.Entitlement)
	}
}

func TestCaptureStacksFromCurrentEnd(t *testing.T) {
	p := newPeriod(365)
	end := t0.Add(10 * 24 * time.Hour)
	ent := &Entitlement{UserID: p.UserID, EndsAt: end, AccessUntil: end.Add(GracePeriod)}
	d := Decide(paidInput(p, ent, t0))
	if !d.Period.PeriodStart.Equal(end) || !d.Entitlement.EndsAt.Equal(end.AddDate(0, 0, 365)) {
		t.Fatalf("stacked pass must start at the current end: %+v / %+v", d.Period, d.Entitlement)
	}
	// In grace (paid end passed): the new pass starts now, the gap is not paid for.
	lapsed := t0.Add(-24 * time.Hour)
	ent = &Entitlement{UserID: p.UserID, EndsAt: lapsed, AccessUntil: lapsed.Add(GracePeriod)}
	d = Decide(paidInput(p, ent, t0))
	if !d.Period.PeriodStart.Equal(t0) {
		t.Fatalf("a lapsed buyer's pass starts now, got %v", d.Period.PeriodStart)
	}
}

func TestCaptureMismatchesApplyNothing(t *testing.T) {
	p := newPeriod(30)
	for name, mut := range map[string]func(*Input){
		"amount":   func(in *Input) { in.Event.AmountMinor-- },
		"currency": func(in *Input) { in.Event.Currency = "USD" },
		"payer":    func(in *Input) { in.Event.PayerID = uuid.New() },
		"no payer": func(in *Input) { in.Event.PayerID = uuid.Nil },
		"intent":   func(in *Input) { in.Event.IntentID = uuid.NewString() },
	} {
		in := paidInput(p, nil, t0)
		mut(&in)
		d := Decide(in)
		if d.Outcome != OutcomeMismatch || d.Period != nil || d.Entitlement != nil {
			t.Fatalf("%s: %+v", name, d)
		}
	}
}

func TestCaptureOfPaidPassIsIgnored(t *testing.T) {
	p := newPeriod(30)
	d := Decide(paidInput(p, nil, t0))
	again := Decide(paidInput(*d.Period, d.Entitlement, t0.Add(time.Hour)))
	if again.Outcome != OutcomeIgnored || again.Entitlement != nil {
		t.Fatalf("a second capture must not extend: %+v", again)
	}
}

func TestLateCaptureOfAbandonedOrFailedActivates(t *testing.T) {
	for _, st := range []string{StatusAbandoned, StatusFailed} {
		p := newPeriod(30)
		p.Status = st
		if d := Decide(paidInput(p, nil, t0)); d.Outcome != OutcomeActivated {
			t.Fatalf("%s: %+v", st, d)
		}
	}
}

func TestFailedEvent(t *testing.T) {
	p := newPeriod(30)
	in := paidInput(p, nil, t0)
	in.Event.EventType = "payment.failed"
	if d := Decide(in); d.Outcome != OutcomeFailed || d.Period.Status != StatusFailed || d.Entitlement != nil {
		t.Fatalf("%+v", d)
	}
	p.Status = StatusPaid
	in.Period = p
	if d := Decide(in); d.Outcome != OutcomeIgnored {
		t.Fatalf("failed after paid must be ignored: %+v", d)
	}
}

func refundInput(p Period, ent *Entitlement, chain []Period, amount int64, now time.Time) Input {
	return Input{Period: p, Entitlement: ent, Chain: chain, Now: now, Event: Event{EventID: "r1", EventType: "payment.refunded",
		PeriodID: p.ID, AmountMinor: amount, IntentID: p.IntentID}}
}

func paidPeriod(user uuid.UUID, start time.Time, days int) Period {
	end := start.AddDate(0, 0, days)
	return Period{ID: uuid.New(), UserID: user, PlanCode: Plan30d, DurationDays: days, AmountPaise: 14900, Currency: "INR",
		Status: StatusPaid, IntentID: uuid.NewString(), PeriodStart: &start, PeriodEnd: &end}
}

func TestFullRefundOfOnlyPassEndsAccessNow(t *testing.T) {
	user := uuid.New()
	p := paidPeriod(user, t0, 30)
	ent := &Entitlement{UserID: user, EndsAt: *p.PeriodEnd, AccessUntil: p.PeriodEnd.Add(GracePeriod)}
	now := t0.Add(48 * time.Hour)
	d := Decide(refundInput(p, ent, nil, 14900, now))
	if d.Outcome != OutcomeRefunded || d.Period.Status != StatusRefunded {
		t.Fatalf("%+v", d)
	}
	if !d.Entitlement.AccessUntil.Equal(now) || !d.Entitlement.EndsAt.Equal(now) {
		t.Fatalf("access must end at the refund, without grace: %+v", d.Entitlement)
	}
}

func TestRefundOfFirstPassPullsTheNextOneEarlier(t *testing.T) {
	user := uuid.New()
	p1 := paidPeriod(user, t0, 30)
	p2 := paidPeriod(user, *p1.PeriodEnd, 30)
	ent := &Entitlement{UserID: user, EndsAt: *p2.PeriodEnd, AccessUntil: p2.PeriodEnd.Add(GracePeriod)}
	now := t0.Add(10 * 24 * time.Hour)
	d := Decide(refundInput(p1, ent, []Period{p2}, 14900, now))
	if len(d.Shifted) != 1 || !d.Shifted[0].PeriodStart.Equal(now) {
		t.Fatalf("p2 must now start at the refund: %+v", d.Shifted)
	}
	wantEnd := now.Add(p2.PeriodEnd.Sub(*p2.PeriodStart))
	if !d.Entitlement.EndsAt.Equal(wantEnd) || !d.Entitlement.AccessUntil.Equal(wantEnd.Add(GracePeriod)) {
		t.Fatalf("entitlement %+v, want ends %v", d.Entitlement, wantEnd)
	}
}

func TestRefundOfLaterPassKeepsTheEarlierOne(t *testing.T) {
	user := uuid.New()
	p1 := paidPeriod(user, t0, 30)
	p2 := paidPeriod(user, *p1.PeriodEnd, 365)
	ent := &Entitlement{UserID: user, EndsAt: *p2.PeriodEnd, AccessUntil: p2.PeriodEnd.Add(GracePeriod)}
	now := t0.Add(24 * time.Hour)
	d := Decide(refundInput(p2, ent, []Period{p1}, 14900, now))
	if len(d.Shifted) != 0 || !d.Entitlement.EndsAt.Equal(*p1.PeriodEnd) {
		t.Fatalf("%+v", d)
	}
}

func TestPartialRefundKeepsThePass(t *testing.T) {
	user := uuid.New()
	p := paidPeriod(user, t0, 30)
	d := Decide(refundInput(p, nil, nil, 4900, t0.Add(time.Hour)))
	if d.Outcome != OutcomePartialRefund || d.Period.Status != StatusPaid || d.Period.RefundedPaise != 4900 || d.Entitlement != nil {
		t.Fatalf("%+v", d)
	}
	// The rest of it completes the refund.
	p = *d.Period
	d = Decide(refundInput(p, nil, nil, 10000, t0.Add(2*time.Hour)))
	if d.Outcome != OutcomeRefunded || d.Entitlement == nil {
		t.Fatalf("%+v", d)
	}
}

func TestRefundGuards(t *testing.T) {
	user := uuid.New()
	p := paidPeriod(user, t0, 30)
	if d := Decide(refundInput(p, nil, nil, 14901, t0)); d.Outcome != OutcomeMismatch {
		t.Fatalf("over-refund: %+v", d)
	}
	in := refundInput(p, nil, nil, 14900, t0)
	in.Event.IntentID = uuid.NewString()
	if d := Decide(in); d.Outcome != OutcomeMismatch {
		t.Fatalf("foreign intent: %+v", d)
	}
	p.Status, p.RefundedPaise = StatusRefunded, 14900
	if d := Decide(refundInput(p, nil, nil, 14900, t0)); d.Outcome != OutcomeIgnored {
		t.Fatalf("second refund: %+v", d)
	}
}

func TestStatusOf(t *testing.T) {
	if s := StatusOf(nil, t0); s.Active || s.InGrace || s.EndsAt != nil {
		t.Fatalf("never bought: %+v", s)
	}
	ent := &Entitlement{EndsAt: t0, AccessUntil: t0.Add(GracePeriod)}
	if s := StatusOf(ent, t0.Add(-time.Second)); !s.Active || s.InGrace {
		t.Fatalf("paid: %+v", s)
	}
	if s := StatusOf(ent, t0.Add(time.Hour)); !s.Active || !s.InGrace {
		t.Fatalf("grace: %+v", s)
	}
	if s := StatusOf(ent, t0.Add(GracePeriod)); s.Active || s.InGrace {
		t.Fatalf("expired: %+v", s)
	}
}

func TestRoleSync(t *testing.T) {
	until := t0.Add(GracePeriod)
	ent := Entitlement{EndsAt: t0, AccessUntil: until}
	if a, at := RoleSync(ent, t0); a != RoleGrant || !at.Equal(until) {
		t.Fatalf("new: %v %v", a, at)
	}
	ent.RoleSyncedUntil = &until
	if a, _ := RoleSync(ent, t0); a != RoleNone {
		t.Fatalf("in sync: %v", a)
	}
	later := until.Add(24 * time.Hour)
	ent.AccessUntil = later
	if a, at := RoleSync(ent, t0); a != RoleGrant || !at.Equal(later) {
		t.Fatalf("extended: %v %v", a, at)
	}
	ent.AccessUntil = until
	if a, _ := RoleSync(ent, until); a != RoleRevoke {
		t.Fatalf("expired: %v", a)
	}
	ent.RoleSyncedUntil = nil
	if a, _ := RoleSync(ent, until); a != RoleNone {
		t.Fatalf("revoked: %v", a)
	}
}

func TestReminderDaysLeft(t *testing.T) {
	for _, c := range []struct {
		in   time.Duration
		want int
	}{
		{4 * 24 * time.Hour, -1},
		{72 * time.Hour, 3},
		{48 * time.Hour, 3},
		{24 * time.Hour, 0},
		{time.Hour, 0},
		{-time.Hour, 0},
		{-72 * time.Hour, -1},
	} {
		if got := ReminderDaysLeft(t0.Add(c.in), t0); got != c.want {
			t.Errorf("ends in %v: %d, want %d", c.in, got, c.want)
		}
	}
}

func TestBackoff(t *testing.T) {
	if Backoff(0) != time.Minute || Backoff(1) != 2*time.Minute || Backoff(20) != time.Hour {
		t.Fatal("backoff")
	}
}

func TestKeysAndCoordinates(t *testing.T) {
	id := uuid.New()
	if IntentKey(id) != "platform_premium:"+id.String() || ApplicationID != "premium" || Role != "premium" {
		t.Fatal("pinned coordinates changed")
	}
	if !strings.Contains(EventKey("a", "b"), "|") {
		t.Fatal("event key")
	}
}
