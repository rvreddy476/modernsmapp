package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// ── lv-3 (a): the count limit fails closed ───────────────────────────

func TestUploadRateLimitFailsClosedOnRedisError(t *testing.T) {
	// Nothing listens on port 1: every command errors at once.
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 200 * time.Millisecond, MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	s := &Service{rdb: rdb}
	if err := s.CheckUploadRateLimit(context.Background(), uuid.New()); !errors.Is(err, ErrUploadLimiterUnavailable) {
		t.Fatalf("err = %v, want ErrUploadLimiterUnavailable (fail closed)", err)
	}
}

func TestUploadRateLimitWithNoRedisFailsClosedUnlessOptional(t *testing.T) {
	old := uploadLimits
	t.Cleanup(func() { uploadLimits = old })
	s := &Service{}
	uploadLimits.RateLimitOptional = false
	if err := s.CheckUploadRateLimit(context.Background(), uuid.New()); !errors.Is(err, ErrUploadLimiterUnavailable) {
		t.Fatalf("no Redis: err = %v, want ErrUploadLimiterUnavailable", err)
	}
	uploadLimits.RateLimitOptional = true
	if err := s.CheckUploadRateLimit(context.Background(), uuid.New()); err != nil {
		t.Fatalf("no Redis with the local opt-out: %v", err)
	}
}

func TestUploadRateLimitCountsWithRedis(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	s := &Service{rdb: rdb}
	user := uuid.New()
	for i := 0; i < MaxUploadsPerHour; i++ {
		if err := s.CheckUploadRateLimit(context.Background(), user); err != nil {
			t.Fatalf("upload %d: %v", i+1, err)
		}
	}
	err := s.CheckUploadRateLimit(context.Background(), user)
	if err == nil || !strings.Contains(err.Error(), "upload rate limit exceeded") {
		t.Fatalf("upload %d: %v, want the hourly refusal", MaxUploadsPerHour+1, err)
	}
}

// ── limits from the environment ──────────────────────────────────────

func TestUploadLimitsDefaultsAndOverrides(t *testing.T) {
	d := loadUploadLimits(func(string) string { return "" })
	if d.VideoMaxDurationSeconds != 4*3600 || d.DailyVideoBytes != 32<<30 || d.DailyVideoSeconds != 8*3600 ||
		d.MaxInflightLong != 2 || d.ResumableIdleTTL != 24*time.Hour || d.ResumableMaxLifetime != 7*24*time.Hour ||
		d.RateLimitOptional {
		t.Fatalf("defaults %+v", d)
	}
	env := map[string]string{
		"MEDIA_MAX_VIDEO_DURATION_SEC":     "0",
		"MEDIA_UPLOAD_DAILY_VIDEO_BYTES":   "1000",
		"MEDIA_UPLOAD_DAILY_VIDEO_SECONDS": "60",
		"MEDIA_UPLOAD_MAX_INFLIGHT_LONG":   "5",
		"MEDIA_RESUMABLE_IDLE_TTL":         "48h",
		"MEDIA_RESUMABLE_MAX_LIFETIME":     "24h", // shorter than idle: raised to idle
		"MEDIA_UPLOAD_RATE_LIMIT_OPTIONAL": "true",
	}
	l := loadUploadLimits(func(k string) string { return env[k] })
	if l.VideoMaxDurationSeconds != 0 || l.DailyVideoBytes != 1000 || l.DailyVideoSeconds != 60 || l.MaxInflightLong != 5 ||
		l.ResumableIdleTTL != 48*time.Hour || l.ResumableMaxLifetime != 48*time.Hour || !l.RateLimitOptional {
		t.Fatalf("overrides %+v", l)
	}
	bad := loadUploadLimits(func(string) string { return "-3" })
	if bad.DailyVideoBytes != 32<<30 || bad.VideoMaxDurationSeconds != 4*3600 || bad.ResumableIdleTTL != 24*time.Hour {
		t.Fatalf("invalid values must keep the defaults: %+v", bad)
	}
}

// ── lv-3 (b): the budget ─────────────────────────────────────────────

type fakeQuotaStore struct {
	usage    postgres.UploadUsage
	override *postgres.UploadQuotaOverride
	readErr  error
	charged  map[uuid.UUID]postgres.VideoUploadCharge
	sinceGot time.Time
}

func (f *fakeQuotaStore) UploadUsageFor(_ context.Context, _ uuid.UUID, since time.Time, _ uuid.UUID) (postgres.UploadUsage, *postgres.UploadQuotaOverride, error) {
	f.sinceGot = since
	return f.usage, f.override, f.readErr
}

