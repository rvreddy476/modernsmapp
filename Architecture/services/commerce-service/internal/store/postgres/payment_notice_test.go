package postgres

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBuildPaymentNotices_NoDuplicateIsNoNotice(t *testing.T) {
	if got := buildPaymentNotices(nil); len(got) != 0 {
		t.Fatalf("no duplicate rows: %v, want no notices", got)
	}
	// A row stating no intent is never a duplicate (differentIntent).
	order := uuid.New()
	got := buildPaymentNotices([]duplicateCaptureRow{{OrderID: order, IntentID: "  ", AmountMinor: 100, ProcessedAt: time.Now()}})
	if got[order] != nil {
		t.Fatalf("blank-intent row produced a notice: %+v", got[order])
	}
}

func TestBuildPaymentNotices_DuplicateCarriesAmountCurrencyAndFirstDetection(t *testing.T) {
	order, other := uuid.New(), uuid.New()
	first := time.Date(2026, 3, 4, 5, 6, 7, 0, time.FixedZone("IST", 5*3600+1800))
	later := first.Add(time.Hour)
	dupA := uuid.NewString()
	dupB := uuid.NewString()

	got := buildPaymentNotices([]duplicateCaptureRow{
		// The same duplicate announced twice (two event ids, intent case and
		// spacing differ): one payment, counted once, detected at the first.
		{OrderID: order, IntentID: strings.ToUpper(dupA), AmountMinor: 134800, ProcessedAt: later, Currency: "INR"},
		{OrderID: order, IntentID: " " + dupA + " ", AmountMinor: 134800, ProcessedAt: first, Currency: "INR"},
		// A second, different extra payment.
		{OrderID: order, IntentID: dupB, AmountMinor: 134800, ProcessedAt: later.Add(time.Hour), Currency: "INR"},
		// Another order, no currency on the row.
		{OrderID: other, IntentID: dupA, AmountMinor: 5000, ProcessedAt: first},
	})
	n := got[order]
	if n == nil {
		t.Fatal("duplicate capture produced no notice")
	}
	if n.Code != "duplicate_capture" || n.AmountMinor.Int64() != 269600 || n.Currency != "INR" || n.Payments != 2 {
		t.Fatalf("notice = %+v, want duplicate_capture, 269600 INR over 2 payments", n)
	}
	if !n.DetectedAt.Equal(first) || n.DetectedAt.Location() != time.UTC || n.DetectedAtEpoch != first.Unix() {
		t.Fatalf("detected = %v (%d), want the first detection %v in UTC", n.DetectedAt, n.DetectedAtEpoch, first.UTC())
	}
	if o := got[other]; o == nil || o.AmountMinor.Int64() != 5000 || o.Currency != "INR" || o.Payments != 1 {
		t.Fatalf("other order's notice = %+v, want 5000 INR (currency defaulted), 1 payment", o)
	}
}

// The wire shape, pinned: clients switch on code and read the rest.
func TestPaymentNotice_JSONShape(t *testing.T) {
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	b, err := json.Marshal(PaymentNotice{
		Code: PaymentNoticeDuplicateCapture, AmountMinor: 121810, Currency: "INR",
		Payments: 1, DetectedAt: at, DetectedAtEpoch: at.Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"code":"duplicate_capture","amount_minor":121810,"currency":"INR","payments":1,` +
		`"detected_at":"2026-03-04T05:06:07Z","detected_at_epoch":1772600767}`
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
