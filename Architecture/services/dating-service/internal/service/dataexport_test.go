// Data export service tests — Sprint 5. DPDP §15.8.
package service

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// stubPublisher records calls without hitting Kafka.
type stubPublisher struct {
	requestedCalls int
	readyCalls     int
	lastReadyURL   string
}

func (s *stubPublisher) PublishDataExportRequested(ctx context.Context, exportID, userID uuid.UUID) error {
	s.requestedCalls++
	return nil
}

func (s *stubPublisher) PublishDataExportReady(ctx context.Context, exportID, userID uuid.UUID, downloadURL string, downloadExpires time.Time) error {
	s.readyCalls++
	s.lastReadyURL = downloadURL
	return nil
}

// stubStorage writes to memory and returns a fake URL.
type stubStorage struct {
	written map[uuid.UUID][]byte
	url     string
	expires time.Time
}

func newStubStorage() *stubStorage {
	return &stubStorage{
		written: map[uuid.UUID][]byte{},
		url:     "https://media.example/exports/test-blob",
		expires: time.Now().Add(7 * 24 * time.Hour),
	}
}

func (s *stubStorage) WriteExport(ctx context.Context, exportID uuid.UUID, payload []byte) (string, time.Time, error) {
	s.written[exportID] = payload
	return s.url, s.expires, nil
}

// stubNotifier records but does not deliver.
type stubNotifier struct {
	count int
}

func (s *stubNotifier) NotifyDataExportReady(ctx context.Context, userID uuid.UUID, url string, expiresAt time.Time) error {
	s.count++
	return nil
}

func newDataExportSvcForTest(t *testing.T) (*Service, *store.Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping data export service tests")
	}
	cfg, _ := pgxpool.ParseConfig(dsn)
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	st := store.New(pool)
	_ = st.SeedPremiumPlans(context.Background())
	return New(st, nil), st, func() { pool.Close() }
}

func TestDataExport_RequestExport_HappyPath(t *testing.T) {
	svc, _, cleanup := newDataExportSvcForTest(t)
	defer cleanup()
	pub := &stubPublisher{}
	svc.SetDataExportPublisher(pub)
	user := uuid.New()
	out, err := svc.RequestDataExport(context.Background(), user)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if out.Status != "pending" {
		t.Fatalf("expected pending, got %s", out.Status)
	}
	if pub.requestedCalls != 1 {
		t.Fatalf("expected 1 publish, got %d", pub.requestedCalls)
	}
}

func TestDataExport_RequestExport_RateLimited(t *testing.T) {
	svc, st, cleanup := newDataExportSvcForTest(t)
	defer cleanup()
	user := uuid.New()
	first, err := svc.RequestDataExport(context.Background(), user)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	// First is pending → second returns the same row, no error.
	second, err := svc.RequestDataExport(context.Background(), user)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("pending request should be returned again; got %s vs %s", first.ID, second.ID)
	}

	// If we age the request to ready and within 7 days, third must fail.
	if err := st.CompleteDataExport(context.Background(), first.ID, "https://x", time.Now().Add(7*24*time.Hour)); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := svc.RequestDataExport(context.Background(), user); err == nil {
		t.Fatalf("expected rate-limit error within 7 days")
	}
}

