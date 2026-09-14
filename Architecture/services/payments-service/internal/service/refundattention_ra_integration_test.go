//go:build integration

package service

// A parked refund is announced once, counted, listed and resolved once.
//
// The refund worker parks a refund that can never succeed. Before this, that
// was one ERROR log line: no event for the owning domain, no metric to alarm
// on, and no way to close the command except by editing the row.
//
// Same rig as refundworker_rw: the real Razorpay adapter against a scripted
// server, and a live PostgreSQL (payments_it_test).
//
//	PAYMENTS_TEST_DSN=... go test -tags=integration ./internal/service/... -run 'Park|Resolv|NeedsAttention' -v

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/atpost/payments-service/internal/obs"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/google/uuid"
)

func raRejectRefund(rzp *rwRazorpay, paymentID string) {
	rzp.scriptRefund(paymentID, rwReply{http.StatusBadRequest, rwError("The id provided does not exist", "input_validation_failed")})
}

// raParked is a captured intent whose refund Razorpay refuses, parked by one tick.
func raParked(t *testing.T) (*Service, *rwRazorpay, rwPaid, uuid.UUID) {
	t.Helper()
	rzp := newRWRazorpay(t)
	order, pay := rwIDs()
	pi := rwSeedPaid(t, 60712, order, pay)
	svc := svcWith(t, rzp.provider())
	raRejectRefund(rzp, pay)
	cmd := rwRequestRefund(t, svc, pi)
	rwTick(t, svc, cmd)
	if c := rwReadCommand(t, cmd); c.status != "needs_attention" {
		t.Fatalf("setup: command = %+v, want needs_attention", c)
	}
	return svc, rzp, pi, cmd
}

func raRefundFailedEvents(t *testing.T, cmd uuid.UUID) int {
	t.Helper()
	return countBy(t,
		`SELECT count(*) FROM payments.outbox_events
		  WHERE event_type = 'payment.refund_failed' AND payload->'payload'->>'command_id' = $1`, cmd.String())
}

func raOutboxRows(t *testing.T, pi rwPaid) int {
	t.Helper()
	return countBy(t, `SELECT count(*) FROM payments.outbox_events WHERE partition_key = $1`, pi.id.String())
}

func raParkedCount(t *testing.T) int {
	t.Helper()
	return countBy(t, `SELECT count(*) FROM payments.refund_commands WHERE status = 'needs_attention'`)
}

func raResolve(resolution, note, operator string, cmd uuid.UUID) postgres.ResolveRefundInput {
	return postgres.ResolveRefundInput{
		CommandID: cmd, Resolution: resolution, Note: note, OperatorID: operator, Credential: "internal_key",
	}
}

func raResolvedAudits(t *testing.T, cmd uuid.UUID) int {
	t.Helper()
	return countBy(t,
		`SELECT count(*) FROM payments.payment_audit_log
		  WHERE event = 'refund_command_resolved' AND metadata->>'command_id' = $1`, cmd.String())
}

// ─── The event ───────────────────────────────────────────────────────

