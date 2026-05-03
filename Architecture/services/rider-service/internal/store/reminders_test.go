package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// helper: make a minimal partner so the FK on rider_doc_reminders_sent is happy.
func makeRiderTestPartner(t *testing.T, st *Store) uuid.UUID {
	t.Helper()
	p, err := st.CreatePartner(context.Background(), CreatePartnerInput{
		UserID:      uuid.New(),
		PartnerType: "individual_driver",
		FullName:    "Test Partner",
		Phone:       "+91" + uuid.New().String()[:10],
	})
	if err != nil {
		t.Fatalf("create partner: %v", err)
	}
	return p.ID
}

// TestReminder_MarkAndDedupe verifies the (document_id, bucket) UNIQUE
// makes the second call a no-op.
func TestReminder_MarkAndDedupe(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	pid := makeRiderTestPartner(t, st)
	docID := uuid.New()
	expiry := time.Now().Add(72 * time.Hour)

	first, err := st.MarkReminderSent(ctx, pid, docID, expiry, "3d")
	if err != nil {
		t.Fatalf("first mark: %v", err)
	}
	if !first {
		t.Errorf("first call should have inserted a row")
	}
	second, err := st.MarkReminderSent(ctx, pid, docID, expiry, "3d")
	if err != nil {
		t.Fatalf("second mark: %v", err)
	}
	if second {
		t.Errorf("second call should have been a no-op (already sent)")
	}
}

// TestReminder_DifferentBuckets verifies the same doc with different
// buckets all succeed (one row per bucket).
func TestReminder_DifferentBuckets(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	pid := makeRiderTestPartner(t, st)
	docID := uuid.New()
	expiry := time.Now().Add(72 * time.Hour)

	for _, bucket := range []string{"30d", "14d", "7d", "3d", "1d"} {
		ok, err := st.MarkReminderSent(ctx, pid, docID, expiry, bucket)
		if err != nil {
			t.Fatalf("mark bucket %s: %v", bucket, err)
		}
		if !ok {
			t.Errorf("bucket %s should have inserted", bucket)
		}
	}
}

// TestReminder_HasReminderSent_RoundTrip cross-checks Mark+Has together.
func TestReminder_HasReminderSent_RoundTrip(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	pid := makeRiderTestPartner(t, st)
	docID := uuid.New()

	if has, _ := st.HasReminderSent(ctx, docID, "7d"); has {
		t.Errorf("nothing inserted yet — HasReminderSent should be false")
	}
	if _, err := st.MarkReminderSent(ctx, pid, docID, time.Now(), "7d"); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if has, _ := st.HasReminderSent(ctx, docID, "7d"); !has {
		t.Errorf("HasReminderSent = false after Mark; should be true")
	}
}

// TestReminder_RequiresIDs verifies the input guards.
func TestReminder_RequiresIDs(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	pid := makeRiderTestPartner(t, st)

	if _, err := st.MarkReminderSent(ctx, uuid.Nil, uuid.New(), time.Now(), "1d"); err == nil {
		t.Errorf("expected error on nil partner id")
	}
	if _, err := st.MarkReminderSent(ctx, pid, uuid.Nil, time.Now(), "1d"); err == nil {
		t.Errorf("expected error on nil document id")
	}
	if _, err := st.MarkReminderSent(ctx, pid, uuid.New(), time.Now(), ""); err == nil {
		t.Errorf("expected error on empty bucket")
	}
}
