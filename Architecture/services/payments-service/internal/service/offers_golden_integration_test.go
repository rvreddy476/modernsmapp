//go:build integration

package service

// GOLDEN — a payment WITHOUT a bank offer is byte-for-byte what it was before
// bank offers existed.
//
// The founder authorised changing payment matching on 1 Oct 2026 only in one
// narrow way: a lower capture is accepted solely for an offer in our registry.
// Everything else must behave exactly as before. This test pins "exactly" for
// the whole life of an ordinary payment, end to end through the real Razorpay
// adapter: the Create Order request body, the webhook capture, a partial and a
// final refund (request bodies to the provider included), every outbox event,
// and every ledger row the payment writes.
//
// The golden file was RECORDED FROM THE CODE BEFORE THE OFFERS CHANGE (run with
// PAYMENTS_GOLDEN_UPDATE=1 on the pre-change tree) and is compared, not
// rewritten, afterwards. Ledger rows are read with the column lists that
// existed before the change, and the columns the offers migration adds are
// asserted NULL separately — so a new column cannot hide a changed old one.
//
//	PAYMENTS_TEST_DSN=... go test -tags=integration ./internal/service/ -run Golden -v -count=1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/atpost/payments-service/internal/gateway"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/google/uuid"
)

const goldenNoOfferFile = "testdata/no_offer_payment.golden"

// deliverWebhook is HandleWebhook's path without the HTTP framing: the REAL
// adapter verifies the signed raw body, and the event is mapped onto
// WebhookInput field for field as internal/http/handler.go maps it.
func deliverWebhook(t *testing.T, svc *Service, p gateway.Provider, headers http.Header, body []byte) error {
	t.Helper()
	ev, err := p.VerifyWebhook(context.Background(), headers, body)
	if err != nil {
		t.Fatalf("the signed fixture did not verify: %v", err)
	}
	return svc.ApplyWebhook(context.Background(), WebhookInput{
		Provider:          p.Name(),
		EventID:           ev.EventID,
		EventType:         ev.Type,
		ProviderOrderID:   ev.ProviderOrderID,
		ProviderPaymentID: ev.ProviderPaymentID,
		ProviderRefundID:  ev.ProviderRefundID,
		AmountMinor:       ev.Amount.Minor,
		Currency:          ev.Amount.Currency,
	})
}

