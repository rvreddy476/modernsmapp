package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestAudit_RecordAndList — round-trip through RecordAudit + ListAuditLogs.
func TestAudit_RecordAndList(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	admin := uuid.New()
	target := uuid.New()
	path := "/v1/rider/admin/partners/" + target.String() + "/approve"
	method := "POST"
	body := `{"reason":"verified"}`
	status := 200
	latency := 12

	rec, err := s.RecordAudit(ctx, RecordAuditInput{
		AdminUserID:    admin,
		Action:         "partner.approve",
		EntityType:     "partner",
		EntityID:       &target,
		RequestPath:    &path,
		RequestMethod:  &method,
		RequestBody:    &body,
		ResponseStatus: &status,
		LatencyMS:      &latency,
	})
	if err != nil {
		t.Fatalf("RecordAudit: %v", err)
	}
	if rec.AdminUserID != admin {
		t.Errorf("admin id mismatch: got %s want %s", rec.AdminUserID, admin)
	}
	if rec.Action != "partner.approve" {
		t.Errorf("action mismatch: %s", rec.Action)
	}

	rows, err := s.ListAuditLogs(ctx, AuditFilter{Actor: &admin, Limit: 10})
	if err != nil {
		t.Fatalf("ListAuditLogs: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected at least one audit row")
	}
	found := false
	for _, r := range rows {
		if r.ID == rec.ID {
			found = true
			if r.RequestPath == nil || *r.RequestPath != path {
				t.Errorf("path round-trip failed")
			}
		}
	}
	if !found {
		t.Errorf("audit row %s not found in actor list", rec.ID)
	}
}

// TestAudit_FilterByAction — action-keyed lookup returns only matching rows.
func TestAudit_FilterByAction(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	admin := uuid.New()
	for i, action := range []string{"partner.approve", "document.verify", "partner.suspend"} {
		_, err := s.RecordAudit(ctx, RecordAuditInput{
			AdminUserID: admin,
			Action:      action,
			EntityType:  "partner",
		})
		if err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	rows, err := s.ListAuditLogs(ctx, AuditFilter{Actor: &admin, Action: "partner.approve", Limit: 50})
	if err != nil {
		t.Fatalf("ListAuditLogs: %v", err)
	}
	for _, r := range rows {
		if r.Action != "partner.approve" {
			t.Errorf("expected only partner.approve rows, got %s", r.Action)
		}
	}
	if len(rows) < 1 {
		t.Errorf("expected at least 1 partner.approve row, got %d", len(rows))
	}
}

// TestAudit_FilterBySince — since cutoff drops earlier rows.
func TestAudit_FilterBySince(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	admin := uuid.New()
	_, _ = s.RecordAudit(ctx, RecordAuditInput{
		AdminUserID: admin, Action: "x.first", EntityType: "x",
	})
	cut := time.Now().Add(-1 * time.Millisecond)
	_, _ = s.RecordAudit(ctx, RecordAuditInput{
		AdminUserID: admin, Action: "x.second", EntityType: "x",
	})
	rows, err := s.ListAuditLogs(ctx, AuditFilter{Actor: &admin, Since: &cut, Limit: 50})
	if err != nil {
		t.Fatalf("ListAuditLogs: %v", err)
	}
	if len(rows) < 1 {
		t.Errorf("expected at least 1 row after cutoff, got %d", len(rows))
	}
}

// TestAudit_RejectsMissingFields — admin id + action + entity type required.
func TestAudit_RejectsMissingFields(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := s.RecordAudit(ctx, RecordAuditInput{}); err == nil {
		t.Error("expected error for empty input")
	}
	if _, err := s.RecordAudit(ctx, RecordAuditInput{AdminUserID: uuid.New()}); err == nil {
		t.Error("expected error for missing action")
	}
	if _, err := s.RecordAudit(ctx, RecordAuditInput{AdminUserID: uuid.New(), Action: "a"}); err == nil {
		t.Error("expected error for missing entity_type")
	}
}
