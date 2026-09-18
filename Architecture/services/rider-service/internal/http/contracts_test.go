// Contract fixtures for the Android lane (testdata/contracts/*.json): the
// exact bytes the handlers write for an estimate with a peak window and a
// coupon, a receipt with a waiting charge, and a coupon validation error.
//
// Each fixture goes through the same writer the handler uses
// (api.JSONWithContext / api.ErrorWithContext, request id pinned to
// "fixture"), so the envelope, field names, order and number formatting are
// the handler's. The coupon error is a full handler round trip. Regenerate
// with UPDATE_CONTRACT_FIXTURES=1 after an intentional shape change, and
// tell the Android lane.
package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atpost/rider-service/internal/payments"
	"github.com/atpost/rider-service/internal/pricing"
	"github.com/atpost/rider-service/internal/service"
	"github.com/atpost/rider-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/o11y/trace"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const fixtureRequestID = "fixture"

func fixtureCtx() context.Context { return trace.WithRequestID(context.Background(), fixtureRequestID) }

func assertFixture(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "contracts", name)
	if os.Getenv("UPDATE_CONTRACT_FIXTURES") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v (run with UPDATE_CONTRACT_FIXTURES=1 to create it)", name, err)
	}
	if !bytes.Equal(bytes.TrimRight(want, "\r\n"), bytes.TrimRight(got, "\r\n")) {
		t.Fatalf("%s changed.\nwant: %s\ngot:  %s", name, want, got)
	}
}

var fixtureQuoteBreakdown = pricing.Breakdown{
	BasePaise: 2500, DistancePaise: 7777, TimePaise: 0, SurgePaise: 2569, MinimumTopUpPaise: 0,
	RideFarePaise: 12846, DiscountPaise: 2000, CouponCode: "MOPEDU20", CouponID: "0550dbb6-446f-4111-8fe9-bf2851ec29fa",
	TaxableRideFarePaise: 10846, PlatformFeePaise: 500, WaitingMinutes: 0, WaitingChargePaise: 0,
	OutstandingPaise: 0, TollPaise: 0, TaxPaise: 632,
	TaxLines: []pricing.TaxLineResult{
		{Ref: "ride_fare", Category: "PASSENGER_TRANSPORT_VIA_ECO", SAC: "996412", RateBPS: 500, TaxablePaise: 10846, TaxPaise: 542, GrossPaise: 11388},
		{Ref: "platform_fee", Category: "PLATFORM_FEE", SAC: "998599", RateBPS: 1800, TaxablePaise: 500, TaxPaise: 90, GrossPaise: 590},
	},
	TaxNote: pricing.TaxNote, SurgeBasisPoints: 2500, SurgeReason: pricing.SurgePeakHours, WindowName: "Morning peak",
	DistanceMeters: 6481, DurationSeconds: 1060, TotalPaise: 11978, FarePolicyVersion: pricing.FarePolicyVersion,
}

func TestContract_EstimateWithPeakWindowAndCoupon(t *testing.T) {
	out := service.FareEstimateResult{
		QuoteID: "7f6d3b1e-9c2a-4e0f-8b5d-2a1c3e4f5a6b", EstimatedDistanceKM: 6.48, EstimatedDurationMin: 17.67,
		FareEstimatePaise: 11978, SurgeMultiplier: 1.25, SurgeBPS: 2500, SurgeReason: pricing.SurgePeakHours, WindowName: "Morning peak",
		DiscountPaise: 2000, CouponCode: "MOPEDU20", OutstandingPaise: 0, TaxNote: pricing.TaxNote,
		VehicleType: "auto", ETAToPickupSeconds: 300,
		BaseFareINR: 25, PerKMINR: 12, PerMinuteINR: 0, MinimumFareINR: 40, FareEstimateINR: 119.78,
		Options: []store.QuoteOption{{
			VehicleType: "auto", Available: true, PickupETASeconds: 300, DistanceMeters: 6481, DurationSeconds: 1060, Currency: "INR",
			TotalPaise: 11978, SurgeBPS: 2500, SurgeReason: pricing.SurgePeakHours, WindowName: "Morning peak",
			DiscountPaise: 2000, CouponCode: "MOPEDU20", Breakdown: fixtureQuoteBreakdown,
		}},
		ExpiresAt: time.Date(2026, 9, 18, 3, 35, 0, 0, time.UTC),
	}
	w := httptest.NewRecorder()
	api.JSONWithContext(fixtureCtx(), w, http.StatusOK, out)
	assertFixture(t, "estimate_peak_window_coupon.json", w.Body.Bytes())
}

