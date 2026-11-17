package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// reels-13: behaviour tests for the server half of resumable upload (part,
// status, complete, expiry) over in-memory stores that keep the SQL's rules
// (store/postgres/resumable.go, upload_quota.go). The SQL itself is covered
// by resumable_sliding_integration_test.go in CI.

type fakeResumableDB struct {
	mu       sync.Mutex
	now      func() time.Time
	sessions map[uuid.UUID]*postgres.ResumableUpload
	parts    map[uuid.UUID]map[int]postgres.UploadPartRecord
	media    map[uuid.UUID]*postgres.MediaAsset
	touches  int
	// beforeTouch runs inside TouchResumableUpload, before its rule (a race hook).
	beforeTouch func()
}

func newFakeResumableDB(now func() time.Time) *fakeResumableDB {
	return &fakeResumableDB{now: now, sessions: map[uuid.UUID]*postgres.ResumableUpload{},
		parts: map[uuid.UUID]map[int]postgres.UploadPartRecord{}, media: map[uuid.UUID]*postgres.MediaAsset{}}
}

func (f *fakeResumableDB) CreateMedia(_ context.Context, m *postgres.MediaAsset) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.media[m.ID] = m
	return nil
}

func (f *fakeResumableDB) CreateResumableUpload(_ context.Context, u *postgres.ResumableUpload) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u.UploadID == uuid.Nil {
		u.UploadID = uuid.New()
	}
	c := *u
	c.CreatedAt = f.now()
	f.sessions[u.UploadID] = &c
	f.parts[u.UploadID] = map[int]postgres.UploadPartRecord{}
	return nil
}

func (f *fakeResumableDB) GetResumableUpload(_ context.Context, id uuid.UUID) (*postgres.ResumableUpload, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	c := *s
	return &c, nil
}

func (f *fakeResumableDB) ListUploadParts(_ context.Context, id uuid.UUID) ([]postgres.UploadPartRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []postgres.UploadPartRecord
	for _, p := range f.parts[id] {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PartNumber < out[j].PartNumber })
	return out, nil
}

func (f *fakeResumableDB) RecordUploadPart(_ context.Context, id uuid.UUID, n int, etag string, size int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.parts[id][n] = postgres.UploadPartRecord{PartNumber: n, ETag: etag, SizeBytes: size}
	return nil
}

func (f *fakeResumableDB) UpdateResumableUploadProgress(_ context.Context, id uuid.UUID, b int64, status string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[id].UploadedBytes, f.sessions[id].Status = b, status
	return nil
}

func (f *fakeResumableDB) CompleteResumableUpload(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.sessions[id]
	s.Status, s.UploadedBytes = "completed", s.TotalBytes
	return nil
}

// TouchResumableUpload keeps the SQL rule: open and unexpired only;
// LEAST(created + max, GREATEST(expires, now + idle)).
func (f *fakeResumableDB) TouchResumableUpload(_ context.Context, id uuid.UUID, idle, max time.Duration) (time.Time, error) {
	if f.beforeTouch != nil {
		f.beforeTouch()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.touches++
	s, ok := f.sessions[id]
	now := f.now()
	if !ok || (s.Status != "initiated" && s.Status != "uploading") || !s.ExpiresAt.After(now) {
		return time.Time{}, pgx.ErrNoRows
	}
	exp := s.ExpiresAt
	if t := now.Add(idle); t.After(exp) {
		exp = t
	}
	if ceil := s.CreatedAt.Add(max); ceil.Before(exp) {
		exp = ceil
	}
	s.ExpiresAt = exp
	return exp, nil
}

// expire is the expiry sweep (ExpireResumableUploads) for one session.
func (f *fakeResumableDB) expire(id uuid.UUID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.sessions[id]
	if (s.Status == "initiated" || s.Status == "uploading") && s.ExpiresAt.Before(f.now()) {
		s.Status = "expired"
		return true
	}
	return false
}

type fakeMultipart struct {
	mu        sync.Mutex
	parts     map[int][]byte
	completes int
	aborted   bool
	object    []byte
}

func (b *fakeMultipart) Bucket() string { return "test" }
func (b *fakeMultipart) InitMultipartUpload(context.Context, string, string) (string, error) {
	b.parts = map[int][]byte{}
	return "mp-1", nil
}
func (b *fakeMultipart) AbortMultipartUpload(context.Context, string, string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.aborted = true
	return nil
}
func (b *fakeMultipart) UploadPart(_ context.Context, _, _ string, n int, r io.Reader, size int64) (blob.MultipartPart, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return blob.MultipartPart{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.aborted {
		return blob.MultipartPart{}, errors.New("NoSuchUpload")
	}
	b.parts[n] = data
	return blob.MultipartPart{PartNumber: n, ETag: fmt.Sprintf("etag-%d-%d", n, len(data)), Size: int64(len(data))}, nil
}
func (b *fakeMultipart) CompleteMultipartUpload(_ context.Context, _, _ string, parts []blob.MultipartPart) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.aborted {
		return errors.New("NoSuchUpload")
	}
	b.completes++
	var all []byte
	for _, p := range parts {
		all = append(all, b.parts[p.PartNumber]...)
	}
	b.object = all
	return nil
}
func (b *fakeMultipart) StatObject(context.Context, string) (blob.ObjectInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.object == nil {
		return blob.ObjectInfo{}, blob.ErrObjectNotFound
	}
	return blob.ObjectInfo{Size: int64(len(b.object))}, nil
}

type flowRig struct {
	flow     *resumableFlow
	db       *fakeResumableDB
	blobs    *fakeMultipart
	clock    time.Time
	confirms int
	user     uuid.UUID
}

func newFlowRig(t *testing.T) *flowRig {
	t.Helper()
	r := &flowRig{clock: time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC), user: uuid.New(), blobs: &fakeMultipart{}}
	now := func() time.Time { return r.clock }
	r.db = newFakeResumableDB(now)
	lim := loadUploadLimits(func(string) string { return "" })
	r.flow = &resumableFlow{db: r.db, blobs: r.blobs, limits: lim, now: now,
		confirm: func(_ context.Context, mediaID, _ uuid.UUID) (*postgres.MediaAsset, error) {
			r.confirms++
			return &postgres.MediaAsset{ID: mediaID, ProcessingStatus: "processing"}, nil
		}}
	return r
}

