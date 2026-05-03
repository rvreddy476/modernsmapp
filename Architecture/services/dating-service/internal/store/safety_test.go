// Safety store tests. Skipped unless TEST_PG_DSN is set.
package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func safetyTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping safety store tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return New(pool), func() { pool.Close() }
}

func TestRecordSafetyEvent_PersistsBeforeReturn(t *testing.T) {
	s, cleanup := safetyTestStore(t)
	defer cleanup()
	uid := uuid.New()
	ensureProfileForTest(t, s, uid)
	if err := s.RecordSafetyEvent(context.Background(), uid, "panic", map[string]any{"latitude": 12.97, "longitude": 77.59}); err != nil {
		t.Fatalf("record: %v", err)
	}
}

func TestRecordSafetyEvent_RejectsEmptyKind(t *testing.T) {
	s, cleanup := safetyTestStore(t)
	defer cleanup()
	if err := s.RecordSafetyEvent(context.Background(), uuid.New(), "", nil); err == nil {
		t.Fatalf("expected error for empty kind")
	}
}

func TestScheduleAndCheckInMeet(t *testing.T) {
	s, cleanup := safetyTestStore(t)
	defer cleanup()
	a, b := uuid.New(), uuid.New()
	ensureProfileForTest(t, s, a)
	ensureProfileForTest(t, s, b)
	id, err := s.ScheduleMeet(context.Background(), a, b, time.Now().Add(2*time.Hour), 12.97, 77.59, "Indiranagar")
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if err := s.MeetCheckIn(context.Background(), id, a, "safe"); err != nil {
		t.Fatalf("checkin: %v", err)
	}
	m, err := s.GetMeet(context.Background(), id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if m.CheckInStatus == nil || *m.CheckInStatus != "safe" {
		t.Fatalf("expected safe checkin, got %+v", m.CheckInStatus)
	}
}

func TestMeetCheckIn_RejectsBogusStatus(t *testing.T) {
	s, cleanup := safetyTestStore(t)
	defer cleanup()
	if err := s.MeetCheckIn(context.Background(), uuid.New(), uuid.New(), "maybe"); err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestBlockUser_IdempotentAndRejectsSelf(t *testing.T) {
	s, cleanup := safetyTestStore(t)
	defer cleanup()
	a, b := uuid.New(), uuid.New()
	ensureProfileForTest(t, s, a)
	ensureProfileForTest(t, s, b)
	if err := s.BlockUser(context.Background(), a, a); err == nil {
		t.Fatalf("expected error for self-block")
	}
	if err := s.BlockUser(context.Background(), a, b); err != nil {
		t.Fatalf("first block: %v", err)
	}
	if err := s.BlockUser(context.Background(), a, b); err != nil {
		t.Fatalf("repeat block: %v", err)
	}
}

func TestCreateReport_PersistsRow(t *testing.T) {
	s, cleanup := safetyTestStore(t)
	defer cleanup()
	a, b := uuid.New(), uuid.New()
	ensureProfileForTest(t, s, a)
	ensureProfileForTest(t, s, b)
	r, err := s.CreateReport(context.Background(), a, b, "harassment", "creepy DM")
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if r.ID == uuid.Nil {
		t.Fatalf("expected id")
	}
}

func TestCreateLiveLocationShare(t *testing.T) {
	s, cleanup := safetyTestStore(t)
	defer cleanup()
	a, b := uuid.New(), uuid.New()
	ensureProfileForTest(t, s, a)
	ensureProfileForTest(t, s, b)
	share, err := s.CreateLiveLocationShare(context.Background(), a, b, 30)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if share.ShareID == uuid.Nil {
		t.Fatalf("expected share id")
	}
	if share.ExpiresAt.Before(time.Now()) {
		t.Fatalf("expected expires in future")
	}
}
