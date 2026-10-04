package service

import (
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
	"testing"
	"time"
)

func extraFacts() (*store.VisitFacts, uuid.UUID, store.VisitExtraOption) {
	f, _, p := careFacts()
	f.Status = "in_progress"
	return f, p, store.VisitExtraOption{ExtraOption: model.ExtraOption{Kind: "rate_card", MaxQuantity: 2}}
}
func TestExtraOwnerGuard(t *testing.T) {
	f, _, o := extraFacts()
	if extraGuard(f, uuid.New(), 1, o) == nil {
		t.Fatal("outsider proposed extra")
	}
}
func TestExtraStatusGuard(t *testing.T) {
	f, p, o := extraFacts()
	f.Status = "completed"
	if extraGuard(f, p, 1, o) == nil {
		t.Fatal("extra outside visit")
	}
}
func TestExtraFinishedGuard(t *testing.T) {
	f, p, o := extraFacts()
	at := time.Now()
	f.FinishedAt = &at
	if extraGuard(f, p, 1, o) == nil {
		t.Fatal("extra after finish")
	}
}
func TestExtraSalonGuard(t *testing.T) {
	f, p, o := extraFacts()
	f.Family = "BEAUTY_SALON"
	if extraGuard(f, p, 1, o) == nil {
		t.Fatal("salon rate card bypassed catalogue")
	}
}
func TestExtraQuantityGuard(t *testing.T) {
	f, p, o := extraFacts()
	for _, q := range []int{0, 3} {
		if extraGuard(f, p, q, o) == nil {
			t.Fatal("bad extra quantity")
		}
	}
}
func TestExtraPartsGuard(t *testing.T) {
	f, p, o := extraFacts()
	o.IsPart = true
	if extraGuard(f, p, 1, o) == nil {
		t.Fatal("parts without adviser approval")
	}
}