func TestContract_ReceiptWithWaitingCharge(t *testing.T) {
	final := fixtureQuoteBreakdown
	final.WaitingMinutes, final.WaitingChargePaise = 3, 450
	final.TaxLines = append(final.TaxLines, pricing.TaxLineResult{Ref: "waiting_charge", Category: "PASSENGER_TRANSPORT_VIA_ECO", SAC: "996412", RateBPS: 500, TaxablePaise: 450, TaxPaise: 23, GrossPaise: 473})
	final.TaxPaise, final.TotalPaise = 655, 12451
	partner := uuid.MustParse("3c9f1a2b-4d5e-4f60-8a71-92b3c4d5e6f7")
	reported, reportedMin, tracked := 250.0, 600.0, 6702
	completed := time.Date(2026, 9, 18, 4, 2, 11, 0, time.UTC)
	rc := store.RideReceipt{
		RideID: uuid.MustParse("9a8b7c6d-5e4f-4a3b-9c2d-1e0f9a8b7c6d"), CustomerUserID: uuid.MustParse("2d598287-eee7-40b4-a7f5-b46b9412e4e7"),
		PartnerID: &partner, VehicleType: "auto", Status: "completed", PickupAddress: "MG Road, Bengaluru", DropAddress: "Koramangala 5th Block",
		DistanceMeters: 6481, DurationSeconds: 1060, TotalPaise: 12451, SurgeBPS: 2500, SurgeReason: pricing.SurgePeakHours,
		DiscountPaise: 2000, CouponCode: "MOPEDU20", WaitingChargePaise: 450, OutstandingPaise: 0, CancellationFeePaise: 0,
		TaxPaise: 655, TaxNote: pricing.TaxNote, PaymentMethod: "cash", PaymentStatus: "pending_cash_confirmation",
		Payment: &store.ReceiptPayment{Method: "cash", Status: "cash_pending", AmountPaise: 12451, RefundedPaise: 0},
		Refunds: []store.ReceiptRefund{},
		ReportedDistanceKM: &reported, ReportedDurationMin: &reportedMin, TrackedDistanceM: &tracked,
		CompletedAt: &completed, CreatedAt: time.Date(2026, 9, 18, 3, 31, 0, 0, time.UTC),
	}
	raw, err := json.Marshal(final)
	if err != nil {
		t.Fatal(err)
	}
	rc.FareBreakdown = raw
	w := httptest.NewRecorder()
	api.JSONWithContext(fixtureCtx(), w, http.StatusOK, rc)
	assertFixture(t, "receipt_waiting_charge.json", w.Body.Bytes())
}

// --- Payments lane ------------------------------------------------------------

var (
	fixtureIntentID = uuid.MustParse("6f0e2c9a-1b3d-4e5f-8a7b-9c0d1e2f3a4b")
	fixtureRideID   = uuid.MustParse("9a8b7c6d-5e4f-4a3b-9c2d-1e0f9a8b7c6d")
	fixturePaidAt   = time.Date(2026, 9, 18, 4, 5, 30, 0, time.UTC)
)

// The intent response: what the app opens Razorpay Checkout with. The
// client_session carries the PUBLISHABLE key id only.
func TestContract_PaymentIntent(t *testing.T) {
	out := service.RidePaymentIntent{
		IntentID: fixtureIntentID, AmountPaise: 12451, Currency: "INR",
		ClientSession: &payments.ClientSession{Provider: "razorpay", OrderID: "order_R1x2y3z4", KeyID: "rzp_test_publishable", MerchantDisplayName: "Mopedu"},
		Status:        "pending",
	}
	w := httptest.NewRecorder()
	api.JSONWithContext(fixtureCtx(), w, http.StatusOK, out)
	assertFixture(t, "payment_intent.json", w.Body.Bytes())
}

// GET /rides/:id/payment after the signed capture was applied.
func TestContract_PaymentStatusPaid(t *testing.T) {
	intent := fixtureIntentID
	out := service.RidePaymentStatus{Method: "upi", Status: "paid", AmountPaise: 12451, RefundedPaise: 0, IntentID: &intent, UpdatedAt: fixturePaidAt}
	w := httptest.NewRecorder()
	api.JSONWithContext(fixtureCtx(), w, http.StatusOK, out)
	assertFixture(t, "payment_status_paid.json", w.Body.Bytes())
}

