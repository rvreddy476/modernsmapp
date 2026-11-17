//go:build integration

package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Lane media-upload (lv-3, lv-16) against live PostgreSQL, CI only, on a
// _test database:
//
//	MEDIA_DURABILITY_POSTGRES_DSN=postgres://…/media_it_test \
//	  go test -tags integration ./internal/store/postgres -run 'UploadQuota|ResumableSliding' -v

func seedSession(t *testing.T, store *MediaAssetStore, m *MediaAsset, exp time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if err := store.CreateResumableUpload(context.Background(), &ResumableUpload{
		UploadID: id, MediaID: m.ID, UploaderID: m.UploaderID, TotalBytes: 10, ChunkSize: 5, TotalParts: 2,
		Status: "initiated", MimeType: "video/mp4", ObjectKey: m.StorageKey, StorageUploadID: "mp-" + id.String(),
		ExpiresAt: exp,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return id
}

func TestLiveResumableSlidingExpiry(t *testing.T) {
	store := New(durabilityPool(t))
	ctx := context.Background()
	m := seedReelAsset(t, store, "pending_upload")

	// Activity an hour before expiry moves it to now + idle.
	id := seedSession(t, store, m, time.Now().Add(time.Hour))
	exp, err := store.TouchResumableUpload(ctx, id, 24*time.Hour, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Until(exp); d < 23*time.Hour+50*time.Minute || d > 24*time.Hour+time.Minute {
		t.Fatalf("expiry %v from now, want about 24h", d)
	}
	// Never past created_at + ceiling.
	exp, err = store.TouchResumableUpload(ctx, id, 24*time.Hour, 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Until(exp); d > 2*time.Hour+time.Minute {
		t.Fatalf("expiry %v from now, past the 2h ceiling", d)
	}
	// Never shortens: a later expiry stays.
	far := seedSession(t, store, m, time.Now().Add(72*time.Hour))
	exp, _ = store.TouchResumableUpload(ctx, far, time.Hour, 7*24*time.Hour)
	if time.Until(exp) < 71*time.Hour {
		t.Fatalf("touch shortened a session to %v", time.Until(exp))
	}
	// An expired or completed session is not revived.
	dead := seedSession(t, store, m, time.Now().Add(-time.Minute))
	if _, err := store.TouchResumableUpload(ctx, dead, 24*time.Hour, 7*24*time.Hour); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expired session touched: %v", err)
	}
	if err := store.CompleteResumableUpload(ctx, far); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TouchResumableUpload(ctx, far, 24*time.Hour, 7*24*time.Hour); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("completed session touched: %v", err)
	}
	// Orphan GC sees the open session's asset as held.
	other := seedReelAsset(t, store, "pending_upload")
	open, err := store.OpenResumableMediaIDs(ctx, []uuid.UUID{m.ID, other.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !open[m.ID] || open[other.ID] {
		t.Fatalf("open sessions %v", open)
	}
}

func TestLiveUploadQuotaLedger(t *testing.T) {
	store := New(durabilityPool(t))
	ctx := context.Background()
	user := uuid.New()
	since := time.Now().Add(-24 * time.Hour)
	limitBytes := int64(100)
	decide := func(u UploadUsage, _ *UploadQuotaOverride) error {
		if u.Bytes+60 > limitBytes {
			return errors.New("over budget")
		}
		return nil
	}
	asset := func() *MediaAsset {
		m := seedReelAsset(t, store, "processing")
		if _, err := store.db.Exec(ctx, `UPDATE media_assets SET uploader_id = $2 WHERE id = $1`, m.ID, user); err != nil {
			t.Fatal(err)
		}
		return m
	}
	a, b := asset(), asset()

	// Two concurrent 60-byte admits under a 100-byte budget: one wins.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, m := range []*MediaAsset{a, b} {
		wg.Add(1)
		go func(i int, m *MediaAsset) {
			defer wg.Done()
			errs[i] = store.AdmitVideoUpload(ctx, VideoUploadCharge{MediaID: m.ID, UploaderID: user, Bytes: 60, DurationMs: 1000, IsLong: true}, since, decide)
		}(i, m)
	}
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("both or neither admitted: %v", errs)
	}
	winner := a
	if errs[0] != nil {
		winner = b
	}
	// A retry of the winner is ErrUploadAlreadyCharged, not a refusal.
	if err := store.AdmitVideoUpload(ctx, VideoUploadCharge{MediaID: winner.ID, UploaderID: user, Bytes: 60, IsLong: true}, since, decide); !errors.Is(err, ErrUploadAlreadyCharged) {
		t.Fatalf("retry: %v", err)
	}
	u, o, err := store.UploadUsageFor(ctx, user, since, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	if u.Bytes != 60 || u.DurationMs != 1000 || u.InflightLong != 1 || o != nil {
		t.Fatalf("usage %+v override %v", u, o)
	}
	// Deleting the video does not refund it; finishing it ends "in flight".
	if _, err := store.db.Exec(ctx, `UPDATE media_assets SET processing_status = 'ready' WHERE id = $1`, winner.ID); err != nil {
		t.Fatal(err)
	}
	u, _, _ = store.UploadUsageFor(ctx, user, since, uuid.Nil)
	if u.Bytes != 60 || u.InflightLong != 0 {
		t.Fatalf("after ready: %+v", u)
	}
	// An override row is read back.
	if _, err := store.db.Exec(ctx, `INSERT INTO media_upload_quota_overrides (user_id, daily_video_bytes, reason, granted_by)
		VALUES ($1, 500, 'test', 'test')`, user); err != nil {
		t.Fatal(err)
	}
	_, o, err = store.UploadUsageFor(ctx, user, since, uuid.Nil)
	if err != nil || o == nil || o.DailyVideoBytes == nil || *o.DailyVideoBytes != 500 || o.DailyVideoSeconds != nil {
		t.Fatalf("override %+v err %v", o, err)
	}
}