// open starts a 3-part image session: 5 MiB, 5 MiB, 1 KiB.
func (r *flowRig) open(t *testing.T) *InitResumableUploadResponse {
	t.Helper()
	resp, err := r.flow.init(context.Background(), r.user, "image", "image/jpeg", 10*mib+1024, initOptions{})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if resp.TotalParts != 3 || resp.ChunkSize != 5*mib {
		t.Fatalf("plan %d x %d, want 3 x 5 MiB", resp.TotalParts, resp.ChunkSize)
	}
	return resp
}

func partSize(n int) int64 {
	if n == 3 {
		return 1024
	}
	return 5 * mib
}

func (r *flowRig) send(id uuid.UUID, n int, fill byte) (*UploadChunkResponse, error) {
	size := partSize(n)
	return r.flow.uploadPart(context.Background(), id, r.user, n, bytes.NewReader(bytes.Repeat([]byte{fill}, int(size))), size)
}

func TestResumablePartsStatusAndIdempotentResend(t *testing.T) {
	r := newFlowRig(t)
	s := r.open(t)
	ctx := context.Background()

	// Parts 1 and 3 (out of order): status lists exactly {1, 3}.
	if _, err := r.send(s.UploadID, 3, 'c'); err != nil {
		t.Fatalf("part 3: %v", err)
	}
	if _, err := r.send(s.UploadID, 1, 'a'); err != nil {
		t.Fatalf("part 1: %v", err)
	}
	st, err := r.flow.status(ctx, s.UploadID, r.user)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(st.UploadedParts) != "[1 3]" || st.UploadedBytes != 5*mib+1024 || st.Status != "uploading" {
		t.Fatalf("status parts %v bytes %d status %q", st.UploadedParts, st.UploadedBytes, st.Status)
	}
	// Re-sending part 1 is idempotent: progress does not double-count.
	resp, err := r.send(s.UploadID, 1, 'A')
	if err != nil {
		t.Fatalf("resend part 1: %v", err)
	}
	if resp.UploadedBytes != 5*mib+1024 || resp.AllPartsIn {
		t.Fatalf("after resend: %d bytes, all in %v", resp.UploadedBytes, resp.AllPartsIn)
	}
	// Complete is refused until part 2 lands.
	if _, err := r.flow.complete(ctx, s.UploadID, r.user); !errors.Is(err, ErrResumableIncomplete) {
		t.Fatalf("complete with a part missing: %v, want ErrResumableIncomplete", err)
	}
	if r.blobs.completes != 0 || r.confirms != 0 {
		t.Fatal("an incomplete upload was assembled or confirmed")
	}
	if resp, err := r.send(s.UploadID, 2, 'b'); err != nil || !resp.AllPartsIn {
		t.Fatalf("part 2: %v %+v", err, resp)
	}
	// The re-sent part 1 is what was assembled.
	if _, err := r.flow.complete(ctx, s.UploadID, r.user); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if r.blobs.object[0] != 'A' || int64(len(r.blobs.object)) != 10*mib+1024 {
		t.Fatalf("assembled %d bytes starting %q", len(r.blobs.object), r.blobs.object[0])
	}
	// A second complete re-confirms without assembling again.
	if _, err := r.flow.complete(ctx, s.UploadID, r.user); err != nil {
		t.Fatalf("second complete: %v", err)
	}
	if r.blobs.completes != 1 || r.confirms != 2 {
		t.Fatalf("assembled %d times, confirmed %d, want 1 and 2", r.blobs.completes, r.confirms)
	}
	// A part after completion is refused.
	if _, err := r.send(s.UploadID, 2, 'b'); !errors.Is(err, ErrResumableCompleted) {
		t.Fatalf("part after complete: %v", err)
	}
}

