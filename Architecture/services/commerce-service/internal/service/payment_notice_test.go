package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

func dupNotice() *postgres.PaymentNotice {
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	return &postgres.PaymentNotice{
		Code: postgres.PaymentNoticeDuplicateCapture, AmountMinor: 134800, Currency: "INR",
		Payments: 1, DetectedAt: at, DetectedAtEpoch: at.Unix(), RefundStatus: "pending",
	}
}

func TestOrderDetail_NoDuplicate_NoPaymentNotice(t *testing.T) {
	b, err := json.Marshal(OrderDetail{ID: uuid.New(), Status: "confirmed", PaymentStatus: "paid"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "payment_notice") {
		t.Fatalf("order detail without a duplicate rendered a notice: %s", b)
	}
}

func TestOrderDetail_Duplicate_PaymentNoticeAfterStatus(t *testing.T) {
	b, err := json.Marshal(OrderDetail{ID: uuid.New(), Status: "confirmed", PaymentStatus: "paid", PaymentNotice: dupNotice()})
	if err != nil {
		t.Fatal(err)
	}
	want := `"status":"confirmed","payment_notice":{"code":"duplicate_capture","amount_minor":134800,"currency":"INR",` +
		`"payments":1,"detected_at":"2026-03-04T05:06:07Z","detected_at_epoch":1772600767,"refund_status":"pending"},"items"`
	if !strings.Contains(string(b), want) {
		t.Fatalf("order detail JSON %s\nwant it to contain %s", b, want)
	}
}

func TestApplyPaymentNotices_OnlyTheOrdersThatHaveOne(t *testing.T) {
	dup, clean := uuid.New(), uuid.New()
	cards := []postgres.OrderCard{{ID: clean}, {ID: dup}}
	applyPaymentNotices(cards, map[uuid.UUID]*postgres.PaymentNotice{dup: dupNotice()})
	if cards[0].PaymentNotice != nil {
		t.Fatalf("order with no duplicate got a notice: %+v", cards[0].PaymentNotice)
	}
	if n := cards[1].PaymentNotice; n == nil || n.Code != "duplicate_capture" || n.AmountMinor.Int64() != 134800 {
		t.Fatalf("order with a duplicate: %+v, want the duplicate_capture notice", n)
	}
	// A failed lookup (nil map) leaves every card without one.
	applyPaymentNotices(cards, nil)
	if cards[1].PaymentNotice != nil {
		t.Fatal("nil notices map left a notice behind")
	}
}

// The seller's order views (fulfilment list and order detail) are built on
// postgres.Order and never carry the buyer's notice.
func TestSellerOrderCard_HasNoPaymentNotice(t *testing.T) {
	b, err := json.Marshal(SellerOrderCard{Order: &postgres.Order{ID: uuid.New(), Status: "confirmed", PaymentStatus: "paid"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "payment_notice") {
		t.Fatalf("seller order card carries the buyer's payment notice: %s", b)
	}
}
