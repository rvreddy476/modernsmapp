package service

import (
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
	"testing"
	"time"
)

func visitFixture() (*store.VisitFacts, uuid.UUID) {
	u := uuid.MustParse("10000000-0000-4000-8000-000000000001")
	return &store.VisitFacts{Status: "arrived", Pro: &store.VisitPro{UserID: u, ProID: u, Status: "accepted", AccountStatus: "approved"}, MinBefore: 2, MinAfter: 2, Photos: model.PhotoCounts{Before: 2, After: 2, KitSeal: 1}}, u
}

func TestVisitStartOwnerGuard(t *testing.T) {
	f, u := visitFixture()
	if visitStartGuard(f, u) != nil {
		t.Fatal("owner refused")
	}
	if visitStartGuard(f, uuid.New()) == nil {
		t.Fatal("another pro started")
	}
}
func TestVisitStartPhotoGuard(t *testing.T) {
	f, u := visitFixture()
	f.Photos.Before = 1
	if visitStartGuard(f, u) == nil {
		t.Fatal("missing before photo accepted")
	}
}
func TestVisitSalonKitGuard(t *testing.T) {
	f, u := visitFixture()
	f.Family = "BEAUTY_SALON"
	f.Photos.KitSeal = 0
	if visitStartGuard(f, u) == nil {
		t.Fatal("unsealed salon visit accepted")
	}
}
func TestVisitStatusGuard(t *testing.T) {
	f, u := visitFixture()
	f.Status = "assigned"
	if visitStartGuard(f, u) == nil {
		t.Fatal("start before arrival")
	}
}
func TestVisitSuspensionGuard(t *testing.T) {
	f, u := visitFixture()
	f.Pro.AccountStatus = "suspended"
	if visitStartGuard(f, u) == nil {
		t.Fatal("suspended professional acted")
	}
}
func TestVisitArrivalRadiusGuard(t *testing.T) {
	f, u := visitFixture()
	f.Status = "en_route"
	f.Lat = 17.4
	f.Lng = 78.4
	if visitMoveGuard(f, u, "arrived", &store.Fix{Lat: 17.4, Lng: 78.4}) != nil {
		t.Fatal("same point refused")
	}
	if visitMoveGuard(f, u, "arrived", &store.Fix{Lat: 17.404, Lng: 78.4}) == nil {
		t.Fatal("far arrival accepted")
	}
}
func TestVisitFinishPhotoGuard(t *testing.T) {
	f, u := visitFixture()
	f.Status = "in_progress"
	f.Photos.After = 1
	if visitFinishGuard(f, u) == nil {
		t.Fatal("missing after photo accepted")
	}
}
func TestVisitFinishPendingExtrasGuard(t *testing.T) {
	f, u := visitFixture()
	f.Status = "in_progress"
	f.ProposedExtras = 1
	if visitFinishGuard(f, u) == nil {
		t.Fatal("unanswered extra accepted")
	}
}
func TestVisitCompleteUnpaidGuard(t *testing.T) {
	f, u := visitFixture()
	now := time.Now()
	f.Status = "awaiting_extras_payment"
	f.FinishedAt = &now
	if visitCompleteGuard(f, u) != nil {
		t.Fatal("finished paid visit refused")
	}
	f.UnpaidBills = 1
	if visitCompleteGuard(f, u) == nil {
		t.Fatal("unpaid bill completed")
	}
}
func TestVisitOTPInputGuard(t *testing.T) {
	for _, s := range []string{"123", "12345", "abcd", " 1234", "１２３４"} {
		if _, err := otpCheck(model.OTPInput{OTP: &s}, "start"); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
	s := "0042"
	c, err := otpCheck(model.OTPInput{OTP: &s}, "start")
	if err != nil || c.MaxAttempts != 5 || c.Lock != 15*time.Minute {
		t.Fatal("wrong OTP policy")
	}
}