// The receipt of an online payment after one partial refund.
func TestContract_ReceiptWithPartialRefund(t *testing.T) {
	partner := uuid.MustParse("3c9f1a2b-4d5e-4f60-8a71-92b3c4d5e6f7")
	completed := time.Date(2026, 9, 18, 4, 2, 11, 0, time.UTC)
	rc := store.RideReceipt{
		RideID: fixtureRideID, CustomerUserID: uuid.MustParse("2d598287-eee7-40b4-a7f5-b46b9412e4e7"),
		PartnerID: &partner, VehicleType: "auto", Status: "completed", PickupAddress: "MG Road, Bengaluru", DropAddress: "Koramangala 5th Block",
		DistanceMeters: 6481, DurationSeconds: 1060, TotalPaise: 12451, SurgeBPS: 2500, SurgeReason: pricing.SurgePeakHours,
		DiscountPaise: 2000, CouponCode: "MOPEDU20", WaitingChargePaise: 450, OutstandingPaise: 0, CancellationFeePaise: 0,
		TaxPaise: 655, TaxNote: pricing.TaxNote, PaymentMethod: "upi", PaymentStatus: "partially_refunded",
		Payment: &store.ReceiptPayment{Method: "upi", Status: "partially_refunded", AmountPaise: 12451, RefundedPaise: 5000},
		Refunds: []store.ReceiptRefund{{
			ID: uuid.MustParse("8d1c2b3a-4f5e-4a6b-9c8d-7e6f5a4b3c2d"), AmountPaise: 5000, Status: "refunded",
			Reason: "captain ended the ride early", CreatedAt: time.Date(2026, 9, 18, 6, 10, 0, 0, time.UTC),
		}},
		CompletedAt: &completed, CreatedAt: time.Date(2026, 9, 18, 3, 31, 0, 0, time.UTC),
	}
	final := fixtureQuoteBreakdown
	final.WaitingMinutes, final.WaitingChargePaise = 3, 450
	final.TaxLines = append(final.TaxLines, pricing.TaxLineResult{Ref: "waiting_charge", Category: "PASSENGER_TRANSPORT_VIA_ECO", SAC: "996412", RateBPS: 500, TaxablePaise: 450, TaxPaise: 23, GrossPaise: 473})
	final.TaxPaise, final.TotalPaise = 655, 12451
	raw, err := json.Marshal(final)
	if err != nil {
		t.Fatal(err)
	}
	rc.FareBreakdown = raw
	w := httptest.NewRecorder()
	api.JSONWithContext(fixtureCtx(), w, http.StatusOK, rc)
	assertFixture(t, "receipt_partial_refund.json", w.Body.Bytes())
}

// One row of GET /v1/rider/internal/admin/refunds, in the list shape.
func TestContract_RefundListRow(t *testing.T) {
	providerRef := "cmd_7b2e4d6f"
	rows := []store.RideRefund{{
		ID: uuid.MustParse("8d1c2b3a-4f5e-4a6b-9c8d-7e6f5a4b3c2d"), RideID: fixtureRideID,
		PaymentID: ptrUUID(uuid.MustParse("5e4d3c2b-1a0f-4e9d-8c7b-6a5f4e3d2c1b")), IntentID: fixtureIntentID, RuleCode: "discretionary",
		AmountPaise: 5000, Reason: "captain ended the ride early", Status: "refunded",
		RequestedBy: uuid.MustParse("0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"), ProviderReference: &providerRef,
		CreatedAt: time.Date(2026, 9, 18, 6, 10, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 9, 18, 6, 12, 45, 0, time.UTC),
	}}
	w := httptest.NewRecorder()
	api.JSONWithContext(fixtureCtx(), w, http.StatusOK, gin.H{"items": rows})
	assertFixture(t, "refund_list_row.json", w.Body.Bytes())
}

// The coupon validation error is a real handler round trip: with coupons
// switched off the service refuses before touching the store, so the route
// runs end to end without a database.
func TestContract_CouponValidateError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(trace.WithRequestID(c.Request.Context(), fixtureRequestID))
		c.Next()
	})
	svc := service.New(store.New(nil), nil, service.Config{CouponsEnabled: false, CouponsFlagSet: true})
	New(svc, "").RegisterRoutes(r)
	req := httptest.NewRequest(http.MethodGet, "/v1/rider/coupons/validate?code=MOPEDU20", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	assertFixture(t, "coupon_validate_error.json", w.Body.Bytes())
}

// ptrUUID is a pointer to a fixture id.
func ptrUUID(id uuid.UUID) *uuid.UUID { return &id }