func TestResumablePartWrongSizeOrNumberIsRefused(t *testing.T) {
	r := newFlowRig(t)
	s := r.open(t)
	ctx := context.Background()
	cases := []struct {
		n    int
		size int64
	}{{0, 5 * mib}, {4, 1024}, {1, 5*mib - 1}, {3, 5 * mib}, {2, 1024}}
	for _, c := range cases {
		_, err := r.flow.uploadPart(ctx, s.UploadID, r.user, c.n, bytes.NewReader(make([]byte, c.size)), c.size)
		if !errors.Is(err, ErrResumableBadPart) {
			t.Fatalf("part %d of %d bytes: %v, want ErrResumableBadPart", c.n, c.size, err)
		}
	}
	if len(r.blobs.parts) != 0 || r.db.touches != 0 {
		t.Fatal("a refused part reached the object store or moved the expiry")
	}
}

func TestResumableSomeoneElsesSessionIsNotFound(t *testing.T) {
	r := newFlowRig(t)
	s := r.open(t)
	ctx := context.Background()
	other := uuid.New()
	if _, err := r.flow.status(ctx, s.UploadID, other); !errors.Is(err, ErrResumableNotFound) {
		t.Fatalf("status: %v", err)
	}
	if _, err := r.flow.uploadPart(ctx, s.UploadID, other, 3, bytes.NewReader(make([]byte, 1024)), 1024); !errors.Is(err, ErrResumableNotFound) {
		t.Fatalf("part: %v", err)
	}
	if _, err := r.flow.complete(ctx, s.UploadID, other); !errors.Is(err, ErrResumableNotFound) {
		t.Fatalf("complete: %v", err)
	}
	if _, err := r.flow.status(ctx, uuid.New(), r.user); !errors.Is(err, ErrResumableNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}

// lv-16: the expiry slides with each accepted part, up to the ceiling.
func TestResumableExpirySlidesOnActivityUpToTheCeiling(t *testing.T) {
	r := newFlowRig(t)
	s := r.open(t)
	start := r.clock
	if !s.ExpiresAt.Equal(start.Add(24 * time.Hour)) {
		t.Fatalf("init expiry %v, want start + 24h", s.ExpiresAt)
	}
	// A part at hour 23 moves the expiry to hour 47.
	r.clock = start.Add(23 * time.Hour)
	resp, err := r.send(s.UploadID, 1, 'a')
	if err != nil {
		t.Fatalf("part at hour 23: %v", err)
	}
	if !resp.ExpiresAt.Equal(start.Add(47 * time.Hour)) {
		t.Fatalf("expiry after hour-23 part %v, want start + 47h", resp.ExpiresAt)
	}
	// Past hour 24 the session is still alive: the sweep does not take it.
	r.clock = start.Add(30 * time.Hour)
	if r.db.expire(s.UploadID) {
		t.Fatal("the sweep expired a session active an hour ago")
	}
	// Keep it busy for days: the ceiling (7 days from init) still holds.
	for h := 30; h < 7*24; h += 20 {
		r.clock = start.Add(time.Duration(h) * time.Hour)
		if _, err := r.send(s.UploadID, 1, 'a'); err != nil {
			t.Fatalf("part at hour %d: %v", h, err)
		}
	}
	st, _ := r.flow.status(context.Background(), s.UploadID, r.user)
	if !st.ExpiresAt.Equal(start.Add(7 * 24 * time.Hour)) {
		t.Fatalf("expiry %v, want the 7-day ceiling %v", st.ExpiresAt, start.Add(7*24*time.Hour))
	}
	r.clock = start.Add(7*24*time.Hour + time.Second)
	if _, err := r.send(s.UploadID, 2, 'b'); !errors.Is(err, ErrResumableExpired) {
		t.Fatalf("part past the ceiling: %v, want ErrResumableExpired", err)
	}
	st, _ = r.flow.status(context.Background(), s.UploadID, r.user)
	if st.Status != "expired" {
		t.Fatalf("status past the ceiling %q, want expired", st.Status)
	}
}

// An idle session expires at its idle TTL: part and complete are refused.
func TestResumableIdleSessionExpires(t *testing.T) {
	r := newFlowRig(t)
	s := r.open(t)
	for n := 1; n <= 3; n++ {
		if _, err := r.send(s.UploadID, n, 'x'); err != nil {
			t.Fatal(err)
		}
	}
	r.clock = r.clock.Add(24*time.Hour + time.Minute)
	if _, err := r.send(s.UploadID, 1, 'x'); !errors.Is(err, ErrResumableExpired) {
		t.Fatalf("part after idle TTL: %v", err)
	}
	if _, err := r.flow.complete(context.Background(), s.UploadID, r.user); !errors.Is(err, ErrResumableExpired) {
		t.Fatalf("complete after idle TTL: %v, want ErrResumableExpired", err)
	}
	if r.blobs.completes != 0 || r.confirms != 0 {
		t.Fatal("an expired session was assembled")
	}
	// Swept: still refused by status alone.
	r.db.expire(s.UploadID)
	if _, err := r.flow.complete(context.Background(), s.UploadID, r.user); !errors.Is(err, ErrResumableExpired) {
		t.Fatalf("complete after sweep: %v", err)
	}
}

// Complete racing the expiry sweep: the sweep wins between complete's read
// and its claim, so complete refuses and never assembles a multipart upload
// the sweep is aborting.
func TestResumableCompleteRacingTheExpirySweep(t *testing.T) {
	r := newFlowRig(t)
	s := r.open(t)
	for n := 1; n <= 3; n++ {
		if _, err := r.send(s.UploadID, n, 'x'); err != nil {
			t.Fatal(err)
		}
	}
	r.db.beforeTouch = func() {
		r.clock = r.clock.Add(25 * time.Hour)
		if r.db.expire(s.UploadID) {
			_ = r.blobs.AbortMultipartUpload(context.Background(), "", "")
		}
	}
	if _, err := r.flow.complete(context.Background(), s.UploadID, r.user); !errors.Is(err, ErrResumableExpired) {
		t.Fatalf("complete that lost to the sweep: %v, want ErrResumableExpired", err)
	}
	if r.blobs.completes != 0 || r.confirms != 0 {
		t.Fatal("complete assembled or confirmed a session the sweep aborted")
	}
}

// Two completes racing: the loser finds the session completed at its claim
// and answers through confirm, without a second assembly.
func TestResumableCompleteRacingAnotherComplete(t *testing.T) {
	r := newFlowRig(t)
	s := r.open(t)
	for n := 1; n <= 3; n++ {
		if _, err := r.send(s.UploadID, n, 'x'); err != nil {
			t.Fatal(err)
		}
	}
	r.db.beforeTouch = func() {
		r.db.beforeTouch = nil
		if _, err := r.flow.complete(context.Background(), s.UploadID, r.user); err != nil {
			t.Errorf("winning complete: %v", err)
		}
	}
	if _, err := r.flow.complete(context.Background(), s.UploadID, r.user); err != nil {
		t.Fatalf("losing complete: %v", err)
	}
	if r.blobs.completes != 1 || r.confirms != 2 {
		t.Fatalf("assembled %d, confirmed %d; want 1 and 2", r.blobs.completes, r.confirms)
	}
}

// lv-30: a declared length over the cap is refused at resumable init,
// before the limiter, the object store or the database.
func TestResumableInitRefusesDeclaredLengthOverTheCap(t *testing.T) {
	old := uploadLimits
	t.Cleanup(func() { uploadLimits = old })
	uploadLimits.VideoMaxDurationSeconds = 4 * 3600
	calls := 0
	s := &Service{uploadLimit: func(context.Context, uuid.UUID) error { calls++; return nil }}
	_, err := s.InitResumableUpload(context.Background(), uuid.New(), "video", "video/mp4", 100<<20,
		WithDeclaredDurationMs((4*3600+60)*1000))
	if !errors.Is(err, ErrVideoTooLong) {
		t.Fatalf("err = %v, want ErrVideoTooLong", err)
	}
	if calls != 0 {
		t.Fatal("an over-long upload spent the count limit")
	}
}