func TestParkingARefundPublishesExactlyOneRefundFailedEvent(t *testing.T) {
	ctx := context.Background()
	svc, rzp, pi, cmd := raParked(t)

	if n := raRefundFailedEvents(t, cmd); n != 1 {
		t.Fatalf("payment.refund_failed events = %d after the park, want 1", n)
	}
	var raw string
	if err := recPool.QueryRow(ctx,
		`SELECT payload::text FROM payments.outbox_events
		  WHERE event_type = 'payment.refund_failed' AND payload->'payload'->>'command_id' = $1`,
		cmd.String()).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, rwRawBodyMarker) {
		t.Fatalf("the event carries the raw provider body: %s", raw)
	}
	var env struct {
		EventID   string         `json:"event_id"`
		EventType string         `json:"event_type"`
		Payload   map[string]any `json:"payload"`
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatal(err)
	}
	var refID string
	if err := recPool.QueryRow(ctx, `SELECT reference_id::text FROM payments.payment_intents WHERE id=$1`, pi.id).Scan(&refID); err != nil {
		t.Fatal(err)
	}
	p := env.Payload
	want := map[string]any{
		"id": pi.id.String(), "intent_id": pi.id.String(), "command_id": cmd.String(),
		"reference_type": "order", "reference_id": refID, "amount_minor": float64(60712),
		"currency": "INR", "reason_code": RefundFailProviderRejected, "status": "needs_attention",
		"provider": "razorpay",
	}
	for k, v := range want {
		if p[k] != v {
			t.Errorf("payload[%s] = %v, want %v", k, p[k], v)
		}
	}
	if reason, _ := p["reason"].(string); !strings.Contains(reason, "does not exist") || !strings.Contains(reason, "400") {
		t.Errorf("payload reason = %q; it must carry the redacted status and description", reason)
	}
	if env.EventID == "" || env.EventType != "payment.refund_failed" {
		t.Errorf("envelope = %+v", env)
	}
	var code string
	if err := recPool.QueryRow(ctx, `SELECT COALESCE(failure_code,'') FROM payments.refund_commands WHERE id=$1`, cmd).Scan(&code); err != nil {
		t.Fatal(err)
	}
	if code != RefundFailProviderRejected {
		t.Errorf("failure_code = %q, want %s", code, RefundFailProviderRejected)
	}

	// A second tick finds nothing to do and announces nothing.
	rwTick(t, svc, cmd)
	if n := raRefundFailedEvents(t, cmd); n != 1 {
		t.Fatalf("payment.refund_failed events = %d after a second tick, want still 1", n)
	}
	if n := len(rzp.all()); n != 1 {
		t.Fatalf("provider calls = %d, want 1", n)
	}

	// Parking an already-parked command writes nothing.
	parked, err := svc.store.ParkRefundCommand(ctx, cmd, RefundFailProviderRejected, "parked again")
	if err != nil || parked {
		t.Fatalf("second park = %v, %v; want false, nil", parked, err)
	}
	if n := raRefundFailedEvents(t, cmd); n != 1 {
		t.Fatalf("payment.refund_failed events = %d after a repeated park, want still 1", n)
	}
}

