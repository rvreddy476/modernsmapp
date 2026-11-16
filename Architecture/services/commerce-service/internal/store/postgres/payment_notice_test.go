package postgres

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBuildPaymentNotices_NothingOwedIsNoNotice(t *testing.T) {
	if got := buildPaymentNotices(nil, nil); len(got) != 0 {
		t.Fatalf("no rows: %v, want no notices", got)
	}
}

// Defect 1: the duplicate notice showed the ORDER value (121810) on a
// bank-offer order whose buyer paid 109629. It now shows the money.
func TestBuildPaymentNotices_DuplicateShowsTheMoneyNotTheOrderValue(t *testing.T) {
	order := uuid.New()
	at := time.Date(2026, 9, 9, 10, 5, 0, 0, time.UTC)
	got := buildPaymentNotices([]owedNoticeRow{{
		OrderID: order, Reason: OwedReasonDuplicateOrderCapture, AmountMinor: 109629, Exact: true,
		Currency: "INR", RefundStatus: OwedRefundPending, DetectedAt: at,
	}}, nil)
	n := got[order]
	if len(n) != 1 || n[0].Code != PaymentNoticeDuplicateCapture || n[0].AmountMinor.Int64() != 109629 ||
		n[0].AmountApproximate || n[0].RefundStatus != "pending" || n[0].RefundedAt != nil || n[0].Payments != 1 {
		t.Fatalf("notice = %+v, want duplicate_capture 109629 exact, pending", n)
	}
}

// An amount never confirmed as money is flagged, never shown as confirmed.
func TestBuildPaymentNotices_StatedAmountIsFlaggedApproximate(t *testing.T) {
	order := uuid.New()
	at := time.Date(2026, 9, 9, 10, 5, 0, 0, time.UTC)
	got := buildPaymentNotices([]owedNoticeRow{
		{OrderID: order, Reason: OwedReasonDuplicateOrderCapture, AmountMinor: 121810, Exact: false, RefundStatus: OwedRefundPending, DetectedAt: at},
		{OrderID: order, Reason: OwedReasonDuplicateOrderCapture, AmountMinor: 5000, Exact: true, RefundStatus: OwedRefundPending, DetectedAt: at.Add(time.Hour)},
	}, nil)[order]
	if len(got) != 1 || !got[0].AmountApproximate || got[0].AmountMinor.Int64() != 126810 || got[0].Payments != 2 ||
		got[0].Currency != "INR" {
		t.Fatalf("notice = %+v, want approximate 126810 over 2 payments, INR defaulted", got)
	}
}

// Defect 2: refund status. Refunded only when every payment is back, with the
// last refund's time; any stalled refund makes the notice failed.
func TestBuildPaymentNotices_RefundStatusAggregates(t *testing.T) {
	order := uuid.New()
	at := time.Date(2026, 9, 9, 10, 0, 0, 0, time.FixedZone("IST", 19800))
	r1, r2 := at.Add(time.Hour), at.Add(2*time.Hour)
	row := func(status string, refunded *time.Time) owedNoticeRow {
		return owedNoticeRow{OrderID: order, Reason: OwedReasonDuplicateOrderCapture, AmountMinor: 100, Exact: true,
			RefundStatus: status, RefundedAt: refunded, DetectedAt: at}
	}
	all := buildPaymentNotices([]owedNoticeRow{row(OwedRefundRefunded, &r2), row(OwedRefundRefunded, &r1)}, nil)[order][0]
	if all.RefundStatus != "refunded" || all.RefundedAt == nil || !all.RefundedAt.Equal(r2) ||
		all.RefundedAt.Location() != time.UTC || all.RefundedAtEpoch == nil || *all.RefundedAtEpoch != r2.Unix() {
		t.Fatalf("all refunded: %+v, want refunded at the last refund %v", all, r2.UTC())
	}
	part := buildPaymentNotices([]owedNoticeRow{row(OwedRefundRefunded, &r1), row(OwedRefundPending, nil)}, nil)[order][0]
	if part.RefundStatus != "pending" || part.RefundedAt != nil || part.RefundedAtEpoch != nil {
		t.Fatalf("one of two refunded: %+v, want pending with no refunded_at", part)
	}
	failed := buildPaymentNotices([]owedNoticeRow{row(OwedRefundPending, nil), row(OwedRefundFailed, nil), row(OwedRefundRefunded, &r1)}, nil)[order][0]
	if failed.RefundStatus != "failed" || failed.RefundedAt != nil {
		t.Fatalf("one stalled: %+v, want failed", failed)
	}
}

// Every reason maps to its stable code, and an unknown one is still shown.
func TestNoticeCodeForReason(t *testing.T) {
	for reason, want := range map[string]string{
		OwedReasonDuplicateOrderCapture:  PaymentNoticeDuplicateCapture,
		OwedReasonRepeatCapture:          PaymentNoticeRepeatCapture,
		OwedReasonLateCapture:            PaymentNoticeLateCapture,
		OwedReasonLateCaptureAfterFailed: PaymentNoticeLateCapture,
		OwedReasonCaptureMismatch:        PaymentNoticeMismatchedCapture,
		"something_new":                  PaymentNoticeDuplicateCapture,
	} {
		if got := noticeCodeForReason(reason); got != want {
			t.Errorf("reason %q -> %q, want %q", reason, got, want)
		}
	}
}