func (f *fakeQuotaStore) AdmitVideoUpload(_ context.Context, c postgres.VideoUploadCharge, since time.Time,
	decide func(postgres.UploadUsage, *postgres.UploadQuotaOverride) error) error {
	if f.readErr != nil {
		return f.readErr
	}
	if _, ok := f.charged[c.MediaID]; ok {
		return postgres.ErrUploadAlreadyCharged
	}
	f.sinceGot = since
	if err := decide(f.usage, f.override); err != nil {
		return err
	}
	if f.charged == nil {
		f.charged = map[uuid.UUID]postgres.VideoUploadCharge{}
	}
	f.charged[c.MediaID] = c
	f.usage.Bytes += c.Bytes
	f.usage.DurationMs += c.DurationMs
	if c.IsLong {
		f.usage.InflightLong++
	}
	return nil
}

func testLimits() UploadLimits {
	l := loadUploadLimits(func(string) string { return "" })
	l.DailyVideoBytes, l.DailyVideoSeconds, l.MaxInflightLong = 10<<30, 3600, 1
	return l
}

func TestVideoBudgetDecision(t *testing.T) {
	l := testLimits()
	var q *UploadQuotaError
	// Bytes: exactly at the budget passes, one byte over is refused.
	if err := decideVideoBudget(l, postgres.UploadUsage{Bytes: 6 << 30}, nil, 4<<30, 0, false, true); err != nil {
		t.Fatalf("at the byte budget: %v", err)
	}
	err := decideVideoBudget(l, postgres.UploadUsage{Bytes: 6 << 30}, nil, 4<<30+1, 0, false, true)
	if !errors.As(err, &q) || q.Limit != QuotaLimitDailyBytes || !errors.Is(err, ErrUploadQuotaExceeded) {
		t.Fatalf("over the byte budget: %v", err)
	}
	// Seconds: known lengths count; unknown (0) does not.
	if err := decideVideoBudget(l, postgres.UploadUsage{DurationMs: 3000 * 1000}, nil, 1, 600*1000, false, true); err != nil {
		t.Fatalf("at the seconds budget: %v", err)
	}
	err = decideVideoBudget(l, postgres.UploadUsage{DurationMs: 3000 * 1000}, nil, 1, 601*1000, false, true)
	if !errors.As(err, &q) || q.Limit != QuotaLimitDailySeconds {
		t.Fatalf("over the seconds budget: %v", err)
	}
	if err := decideVideoBudget(l, postgres.UploadUsage{DurationMs: 3600 * 1000}, nil, 1, 0, true, false); err != nil {
		t.Fatalf("an unknown length counted against the seconds budget: %v", err)
	}
	// In flight: a long upload is refused at the cap; a short one is not.
	err = decideVideoBudget(l, postgres.UploadUsage{InflightLong: 1}, nil, 1, 0, true, true)
	if !errors.Is(err, ErrLongVideoInFlight) || errors.Is(err, ErrUploadQuotaExceeded) {
		t.Fatalf("second long video in flight: %v", err)
	}
	if err := decideVideoBudget(l, postgres.UploadUsage{InflightLong: 1}, nil, 1, 30*1000, false, true); err != nil {
		t.Fatalf("a short video met the long cap: %v", err)
	}
	// An override raises the account's limits; 0 is no limit.
	raised, zero := int64(100<<30), 0
	o := &postgres.UploadQuotaOverride{DailyVideoBytes: &raised, MaxInflightLong: &zero}
	if err := decideVideoBudget(l, postgres.UploadUsage{Bytes: 50 << 30, InflightLong: 9}, o, 1<<30, 0, true, true); err != nil {
		t.Fatalf("override ignored: %v", err)
	}
}

func TestVideoBudgetAtInitReadsTheWindowAndChargesNothing(t *testing.T) {
	l := testLimits()
	now := time.Now()
	st := &fakeQuotaStore{usage: postgres.UploadUsage{Bytes: 9 << 30, InflightLong: 1}}
	if err := videoBudgetAtInit(context.Background(), st, l, now, uuid.New(), 2<<30, 0); !errors.Is(err, ErrUploadQuotaExceeded) {
		t.Fatalf("declared size over the budget: %v", err)
	}
	if !st.sinceGot.Equal(now.Add(-24 * time.Hour)) {
		t.Fatalf("window starts %v, want now - 24h", st.sinceGot)
	}
	// An undeclared length does not meet the in-flight cap at init.
	if err := videoBudgetAtInit(context.Background(), st, l, now, uuid.New(), 1<<20, 0); err != nil {
		t.Fatalf("undeclared length at init: %v", err)
	}
	// A declared long one does.
	if err := videoBudgetAtInit(context.Background(), st, l, now, uuid.New(), 1<<20, 3600*1000); !errors.Is(err, ErrLongVideoInFlight) {
		t.Fatalf("declared long video with one in flight: %v", err)
	}
	if len(st.charged) != 0 {
		t.Fatal("init charged the ledger")
	}
	st.readErr = errors.New("db down")
	if err := videoBudgetAtInit(context.Background(), st, l, now, uuid.New(), 1, 0); !errors.Is(err, ErrUploadLimiterUnavailable) {
		t.Fatalf("unreadable budget: %v, want fail closed", err)
	}
}

