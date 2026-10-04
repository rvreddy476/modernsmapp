package service

import (
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
	"testing"
	"time"
)

func careFacts() (*store.VisitFacts, uuid.UUID, uuid.UUID) {
	c, p := uuid.New(), uuid.New()
	return &store.VisitFacts{Customer: c, Status: "assigned", Pro: &store.VisitPro{ProID: uuid.New(), UserID: p, Status: "accepted", AccountStatus: "approved"}}, c, p
}
func TestRatingOwnerGuard(t *testing.T) {
	f, _, _ := careFacts()
	f.Status = "completed"
	if ratingGuard(f, uuid.New(), "customer") == nil {
		t.Fatal("outsider rated a visit")
	}
}
func TestRatingCompletedGuard(t *testing.T) {
	f, c, _ := careFacts()
	if ratingGuard(f, c, "customer") == nil {
		t.Fatal("unfinished visit rated")
	}
}
func TestChatWindowGuard(t *testing.T) {
	f, c, _ := careFacts()
	at := time.Now()
	f.Status = "completed"
	completed := at.Add(-2 * time.Hour)
	f.CompletedAt = &completed
	if chatWriteGuard(f, c, "customer", at) == nil {
		t.Fatal("expired chat accepted message")
	}
}
func TestChatOwnerGuard(t *testing.T) {
	f, _, _ := careFacts()
	if chatWriteGuard(f, uuid.New(), "customer", time.Now()) == nil {
		t.Fatal("outsider sent message")
	}
}
func TestSafetyOwnerGuard(t *testing.T) {
	f, _, _ := careFacts()
	if safetyGuard(f, uuid.New(), "pro") == nil {
		t.Fatal("outsider raised incident")
	}
}
func TestSafetyStatusGuard(t *testing.T) {
	f, c, _ := careFacts()
	f.Status = "cancelled"
	if safetyGuard(f, c, "customer") == nil {
		t.Fatal("cancelled visit safety mutation")
	}
}
func TestReworkOwnerGuard(t *testing.T) {
	f, _, _ := careFacts()
	f.Status = "completed"
	at := time.Now()
	f.CompletedAt = &at
	f.ReworkDays = 7
	if reworkGuard(f, uuid.New(), at) == nil {
		t.Fatal("outsider rework")
	}
}
func TestReworkWindowGuard(t *testing.T) {
	f, c, _ := careFacts()
	f.Status = "completed"
	at := time.Now()
	completed := at.Add(-8 * 24 * time.Hour)
	f.CompletedAt = &completed
	f.ReworkDays = 7
	if reworkGuard(f, c, at) == nil {
		t.Fatal("expired rework")
	}
}
func TestReworkChildGuard(t *testing.T) {
	f, c, _ := careFacts()
	f.Status = "completed"
	at := time.Now()
	f.CompletedAt = &at
	f.ReworkDays = 7
	parent := uuid.New()
	f.ParentBookingID = &parent
	if reworkGuard(f, c, at) == nil {
		t.Fatal("recursive rework")
	}
}