var (
	goldenUUID = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	goldenTS   = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}(:\d{2})?)?`)
)

func TestGolden_NoOfferPaymentIsUnchanged(t *testing.T) {
	ctx := context.Background()
	rz := newOfferRazorpay(t)
	prov := rz.provider()
	store := postgres.New(recPool)
	svc := New(store, nil).WithProvider(prov)

	payer, payee, ref := uuid.New(), uuid.New(), uuid.New()
	idem := "golden-" + uuid.NewString()
	intent, err := svc.InitiatePayment(ctx, InitiateInput{
		PayerID: payer, PayeeID: payee, ReferenceType: "order", ReferenceID: ref,
		AmountMinor: 118000, Currency: "INR", Method: "card",
		IdempotencyKey: idem, OwnerDomain: "commerce-service", ApplicationID: "mstore",
	})
	if err != nil {
		t.Fatalf("initiate: %v", err)
	}
	order := intent.ProviderRef
	payID := "pay_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:14]
	pay := fakePayment{ID: payID, OrderID: order, Currency: "INR", Status: "captured", Amount: 118000, CreatedAt: time.Now().Unix()}
	rz.putPayment(pay)

	h, b := signedPaymentWebhook("evt_cap_"+payID, "payment.captured", pay, time.Now().Unix())
	if err := deliverWebhook(t, svc, prov, h, b); err != nil {
		t.Fatalf("capture webhook: %v", err)
	}
	// Razorpay also announces the capture as order.paid; it changes nothing.
	h, b = signedPaymentWebhook("evt_paid_"+payID, "order.paid", pay, time.Now().Unix())
	if err := deliverWebhook(t, svc, prov, h, b); err != nil {
		t.Fatalf("order.paid webhook: %v", err)
	}

	refundIDs := []string{}
	for i, amount := range []int64{30000, 88000} {
		cmd, err := svc.RequestRefund(ctx, RefundRequest{
			IntentID: intent.ID, AmountMinor: amount, Reason: "golden",
			ProviderIdempotencyKey: fmt.Sprintf("%s-refund-%d", idem, i),
			CallerDomain:           "commerce-service", ApplicationID: "mstore",
		})
		if err != nil {
			t.Fatalf("refund %d: %v", i, err)
		}
		svc.attemptRefund(ctx, *cmd)
		got, err := store.GetRefundCommand(ctx, cmd.ID)
		if err != nil || got == nil || got.ProviderRefundID == "" {
			t.Fatalf("refund %d was not submitted: %+v %v", i, got, err)
		}
		refundIDs = append(refundIDs, got.ProviderRefundID)
		h, b := signedRefundWebhook("evt_rfnd_"+got.ProviderRefundID, got.ProviderRefundID, payID, amount, time.Now().Unix())
		if err := deliverWebhook(t, svc, prov, h, b); err != nil {
			t.Fatalf("refund webhook %d: %v", i, err)
		}
	}

	orderBodies, refundBodies, expands := rz.snapshot()
	if len(expands) != 0 {
		t.Fatalf("an exact capture must never fetch offers; fetched with %v", expands)
	}

	var out strings.Builder
	section := func(name string, lines ...string) {
		out.WriteString("## " + name + "\n")
		for _, l := range lines {
			out.WriteString(l + "\n")
		}
	}
	section("razorpay create order request", orderBodies...)
	section("razorpay refund requests", refundBodies...)
	section("outbox events", goldenRows(t, ctx,
		`SELECT event_type || ' ' || partition_key || ' ' || payload::text FROM payments.outbox_events
		  WHERE payload::text LIKE '%' || $1 || '%' ORDER BY id`, intent.ID.String())...)
	section("payment_intents", goldenRows(t, ctx,
		`SELECT row_to_json(x)::text FROM (SELECT id, payer_id, payee_id, reference_type, reference_id, amount,
		        currency, method, status, provider_ref, upi_intent_url, metadata, idempotency_key, created_at,
		        updated_at, refunded_amount_minor, amount_minor, provider, provider_order_id, provider_payment_id,
		        owner_domain, refund_reserved_minor, application_id, channel
		   FROM payments.payment_intents WHERE id = $1) x`, intent.ID)...)
	section("payment_audit_log", goldenRows(t, ctx,
		`SELECT row_to_json(x)::text FROM (SELECT intent_id, event, old_status, new_status, actor_id, metadata, created_at
		   FROM payments.payment_audit_log WHERE intent_id = $1 ORDER BY id) x`, intent.ID)...)
	section("provider_events", goldenRows(t, ctx,
		`SELECT row_to_json(x)::text FROM (SELECT provider, event_id, event_type, provider_order_id, provider_payment_id
		   FROM payments.provider_events WHERE provider_order_id = $1 OR provider_payment_id = $2
		  ORDER BY received_at, event_id) x`, order, payID)...)
	section("refund_commands", goldenRows(t, ctx,
		`SELECT row_to_json(x)::text FROM (SELECT intent_id, amount_minor, currency, reason, provider_idempotency_key,
		        status, provider, provider_refund_id, attempts, last_error, requested_by, failure_code, resolution,
		        resolution_note, resolved_by, application_id
		   FROM payments.refund_commands WHERE intent_id = $1 ORDER BY created_at, provider_idempotency_key) x`, intent.ID)...)
	section("provider_refunds_applied", goldenRows(t, ctx,
		`SELECT row_to_json(x)::text FROM (SELECT provider, provider_refund_id, command_id, intent_id, amount_minor, application_id
		   FROM payments.provider_refunds_applied WHERE intent_id = $1 ORDER BY provider_refund_id) x`, intent.ID)...)

	got := out.String()
	// Run-specific identifiers become stable placeholders, longest first so a
	// refund id that embeds the payment id is replaced whole.
	repl := [][2]string{}
	for i, r := range refundIDs {
		repl = append(repl, [2]string{r, fmt.Sprintf("<refund_%d>", i)})
	}
	repl = append(repl,
		[2]string{strings.TrimPrefix(payID, "pay_"), "<payment>"},
		[2]string{order, "<order>"},
		[2]string{idem, "<idem>"},
		[2]string{intent.ID.String(), "<intent>"},
		[2]string{payer.String(), "<payer>"},
		[2]string{payee.String(), "<payee>"},
		[2]string{ref.String(), "<reference>"},
	)
	for _, r := range repl {
		got = strings.ReplaceAll(got, r[0], r[1])
	}
	got = goldenUUID.ReplaceAllString(got, "<uuid>")
	got = goldenTS.ReplaceAllString(got, "<ts>")

	if os.Getenv("PAYMENTS_GOLDEN_UPDATE") == "1" {
		if err := os.MkdirAll(filepath.Dir(goldenNoOfferFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenNoOfferFile, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("golden written to %s", goldenNoOfferFile)
		return
	}
	want, err := os.ReadFile(goldenNoOfferFile)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if got != string(want) {
		t.Fatalf("a payment without an offer CHANGED.\n--- want\n%s\n--- got\n%s", want, got)
	}

	assertNoOfferColumnsNull(t, ctx, intent.ID)
}

func goldenRows(t *testing.T, ctx context.Context, query string, args ...any) []string {
	t.Helper()
	rows, err := recPool.Query(ctx, query, args...)
	if err != nil {
		t.Fatalf("golden query: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// assertNoOfferColumnsNull: the columns migration 014 adds stay NULL for a
// payment no offer touched. Before migration 014 exists the columns do not,
// and there is nothing to assert.
func assertNoOfferColumnsNull(t *testing.T, ctx context.Context, intentID uuid.UUID) {
	t.Helper()
	var n int
	if err := recPool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.columns
		  WHERE table_schema = 'payments' AND table_name = 'payment_intents' AND column_name = 'captured_minor'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		return
	}
	var nonNull []string
	err := recPool.QueryRow(ctx,
		`SELECT array_remove(ARRAY[
		    CASE WHEN i.captured_minor IS NOT NULL THEN 'payment_intents.captured_minor' END,
		    CASE WHEN i.offer_id IS NOT NULL THEN 'payment_intents.offer_id' END,
		    CASE WHEN i.offer_discount_minor IS NOT NULL THEN 'payment_intents.offer_discount_minor' END,
		    CASE WHEN i.refunded_captured_minor IS NOT NULL THEN 'payment_intents.refunded_captured_minor' END,
		    CASE WHEN EXISTS (SELECT 1 FROM payments.refund_commands c WHERE c.intent_id = i.id AND c.provider_amount_minor IS NOT NULL)
		         THEN 'refund_commands.provider_amount_minor' END,
		    CASE WHEN EXISTS (SELECT 1 FROM payments.provider_refunds_applied r WHERE r.intent_id = i.id AND r.order_value_minor IS NOT NULL)
		         THEN 'provider_refunds_applied.order_value_minor' END
		  ], NULL)
		   FROM payments.payment_intents i WHERE i.id = $1`, intentID).Scan(&nonNull)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(nonNull) > 0 {
		t.Fatalf("a payment without an offer wrote offer columns: %v", nonNull)
	}
}