// The code list is pinned: clients switch on these strings.
func TestPaymentNoticeCodes_Pinned(t *testing.T) {
	want := "mismatched_capture,late_capture,duplicate_capture,repeat_capture,refund_delayed"
	if got := strings.Join(PaymentNoticeCodes, ","); got != want {
		t.Fatalf("codes = %s, want %s", got, want)
	}
}

// Several kinds on one order: the most urgent first (a stalled refund, then
// the code order), the rest under others.
func TestBuildPaymentNotices_OrderingAndOthers(t *testing.T) {
	order := uuid.New()
	at := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	list := buildPaymentNotices([]owedNoticeRow{
		{OrderID: order, Reason: OwedReasonDuplicateOrderCapture, AmountMinor: 100, Exact: true, RefundStatus: OwedRefundPending, DetectedAt: at},
		{OrderID: order, Reason: OwedReasonLateCapture, AmountMinor: 200, Exact: true, RefundStatus: OwedRefundPending, DetectedAt: at.Add(time.Minute)},
		{OrderID: order, Reason: OwedReasonRepeatCapture, AmountMinor: 300, Exact: true, RefundStatus: OwedRefundFailed, DetectedAt: at.Add(2 * time.Minute)},
	}, []delayedRefundRow{{OrderID: order, AmountMinor: 900, Currency: "INR", DetectedAt: at.Add(3 * time.Minute)}})[order]
	var codes []string
	for _, n := range list {
		codes = append(codes, n.Code+"/"+n.RefundStatus)
	}
	want := "repeat_capture/failed,refund_delayed/failed,late_capture/pending,duplicate_capture/pending"
	if got := strings.Join(codes, ","); got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
	p := primaryNotice(list)
	if p.Code != "repeat_capture" || len(p.Others) != 3 || p.Others[0].Code != "refund_delayed" || p.Others[0].Others != nil {
		t.Fatalf("primary = %+v, want repeat_capture carrying the other three", p)
	}
	if d := list[1]; d.AmountMinor.Int64() != 900 || d.Payments != 1 {
		t.Fatalf("refund_delayed = %+v, want 900 over the order's own payment", d)
	}
	if primaryNotice(nil) != nil {
		t.Fatal("no notices must be no primary")
	}
	if one := primaryNotice(list[:1]); one.Others != nil {
		t.Fatalf("a single notice carries others: %+v", one)
	}
}

// The wire shape, pinned: the original six fields in their order, then the
// additions, each present only when it says something.
func TestPaymentNotice_JSONShape(t *testing.T) {
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	b, err := json.Marshal(PaymentNotice{
		Code: PaymentNoticeDuplicateCapture, AmountMinor: 121810, Currency: "INR",
		Payments: 1, DetectedAt: at, DetectedAtEpoch: at.Unix(), RefundStatus: "pending",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"code":"duplicate_capture","amount_minor":121810,"currency":"INR","payments":1,` +
		`"detected_at":"2026-03-04T05:06:07Z","detected_at_epoch":1772600767,"refund_status":"pending"}`
	if string(b) != want {
		t.Fatalf("payment notice JSON\n got %s\nwant %s", b, want)
	}
	rat := at.Add(time.Hour)
	epoch := rat.Unix()
	b, err = json.Marshal(PaymentNotice{
		Code: PaymentNoticeLateCapture, AmountMinor: 5, Currency: "INR", Payments: 1, DetectedAt: at,
		DetectedAtEpoch: at.Unix(), AmountApproximate: true, RefundStatus: "refunded", RefundedAt: &rat,
		RefundedAtEpoch: &epoch, Others: []PaymentNotice{{Code: PaymentNoticeRefundDelayed, RefundStatus: "failed"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want = `{"code":"late_capture","amount_minor":5,"currency":"INR","payments":1,"detected_at":"2026-03-04T05:06:07Z",` +
		`"detected_at_epoch":1772600767,"amount_approximate":true,"refund_status":"refunded",` +
		`"refunded_at":"2026-03-04T06:06:07Z","refunded_at_epoch":1772604367,` +
		`"others":[{"code":"refund_delayed","amount_minor":0,"currency":"","payments":0,` +
		`"detected_at":"0001-01-01T00:00:00Z","detected_at_epoch":0,"refund_status":"failed"}]}`
	if string(b) != want {
		t.Fatalf("payment notice JSON\n got %s\nwant %s", b, want)
	}
}

// An order card with nothing to say carries no payment_notice key at all, so
// every existing list response is byte-for-byte what it was.
func TestOrderCard_PaymentNoticeOmittedWhenNone(t *testing.T) {
	b, err := json.Marshal(OrderCard{ID: uuid.New(), Status: "confirmed"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "payment_notice") {
		t.Fatalf("card without a notice rendered one: %s", b)
	}
	b, err = json.Marshal(OrderCard{ID: uuid.New(), Status: "confirmed",
		PaymentNotice: &PaymentNotice{Code: PaymentNoticeDuplicateCapture, AmountMinor: 100, Currency: "INR", Payments: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"status":"confirmed","payment_notice":{"code":"duplicate_capture"`) {
		t.Fatalf("card with a notice: %s, want payment_notice right after status", b)
	}
}

// The seller and admin order views are postgres.Order, which has no notice:
// a buyer's double payment is between the buyer and the platform.
func TestOrder_HasNoPaymentNotice(t *testing.T) {
	b, err := json.Marshal(Order{ID: uuid.New(), Status: "confirmed", PaymentStatus: "paid"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "payment_notice") || strings.Contains(string(b), "duplicate_capture") {
		t.Fatalf("seller/admin order row carries the buyer's notice: %s", b)
	}
}