// The event is written in the park's own transaction: when it cannot be
// written, the command is not parked either, and the next tick parks it with
// its event.
func TestAParkWhoseEventCannotBeWrittenLeavesTheCommandUnparked(t *testing.T) {
	ctx := context.Background()
	rzp := newRWRazorpay(t)
	order, pay := rwIDs()
	pi := rwSeedPaid(t, 60712, order, pay)
	svc := svcWith(t, rzp.provider())
	raRejectRefund(rzp, pay)
	cmd := rwRequestRefund(t, svc, pi)

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	fn := "payments.pay4_reject_refund_failed_" + suffix
	trg := "pay4_reject_refund_failed_" + suffix
	exec := func(sql string) {
		t.Helper()
		if _, err := recPool.Exec(ctx, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	exec(fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $f$
		BEGIN RAISE EXCEPTION 'injected: the outbox refuses this payment.refund_failed'; END $f$`, fn))
	exec(fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON payments.outbox_events FOR EACH ROW
		WHEN (NEW.event_type = 'payment.refund_failed' AND NEW.payload->'payload'->>'command_id' = '%s')
		EXECUTE FUNCTION %s()`, trg, cmd, fn))
	dropped := false
	drop := func() {
		if dropped {
			return
		}
		dropped = true
		_, _ = recPool.Exec(ctx, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON payments.outbox_events`, trg))
		_, _ = recPool.Exec(ctx, fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, fn))
	}
	t.Cleanup(drop)

	rwTick(t, svc, cmd)
	if c := rwReadCommand(t, cmd); c.status == "needs_attention" {
		t.Fatalf("command = %+v: parked although its payment.refund_failed was never written", c)
	}
	if n := raRefundFailedEvents(t, cmd); n != 0 {
		t.Fatalf("payment.refund_failed events = %d with the outbox refusing them", n)
	}

	drop()
	rwTick(t, svc, cmd)
	if c := rwReadCommand(t, cmd); c.status != "needs_attention" {
		t.Fatalf("command = %+v after the outbox recovered, want needs_attention", c)
	}
	if n := raRefundFailedEvents(t, cmd); n != 1 {
		t.Fatalf("payment.refund_failed events = %d, want 1", n)
	}
}

// ─── The metric ──────────────────────────────────────────────────────

func TestNeedsAttentionGaugeTracksTheParkedCount(t *testing.T) {
	ctx := context.Background()
	svc, _, _, cmd := raParked(t)

	parked := raParkedCount(t)
	if got := obs.RefundsNeedingAttention(); parked < 1 || got != float64(parked) {
		t.Fatalf("payments_refunds_needs_attention = %v after a tick, database holds %d", got, parked)
	}
	if n := obs.RefundParkedCount(RefundFailProviderRejected); n < 1 {
		t.Errorf("payments_refund_parked_total{reason_code=provider_rejected} = %v, want >= 1", n)
	}

	if _, err := svc.ResolveRefundCommand(ctx, raResolve(postgres.ResolutionTestData, "gauge proof", "ra-operator", cmd)); err != nil {
		t.Fatal(err)
	}
	after := raParkedCount(t)
	if after != parked-1 {
		t.Fatalf("setup: parked count %d -> %d after one resolution", parked, after)
	}
	if got := obs.RefundsNeedingAttention(); got != float64(after) {
		t.Fatalf("payments_refunds_needs_attention = %v after a resolution, database holds %d", got, after)
	}
}

// ─── Resolution ──────────────────────────────────────────────────────

func TestResolvingAsTestDataResolvesOnceAndReplaysTheSameResult(t *testing.T) {
	ctx := context.Background()
	svc, rzp, pi, cmd := raParked(t)
	before := raOutboxRows(t, pi)

	res, err := svc.ResolveRefundCommand(ctx, raResolve(postgres.ResolutionTestData, "dev seed: simulated payment", "op-ra-1", cmd))
	if err != nil {
		t.Fatal(err)
	}
	if res.Replayed || res.Status != "resolved" || res.Resolution != "test_data" || res.ResolvedBy != "op-ra-1" ||
		res.RefundEventEmitted || res.ResolvedAt.IsZero() {
		t.Fatalf("resolution = %+v", res)
	}
	var status, resolution, note, by string
	var reserved int64
	if err := recPool.QueryRow(ctx,
		`SELECT c.status, COALESCE(c.resolution,''), COALESCE(c.resolution_note,''), COALESCE(c.resolved_by,''),
		        i.refund_reserved_minor
		   FROM payments.refund_commands c JOIN payments.payment_intents i ON i.id = c.intent_id
		  WHERE c.id = $1`, cmd).Scan(&status, &resolution, &note, &by, &reserved); err != nil {
		t.Fatal(err)
	}
	if status != "resolved" || resolution != "test_data" || note != "dev seed: simulated payment" || by != "op-ra-1" {
		t.Fatalf("stored = %s/%s/%q/%s", status, resolution, note, by)
	}
	if reserved != 0 {
		t.Errorf("refund_reserved_minor = %d; the resolved command's reservation must be released", reserved)
	}
	if n := countBy(t,
		`SELECT count(*) FROM payments.payment_audit_log
		  WHERE event = 'refund_command_resolved' AND metadata->>'command_id' = $1
		    AND metadata->>'operator_id' = 'op-ra-1' AND metadata->>'resolution' = 'test_data'`, cmd.String()); n != 1 {
		t.Fatalf("audit rows naming the operator and resolution = %d, want 1", n)
	}
	if n := raOutboxRows(t, pi); n != before {
		t.Fatalf("outbox rows %d -> %d: test_data must emit nothing", before, n)
	}

	replay, err := svc.ResolveRefundCommand(ctx, raResolve(postgres.ResolutionWrittenOff, "second thoughts", "op-ra-2", cmd))
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || replay.Resolution != "test_data" || replay.ResolvedBy != "op-ra-1" ||
		replay.Note != "dev seed: simulated payment" || !replay.ResolvedAt.Equal(res.ResolvedAt) {
		t.Fatalf("replay = %+v, want the stored test_data resolution", replay)
	}
	if n := raResolvedAudits(t, cmd); n != 1 {
		t.Fatalf("audit rows = %d after a replay, want 1", n)
	}
	if n := raOutboxRows(t, pi); n != before {
		t.Fatalf("outbox rows %d -> %d after a replay", before, n)
	}
	rwTick(t, svc, cmd)
	if c := rwReadCommand(t, cmd); c.status != "resolved" || len(rzp.all()) != 1 {
		t.Fatalf("after a tick: command = %+v, provider calls = %d", c, len(rzp.all()))
	}
}

func TestResolvingAsWrittenOffEmitsNothing(t *testing.T) {
	ctx := context.Background()
	svc, _, pi, cmd := raParked(t)
	before := raOutboxRows(t, pi)

	res, err := svc.ResolveRefundCommand(ctx, raResolve(postgres.ResolutionWrittenOff, "customer unreachable; approved by finance", "op-ra-3", cmd))
	if err != nil {
		t.Fatal(err)
	}
	if res.RefundEventEmitted || res.Resolution != "written_off" {
		t.Fatalf("resolution = %+v", res)
	}
	if n := raOutboxRows(t, pi); n != before {
		t.Fatalf("outbox rows %d -> %d: written_off must emit nothing", before, n)
	}
	if n := rwRefundedEvents(t, pi); n != 0 {
		t.Fatalf("payment.refunded events = %d for a written-off refund", n)
	}
	if n := rwRefundedMinor(t, pi); n != 0 {
		t.Errorf("refunded_amount_minor = %d for a written-off refund", n)
	}
	if s := statusOf(t, pi.id); s != "succeeded" {
		t.Errorf("intent status = %q, want succeeded", s)
	}
}

// refunded_manually credits the ledger and publishes payment.refunded, marked
// manual, exactly once — however often it is replayed.
func TestResolvingAsRefundedManuallyEmitsOneRefundedEvent(t *testing.T) {
	ctx := context.Background()
	svc, rzp, pi, cmd := raParked(t)

	res, err := svc.ResolveRefundCommand(ctx, raResolve(postgres.ResolutionRefundedManually, "UPI refund sent from the ops account, ref 4711", "op-ra-4", cmd))
	if err != nil {
		t.Fatal(err)
	}
	if !res.RefundEventEmitted || res.Replayed {
		t.Fatalf("resolution = %+v", res)
	}
	if n := rwRefundedEvents(t, pi); n != 1 {
		t.Fatalf("payment.refunded events = %d, want 1", n)
	}
	if n := countBy(t,
		`SELECT count(*) FROM payments.outbox_events
		  WHERE event_type = 'payment.refunded' AND partition_key = $1
		    AND payload->'payload'->>'manual' = 'true' AND payload->'payload'->>'command_id' = $2
		    AND payload->'payload'->>'reference_type' = 'order' AND payload->'payload'->>'status' = 'refunded'
		    AND (payload->'payload'->>'amount_minor')::bigint = 60712`, pi.id.String(), cmd.String()); n != 1 {
		t.Fatalf("manual payment.refunded with the expected payload = %d, want 1", n)
	}
	if n := rwRefundedMinor(t, pi); n != 60712 {
		t.Errorf("refunded_amount_minor = %d, want 60712", n)
	}
	if s := statusOf(t, pi.id); s != "refunded" {
		t.Errorf("intent status = %q, want refunded", s)
	}

	replay, err := svc.ResolveRefundCommand(ctx, raResolve(postgres.ResolutionRefundedManually, "again", "op-ra-5", cmd))
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || replay.RefundEventEmitted || replay.ResolvedBy != "op-ra-4" {
		t.Fatalf("replay = %+v", replay)
	}
	if n := rwRefundedEvents(t, pi); n != 1 {
		t.Fatalf("payment.refunded events = %d after a replay, want still 1", n)
	}
	if n := rwRefundedMinor(t, pi); n != 60712 {
		t.Errorf("refunded_amount_minor = %d after a replay — credited twice", n)
	}
	rwTick(t, svc, cmd)
	if n := len(rzp.all()); n != 1 {
		t.Errorf("provider calls = %d after resolving, want 1", n)
	}
}

// The provider's settle path would also settle a SUBMITTED sibling command of
// the same amount on the same intent. A manual resolution must not.
func TestAManualResolutionNeverSettlesASiblingRefund(t *testing.T) {
	ctx := context.Background()
	rzp := newRWRazorpay(t)
	order, pay := rwIDs()
	pi := rwSeedPaid(t, 60000, order, pay)
	svc := svcWith(t, rzp.provider())
	request := func(key string) uuid.UUID {
		t.Helper()
		c, err := svc.RequestRefund(ctx, RefundRequest{
			IntentID: pi.id, AmountMinor: 30000, Reason: "ra sibling", ProviderIdempotencyKey: key, CallerDomain: "commerce",
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = recPool.Exec(context.Background(),
				`UPDATE payments.refund_commands SET next_attempt_at = NOW() + INTERVAL '30 days'
				  WHERE id = $1 AND status IN ('pending','submitted')`, c.ID)
		})
		return c.ID
	}

	a := request("ra-a-" + pi.id.String())
	raRejectRefund(rzp, pay)
	rwTick(t, svc, a)
	b := request("ra-b-" + pi.id.String())
	rzp.scriptRefund(pay, rwOK(rwRefundEntity("rfnd_"+pay[4:], pay, 30000, "processed")))
	rwTick(t, svc, b)
	if ca, cb := rwReadCommand(t, a), rwReadCommand(t, b); ca.status != "needs_attention" || cb.status != "submitted" {
		t.Fatalf("setup: a = %+v, b = %+v", ca, cb)
	}

	if _, err := svc.ResolveRefundCommand(ctx, raResolve(postgres.ResolutionRefundedManually, "half refunded by hand", "op-ra-6", a)); err != nil {
		t.Fatal(err)
	}
	if cb := rwReadCommand(t, b); cb.status != "submitted" {
		t.Fatalf("sibling = %+v, want still submitted: its provider refund has not settled", cb)
	}
	if n := rwRefundedMinor(t, pi); n != 30000 {
		t.Errorf("refunded_amount_minor = %d, want 30000", n)
	}
	if s := statusOf(t, pi.id); s != "partially_refunded" {
		t.Errorf("intent status = %q, want partially_refunded", s)
	}
	if n := rwRefundedEvents(t, pi); n != 1 {
		t.Errorf("payment.refunded events = %d, want 1", n)
	}
}

func TestResolveRefusesACommandThatIsNotParkedOrNotTheCallers(t *testing.T) {
	ctx := context.Background()
	rzp := newRWRazorpay(t)
	order, pay := rwIDs()
	pi := rwSeedPaid(t, 60712, order, pay)
	svc := svcWith(t, rzp.provider())
	pending := rwRequestRefund(t, svc, pi)

	if _, err := svc.ResolveRefundCommand(ctx, raResolve(postgres.ResolutionTestData, "n", "op", pending)); !errors.Is(err, postgres.ErrRefundCommandNotParked) {
		t.Fatalf("resolving a pending command: err = %v, want ErrRefundCommandNotParked", err)
	}
	if c := rwReadCommand(t, pending); c.status != "pending" {
		t.Fatalf("command = %+v, want pending", c)
	}
	if _, err := svc.ResolveRefundCommand(ctx, raResolve(postgres.ResolutionTestData, "n", "op", uuid.New())); !errors.Is(err, postgres.ErrRefundCommandNotFound) {
		t.Fatalf("unknown command: err = %v, want ErrRefundCommandNotFound", err)
	}

	_, _, _, parked := raParked(t)
	in := raResolve(postgres.ResolutionTestData, "n", "op", parked)
	in.OwnerDomain = "food-service"
	if _, err := svc.ResolveRefundCommand(ctx, in); !errors.Is(err, postgres.ErrRefundCommandNotFound) {
		t.Fatalf("another domain's command: err = %v, want ErrRefundCommandNotFound", err)
	}
	if c := rwReadCommand(t, parked); c.status != "needs_attention" {
		t.Fatalf("command = %+v, want still needs_attention", c)
	}
}

// ─── The list ────────────────────────────────────────────────────────

func raListAll(t *testing.T, svc *Service, f postgres.NeedsAttentionFilter) map[uuid.UUID]postgres.NeedsAttentionRefund {
	t.Helper()
	seen := map[uuid.UUID]postgres.NeedsAttentionRefund{}
	for page := 0; ; page++ {
		if page > 5000 {
			t.Fatal("the list never ended")
		}
		items, next, err := svc.ListRefundsNeedingAttention(context.Background(), f)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) > f.Limit {
			t.Fatalf("page of %d items with limit %d", len(items), f.Limit)
		}
		for _, it := range items {
			if _, dup := seen[it.ID]; dup {
				t.Fatalf("command %s listed on two pages", it.ID)
			}
			seen[it.ID] = it
		}
		if next == nil {
			return seen
		}
		f.After = next
	}
}

func TestTheNeedsAttentionListShowsOnlyParkedCommands(t *testing.T) {
	ctx := context.Background()
	svc, _, parkedPI, parked := raParked(t)
	_, _, _, resolved := raParked(t)
	if _, err := svc.ResolveRefundCommand(ctx, raResolve(postgres.ResolutionTestData, "list proof", "op-ra-7", resolved)); err != nil {
		t.Fatal(err)
	}
	order, pay := rwIDs()
	pendingPI := rwSeedPaid(t, 60712, order, pay)
	pending := rwRequestRefund(t, svc, pendingPI)

	seen := raListAll(t, svc, postgres.NeedsAttentionFilter{Limit: 3, ReferenceType: "order"})
	for id, it := range seen {
		var status string
		if err := recPool.QueryRow(ctx, `SELECT status FROM payments.refund_commands WHERE id=$1`, id).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != "needs_attention" || it.ReferenceType != "order" {
			t.Fatalf("listed command %s is %s (reference %s)", id, status, it.ReferenceType)
		}
	}
	it, ok := seen[parked]
	if !ok {
		t.Fatalf("the parked command %s is not listed (%d listed)", parked, len(seen))
	}
	if it.IntentID != parkedPI.id || it.AmountMinor != 60712 || it.Currency != "INR" ||
		it.ReasonCode != RefundFailProviderRejected || it.Attempts != 1 ||
		it.ProviderPaymentID != parkedPI.payment || !strings.Contains(it.Reason, "does not exist") ||
		it.CreatedAt.IsZero() || it.UpdatedAt.IsZero() {
		t.Errorf("listed item = %+v", it)
	}
	if _, ok := seen[resolved]; ok {
		t.Error("a resolved command is listed")
	}
	if _, ok := seen[pending]; ok {
		t.Error("a pending command is listed")
	}

	if other := raListAll(t, svc, postgres.NeedsAttentionFilter{Limit: 50, OwnerDomain: "food-service"}); len(other) > 0 {
		if _, ok := other[parked]; ok {
			t.Error("another domain's list includes a commerce command")
		}
	}
}