func TestAdmitVideoAtConfirm(t *testing.T) {
	l := testLimits()
	ctx := context.Background()
	user := uuid.New()
	video := func() *postgres.MediaAsset {
		return &postgres.MediaAsset{ID: uuid.New(), UploaderID: user, FileType: "video"}
	}
	probe := func(s float64, err error) func() (float64, error) { return func() (float64, error) { return s, err } }
	rejected := 0
	reject := func() error { rejected++; return nil }
	st := &fakeQuotaStore{}

	// lv-30: over the length cap is rejected and nothing is charged.
	if err := admitVideoAtConfirm(ctx, st, l, time.Now(), video(), 1<<20, probe(4*3600+1, nil), reject); !errors.Is(err, ErrVideoTooLong) {
		t.Fatalf("over-long video: %v", err)
	}
	if rejected != 1 || len(st.charged) != 0 {
		t.Fatalf("rejected %d, charged %d", rejected, len(st.charged))
	}
	// A short reel is charged with its probed length.
	reel := video()
	if err := admitVideoAtConfirm(ctx, st, l, time.Now(), reel, 1<<20, probe(30, nil), reject); err != nil {
		t.Fatalf("reel: %v", err)
	}
	if c := st.charged[reel.ID]; c.DurationMs != 30000 || c.IsLong || c.Bytes != 1<<20 {
		t.Fatalf("reel charge %+v", c)
	}
	// An unreadable length is charged as long, with no seconds.
	unknown := video()
	if err := admitVideoAtConfirm(ctx, st, l, time.Now(), unknown, 1<<20, probe(0, errors.New("not mp4")), reject); err != nil {
		t.Fatalf("unknown length: %v", err)
	}
	if c := st.charged[unknown.ID]; c.DurationMs != 0 || !c.IsLong {
		t.Fatalf("unknown-length charge %+v", c)
	}
	// The second long video while one is in flight is refused, uncharged.
	second := video()
	if err := admitVideoAtConfirm(ctx, st, l, time.Now(), second, 1<<20, probe(3600, nil), reject); !errors.Is(err, ErrLongVideoInFlight) {
		t.Fatalf("second long video: %v", err)
	}
	if _, ok := st.charged[second.ID]; ok {
		t.Fatal("a refused video was charged")
	}
	// A retried confirm of a charged video is admitted again.
	if err := admitVideoAtConfirm(ctx, st, l, time.Now(), unknown, 1<<20, probe(0, nil), reject); err != nil {
		t.Fatalf("retried confirm: %v", err)
	}
	if rejected != 1 {
		t.Fatalf("rejected %d times, want 1", rejected)
	}
}

func TestQuotaMessagesNameTheLimit(t *testing.T) {
	for _, c := range []struct {
		e    *UploadQuotaError
		want string
	}{
		{&UploadQuotaError{Limit: QuotaLimitDailyBytes, Max: 32 << 30}, "32 GiB"},
		{&UploadQuotaError{Limit: QuotaLimitDailySeconds, Max: 8 * 3600}, "8 hours"},
		{&UploadQuotaError{Limit: QuotaLimitInflightLong, Max: 2, Used: 2}, "2 long videos"},
	} {
		if !strings.Contains(c.e.Message(), c.want) {
			t.Fatalf("%s message %q lacks %q", c.e.Limit, c.e.Message(), c.want)
		}
	}
	if !strings.Contains(VideoTooLongMessage(4*3600), "4 hours") {
		t.Fatal(VideoTooLongMessage(4 * 3600))
	}
}

// lv-16: orphan GC leaves assets with an open resumable session.
func TestOrphanSweepSkipsOpenResumableSessions(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	got := withoutOpenSessions([]uuid.UUID{a, b, c}, map[uuid.UUID]bool{b: true})
	if len(got) != 2 || got[0] != a || got[1] != c {
		t.Fatalf("got %v", got)
	}
}