func TestDataExport_BuildExportPayload_NoAadhaarNumber(t *testing.T) {
	svc, st, cleanup := newDataExportSvcForTest(t)
	defer cleanup()
	user := uuid.New()

	intent := "casual"
	if _, err := st.UpsertProfile(context.Background(), user, store.UpsertProfileParams{Intent: &intent}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	if err := st.RecordAadhaarVerification(context.Background(), user, "ref_xyz_123", "doc_hash_abc"); err != nil {
		t.Fatalf("seed aadhaar: %v", err)
	}

	payload, err := svc.BuildExportPayload(context.Background(), user)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Decode + assert.
	var decoded UserDataExport
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.UserID != user {
		t.Fatalf("user id mismatch")
	}
	if decoded.Verification == nil {
		t.Fatalf("verification should be present")
	}
	if decoded.Verification.DigilockerRef == nil || *decoded.Verification.DigilockerRef != "ref_xyz_123" {
		t.Fatalf("digilocker_ref missing or wrong: %+v", decoded.Verification)
	}

	// CRITICAL DPDP audit: the payload must NEVER include the literal
	// string "aadhaar_number", and must not include any 12-digit Aadhaar.
	raw := string(payload)
	if strings.Contains(raw, "aadhaar_number") {
		t.Fatalf("DPDP violation: aadhaar_number key present in export payload")
	}
}

func TestDataExport_BuildExportPayload_OtherPartyOnlyByID(t *testing.T) {
	svc, st, cleanup := newDataExportSvcForTest(t)
	defer cleanup()
	a, b := uuid.New(), uuid.New()
	intent := "casual"
	_, _ = st.UpsertProfile(context.Background(), a, store.UpsertProfileParams{Intent: &intent})
	_, _ = st.UpsertProfile(context.Background(), b, store.UpsertProfileParams{Intent: &intent})
	if _, err := st.CreateSpark(context.Background(), a, b, "photo", "0", "hi"); err != nil {
		t.Fatalf("spark: %v", err)
	}

	payload, err := svc.BuildExportPayload(context.Background(), a)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var decoded UserDataExport
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.SparksSent) == 0 {
		t.Fatalf("expected at least one spark in export")
	}
	// The other party must appear ONLY as a user id, not as a profile.
	raw := string(payload)
	if !strings.Contains(raw, b.String()) {
		t.Fatalf("expected other party id in export")
	}
}

func TestDataExport_FulfillExport(t *testing.T) {
	svc, _, cleanup := newDataExportSvcForTest(t)
	defer cleanup()
	pub := &stubPublisher{}
	stor := newStubStorage()
	notif := &stubNotifier{}
	svc.SetDataExportPublisher(pub)
	svc.SetExportStorageClient(stor)
	svc.SetNotificationClient(notif)

	user := uuid.New()
	out, err := svc.RequestDataExport(context.Background(), user)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if err := svc.FulfillExport(context.Background(), out.ID); err != nil {
		t.Fatalf("fulfill: %v", err)
	}
	if pub.readyCalls != 1 {
		t.Fatalf("expected 1 ready publish, got %d", pub.readyCalls)
	}
	if notif.count != 1 {
		t.Fatalf("expected 1 notification, got %d", notif.count)
	}
	if _, ok := stor.written[out.ID]; !ok {
		t.Fatalf("blob not written")
	}
}

func TestDataExport_PurgeProfile_Anonymisation(t *testing.T) {
	svc, st, cleanup := newDataExportSvcForTest(t)
	defer cleanup()

	a, b := uuid.New(), uuid.New()
	intent := "casual"
	_, _ = st.UpsertProfile(context.Background(), a, store.UpsertProfileParams{Intent: &intent})
	_, _ = st.UpsertProfile(context.Background(), b, store.UpsertProfileParams{Intent: &intent})

	// seed a vouch from a -> b.
	if _, err := st.CreateVouchRequest(context.Background(), a, b, "friend", nil, "love this person"); err != nil {
		t.Fatalf("vouch: %v", err)
	}

	// 30-day grace would have already passed if deleted_at is set in the past.
	if err := st.SoftDeleteProfile(context.Background(), a); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if err := svc.PurgeProfile(context.Background(), a); err != nil {
		t.Fatalf("purge: %v", err)
	}

	// Verify a's profile is gone and the vouch was revoked.
	if _, err := st.GetProfile(context.Background(), a); err == nil {
		t.Fatalf("profile must be deleted")
	}
	vouches, err := st.ListVouchesFor(context.Background(), b, "")
	if err != nil {
		t.Fatalf("list vouches: %v", err)
	}
	for _, v := range vouches {
		if v.VoucherID == a && v.Status != "revoked" {
			t.Fatalf("vouch must be revoked after purge, got %s", v.Status)
		}
	}
}
