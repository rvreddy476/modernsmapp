// Lane D5 selfie (blink liveness) integration tests. Skipped unless
// TEST_PG_DSN is set; refuses a database not named *_test. media-service is
// replaced by a fake LivenessClient returning scripted results; the per-frame
// blink analysis is tested in media-service (processing/liveness_test.go) and
// the HTTP call in liveness_client_test.go.
package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	datingevents "github.com/atpost/dating-service/internal/events"
	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type fakeLiveness struct {
	mu      sync.Mutex
	results []*LivenessResult // one per call; the last one repeats
	err     error
	calls   []LivenessRequest
}

func (f *fakeLiveness) CheckLiveness(_ context.Context, req LivenessRequest) (*LivenessResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	if f.err != nil {
		return nil, f.err
	}
	i := len(f.calls) - 1
	if i >= len(f.results) {
		i = len(f.results) - 1
	}
	cp := *f.results[i]
	return &cp, nil
}

func (f *fakeLiveness) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// blinked is a clean two-blink recording of one consistent face.
func blinked(similarity float64) *LivenessResult {
	return &LivenessResult{BlinksDetected: 2, FramesAnalysed: 32, DurationMs: 3000, SingleFace: true,
		SameFaceAcrossFrames: true, Similarity: similarity, Match: similarity >= 90, Provider: "rekognition"}
}

func scripted(results ...*LivenessResult) *fakeLiveness { return &fakeLiveness{results: results} }

func newSelfieSvc(t *testing.T) (*Service, *store.Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping lane D5 selfie verification tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to run against database %q: name must end in _test", cfg.ConnConfig.Database)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	ensureSchemaForTest(t, pool)
	st := store.New(pool)
	svc := New(st, nil)
	svc.SetProducer(datingevents.NewProducerWithWriter(&recordingWriter{}))
	svc.SetMessageClient(&syncStubMessageClient{})
	return svc, st, pool
}

// seedPendingSelfie walks a profile to pending_selfie through the writer
// (basics, approved primary photo) and returns the primary photo's media id.
func seedPendingSelfie(t *testing.T, st *store.Store, id uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	seedBasicsProfile(t, st, id)
	primary := uuid.New()
	photo, err := st.CreatePhoto(ctx, id, store.CreatePhotoParams{MediaID: primary, IsPrimary: true, Visibility: "public"})
	if err != nil {
		t.Fatalf("seed photo: %v", err)
	}
	if _, err := st.SetPhotoModerationStatus(ctx, photo.ID, "approved", ""); err != nil {
		t.Fatalf("approve photo: %v", err)
	}
	driveProfile(t, st, id, store.ProfileEventBasicsComplete, store.ProfileActorSystem)
	if p := driveProfile(t, st, id, store.ProfileEventPhotoApproved, store.ProfileActorSystem); p.ProfileStatus != store.ProfileStatusPendingSelfie {
		t.Fatalf("seeded status = %s, want pending_selfie", p.ProfileStatus)
	}
	return primary
}

func profileStatusOf(t *testing.T, st *store.Store, id uuid.UUID) string {
	t.Helper()
	p, err := st.GetProfile(context.Background(), id)
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	return p.ProfileStatus
}

func newChallenge(t *testing.T, svc *Service, id uuid.UUID) uuid.UUID {
	t.Helper()
	ch, err := svc.CreateSelfieChallenge(context.Background(), id)
	if err != nil {
		t.Fatalf("create challenge: %v", err)
	}
	if ch.ChallengeID == uuid.Nil || ch.Instruction != "blink_twice" || ch.MaxDurationMs != svc.SelfieSettings().MaxVideoDurationMs || ch.ExpiresAt.IsZero() {
		t.Fatalf("challenge = %+v", ch)
	}
	return ch.ChallengeID
}

type selfieRow struct {
	status, provider string
	score            float64
	blinks           int
	videoID          uuid.UUID
}

func selfieRowOf(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) selfieRow {
	t.Helper()
	var r selfieRow
	var provider *string
	var score *float64
	var blinks *int
	var video *uuid.UUID
	if err := pool.QueryRow(context.Background(), `
        SELECT COALESCE(selfie_status, ''), selfie_provider, selfie_score, selfie_blinks, selfie_media_id
        FROM dating_verifications WHERE user_id = $1`, id).Scan(&r.status, &provider, &score, &blinks, &video); err != nil {
		t.Fatalf("read verification: %v", err)
	}
	if provider != nil {
		r.provider = *provider
	}
	if score != nil {
		r.score = *score
	}
	if blinks != nil {
		r.blinks = *blinks
	}
	if video != nil {
		r.videoID = *video
	}
	return r
}

func TestSelfie_TwoBlinksPassActivatesThroughWriter(t *testing.T) {
	svc, st, pool := newSelfieSvc(t)
	ctx := context.Background()
	user, video := uuid.New(), uuid.New()
	primary := seedPendingSelfie(t, st, user)
	live := scripted(blinked(96.4))
	svc.SetLivenessClient(live)

	res, err := svc.SubmitSelfie(ctx, user, video, newChallenge(t, svc, user))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if res.Status != store.SelfieStatusPassed || !res.Passed || res.TrustTier != "selfie" ||
		res.ProfileStatus != store.ProfileStatusActive || res.AttemptsRemaining != DefaultSelfieMaxAttempts-1 {
		t.Fatalf("result = %+v", res)
	}
	p, err := st.GetProfile(ctx, user)
	if err != nil || p.ProfileStatus != store.ProfileStatusActive || p.PriorStatus != nil || p.TrustTier != "selfie" {
		t.Fatalf("profile after pass = %+v, %v", p, err)
	}
	if row := selfieRowOf(t, pool, user); row.status != "passed" || row.score != 96 || row.blinks != 2 ||
		row.provider != "rekognition" || row.videoID != video {
		t.Fatalf("verification row = %+v", row)
	}
	if live.callCount() != 1 || live.calls[0] != (LivenessRequest{VideoMediaID: video, ReferenceMediaID: primary, RequesterUserID: user}) {
		t.Fatalf("liveness calls = %+v; want the video vs the primary photo for the user", live.calls)
	}
	if _, err := svc.CreateSelfieChallenge(ctx, user); !errors.Is(err, store.ErrSelfieAlreadyPassed) {
		t.Fatalf("challenge after pass: err=%v, want ErrSelfieAlreadyPassed", err)
	}
}

func TestSelfie_ChallengeRequiredUnexpiredUnusedOwned(t *testing.T) {
	svc, st, pool := newSelfieSvc(t)
	ctx := context.Background()
	user, other := uuid.New(), uuid.New()
	seedPendingSelfie(t, st, user)
	seedPendingSelfie(t, st, other)
	oneBlink := blinked(95)
	oneBlink.BlinksDetected, oneBlink.Reason = 1, "NOT_ENOUGH_BLINKS"
	live := scripted(oneBlink)
	svc.SetLivenessClient(live)

	for name, ch := range map[string]uuid.UUID{"none": uuid.Nil, "unknown": uuid.New()} {
		if _, err := svc.SubmitSelfie(ctx, user, uuid.New(), ch); !errors.Is(err, store.ErrSelfieChallengeInvalid) {
			t.Fatalf("%s challenge: err=%v, want ErrSelfieChallengeInvalid", name, err)
		}
	}
	expired := newChallenge(t, svc, user)
	if _, err := pool.Exec(ctx, `UPDATE dating_selfie_challenges SET expires_at = now() - INTERVAL '1 second' WHERE id = $1`, expired); err != nil {
		t.Fatalf("expire challenge: %v", err)
	}
	if _, err := svc.SubmitSelfie(ctx, user, uuid.New(), expired); !errors.Is(err, store.ErrSelfieChallengeInvalid) {
		t.Fatalf("expired challenge: err=%v", err)
	}
	foreign := newChallenge(t, svc, other)
	if _, err := svc.SubmitSelfie(ctx, user, uuid.New(), foreign); !errors.Is(err, store.ErrSelfieChallengeInvalid) {
		t.Fatalf("foreign challenge: err=%v", err)
	}
	if live.callCount() != 0 {
		t.Fatalf("media-service was called %d times for refused challenges", live.callCount())
	}
	ch := newChallenge(t, svc, user)
	if res, err := svc.SubmitSelfie(ctx, user, uuid.New(), ch); err != nil || res.Status != store.SelfieStatusFailed {
		t.Fatalf("first use: %+v, %v", res, err)
	}
	if _, err := svc.SubmitSelfie(ctx, user, uuid.New(), ch); !errors.Is(err, store.ErrSelfieChallengeInvalid) {
		t.Fatalf("reused challenge: err=%v", err)
	}
	if live.callCount() != 1 {
		t.Fatalf("liveness calls = %d, want 1", live.callCount())
	}
	if _, err := svc.SubmitSelfie(ctx, other, uuid.New(), foreign); err != nil {
		t.Fatalf("owner using their own challenge after a foreign attempt: %v", err)
	}
	if s := profileStatusOf(t, st, user); s != store.ProfileStatusPendingSelfie {
		t.Fatalf("status = %s, want pending_selfie", s)
	}
}

func TestSelfie_PrimaryPhotoNotApproved409(t *testing.T) {
	svc, st, _ := newSelfieSvc(t)
	ctx := context.Background()
	live := scripted(blinked(99))
	svc.SetLivenessClient(live)

	pendingPhoto := uuid.New()
	seedBasicsProfile(t, st, pendingPhoto)
	if _, err := st.CreatePhoto(ctx, pendingPhoto, store.CreatePhotoParams{MediaID: uuid.New(), IsPrimary: true, Visibility: "public"}); err != nil {
		t.Fatalf("seed photo: %v", err)
	}
	noPhoto := uuid.New()
	seedBasicsProfile(t, st, noPhoto)

	for name, id := range map[string]uuid.UUID{"pending photo": pendingPhoto, "no photo": noPhoto} {
		if _, err := svc.CreateSelfieChallenge(ctx, id); !errors.Is(err, ErrPrimaryPhotoNotApproved) {
			t.Fatalf("%s challenge: err=%v, want ErrPrimaryPhotoNotApproved", name, err)
		}
		if _, err := svc.SubmitSelfie(ctx, id, uuid.New(), uuid.New()); !errors.Is(err, ErrPrimaryPhotoNotApproved) {
			t.Fatalf("%s submit: err=%v, want ErrPrimaryPhotoNotApproved", name, err)
		}
	}
	if live.callCount() != 0 {
		t.Fatalf("media-service called without an approved primary photo")
	}
	user := uuid.New()
	primary := seedPendingSelfie(t, st, user)
	if _, err := svc.SubmitSelfie(ctx, user, primary, newChallenge(t, svc, user)); !errors.Is(err, ErrSelfieSameAsPrimaryPhoto) {
		t.Fatalf("primary photo as the video: err=%v", err)
	}
}

func TestSelfie_BorderlineGoesToReviewAndAdminApproveActivates(t *testing.T) {
	svc, st, _ := newSelfieSvc(t)
	ctx := context.Background()
	user, video, admin := uuid.New(), uuid.New(), uuid.New()
	primary := seedPendingSelfie(t, st, user)
	svc.SetLivenessClient(scripted(blinked(84.2)))

	res, err := svc.SubmitSelfie(ctx, user, video, newChallenge(t, svc, user))
	if err != nil || res.Status != store.SelfieStatusPendingReview || res.Passed || res.Reason != SelfieReasonManualReview ||
		res.ProfileStatus != store.ProfileStatusPendingSelfie {
		t.Fatalf("borderline = %+v, %v", res, err)
	}
	queue, err := svc.ListSelfieReviews(ctx, 200)
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	var item *store.SelfieReview
	for _, q := range queue {
		if q.UserID == user {
			item = q
		}
	}
	if item == nil || item.Similarity == nil || *item.Similarity != 84 || item.Blinks == nil || *item.Blinks != 2 ||
		item.SelfieVideoMediaID == nil || *item.SelfieVideoMediaID != video ||
		item.PrimaryPhotoMediaID == nil || *item.PrimaryPhotoMediaID != primary ||
		item.Instruction == nil || *item.Instruction != "blink_twice" ||
		item.Reason == nil || *item.Reason != selfieReviewBorderline {
		t.Fatalf("review queue item = %+v", item)
	}
	if _, err := svc.CreateSelfieChallenge(ctx, user); !errors.Is(err, store.ErrSelfieReviewPending) {
		t.Fatalf("challenge during review: err=%v", err)
	}
	out, err := svc.ReviewSelfie(ctx, admin, user, "approve", "same person")
	if err != nil || !out.Passed || out.ProfileStatus != store.ProfileStatusActive {
		t.Fatalf("approve = %+v, %v", out, err)
	}
	audit, err := st.ListAdminAudit(ctx, store.AdminAuditFilter{TargetUserID: user}, 20, 0)
	if err != nil || len(audit) == 0 || audit[0].Action != "selfie_review_approve" || audit[0].ActorAdminID != admin {
		t.Fatalf("audit = %+v, %v", audit, err)
	}
	if _, err := svc.ReviewSelfie(ctx, admin, user, "approve", ""); !errors.Is(err, store.ErrSelfieNotPendingReview) {
		t.Fatalf("second decision: err=%v", err)
	}
}

func TestSelfie_AdminRejectKeepsPendingSelfie(t *testing.T) {
	svc, st, pool := newSelfieSvc(t)
	ctx := context.Background()
	user := uuid.New()
	seedPendingSelfie(t, st, user)
	svc.SetLivenessClient(scripted(blinked(88)))
	if res, err := svc.SubmitSelfie(ctx, user, uuid.New(), newChallenge(t, svc, user)); err != nil || res.Status != store.SelfieStatusPendingReview {
		t.Fatalf("submit = %+v, %v", res, err)
	}
	out, err := svc.ReviewSelfie(ctx, uuid.New(), user, "reject", "different person")
	if err != nil || out.Passed || out.Status != store.SelfieStatusFailed || out.ProfileStatus != store.ProfileStatusPendingSelfie {
		t.Fatalf("reject = %+v, %v", out, err)
	}
	if row := selfieRowOf(t, pool, user); row.status != "failed" {
		t.Fatalf("verification after reject = %+v", row)
	}
	newChallenge(t, svc, user)
}

// Every liveness failure is a failed verdict: the profile stays
// pending_selfie and the attempt counts toward the limit.
func TestSelfie_LivenessFailuresStayPendingSelfie(t *testing.T) {
	svc, st, pool := newSelfieSvc(t)
	ctx := context.Background()
	with := func(mut func(*LivenessResult)) *LivenessResult {
		r := blinked(97)
		mut(r)
		return r
	}
	cases := []struct {
		name       string
		result     *LivenessResult
		wantReason string
	}{
		{"one blink (media reason)", with(func(r *LivenessResult) { r.BlinksDetected, r.Reason, r.Similarity = 1, "NOT_ENOUGH_BLINKS", 0 }), SelfieReasonNotEnoughBlinks},
		{"one blink (no reason: dating's own bar)", with(func(r *LivenessResult) { r.BlinksDetected = 1 }), SelfieReasonNotEnoughBlinks},
		{"eyes never closed", with(func(r *LivenessResult) { r.BlinksDetected, r.Reason, r.Similarity = 0, "NOT_ENOUGH_BLINKS", 0 }), SelfieReasonNotEnoughBlinks},
		{"second face", with(func(r *LivenessResult) {
			r.SingleFace, r.SameFaceAcrossFrames, r.Reason, r.Similarity = false, false, "MULTIPLE_FACES", 0
		}), SelfieReasonMultipleFaces},
		{"face changed", with(func(r *LivenessResult) { r.SameFaceAcrossFrames, r.Reason, r.Similarity = false, "FACE_CHANGED", 0 }), SelfieReasonFaceChanged},
		{"face changed without reason", with(func(r *LivenessResult) { r.SameFaceAcrossFrames = false }), SelfieReasonFaceChanged},
		{"no face", with(func(r *LivenessResult) { r.SingleFace, r.Reason, r.Similarity = false, "NO_FACE", 0 }), SelfieReasonNoFace},
		{"low quality", with(func(r *LivenessResult) { r.Reason, r.Similarity = "LOW_QUALITY", 0 }), SelfieReasonLowQuality},
		{"low similarity", blinked(42), SelfieReasonNoMatch},
	}
	for _, tc := range cases {
		user := uuid.New()
		seedPendingSelfie(t, st, user)
		svc.SetLivenessClient(scripted(tc.result))
		res, err := svc.SubmitSelfie(ctx, user, uuid.New(), newChallenge(t, svc, user))
		if err != nil || res.Status != store.SelfieStatusFailed || res.Passed || res.Reason != tc.wantReason ||
			res.ProfileStatus != store.ProfileStatusPendingSelfie || res.AttemptsRemaining != DefaultSelfieMaxAttempts-1 {
			t.Fatalf("%s: %+v, %v", tc.name, res, err)
		}
		if row := selfieRowOf(t, pool, user); row.status != "failed" {
			t.Fatalf("%s: verification = %+v", tc.name, row)
		}
	}
}

func TestSelfie_HighRiskFirstAttemptGoesToReview(t *testing.T) {
	svc, st, _ := newSelfieSvc(t)
	ctx := context.Background()
	seedRisk := func(id uuid.UUID) {
		if err := st.UpsertAccountRisk(ctx, &store.AccountRisk{UserID: id, RiskScore: 70,
			RiskLevel: store.RiskLevelHideFromDiscovery, Signals: map[string]any{"test": "d5"}}); err != nil {
			t.Fatalf("seed risk: %v", err)
		}
	}
	risky := uuid.New()
	seedPendingSelfie(t, st, risky)
	seedRisk(risky)
	svc.SetLivenessClient(scripted(blinked(98)))
	res, err := svc.SubmitSelfie(ctx, risky, uuid.New(), newChallenge(t, svc, risky))
	if err != nil || res.Status != store.SelfieStatusPendingReview || res.ProfileStatus != store.ProfileStatusPendingSelfie {
		t.Fatalf("high-risk first attempt = %+v, %v; want pending_review", res, err)
	}

	later := uuid.New()
	seedPendingSelfie(t, st, later)
	seedRisk(later)
	svc.SetLivenessClient(scripted(blinked(30), blinked(98)))
	if res, err := svc.SubmitSelfie(ctx, later, uuid.New(), newChallenge(t, svc, later)); err != nil || res.Status != store.SelfieStatusFailed {
		t.Fatalf("first (failing) attempt = %+v, %v", res, err)
	}
	if res, err := svc.SubmitSelfie(ctx, later, uuid.New(), newChallenge(t, svc, later)); err != nil || res.Status != store.SelfieStatusPassed {
		t.Fatalf("second attempt = %+v, %v; want passed", res, err)
	}
}

// NOT_ENOUGH_BLINKS attempts count toward the limit; N+1 is refused.
func TestSelfie_AttemptLimit429OnNPlusOne(t *testing.T) {
	svc, st, _ := newSelfieSvc(t)
	ctx := context.Background()
	user := uuid.New()
	seedPendingSelfie(t, st, user)
	const limit = 3
	svc.SetSelfieConfig(SelfieConfig{PassThreshold: 90, ReviewThreshold: 80, MaxAttemptsPerDay: limit, RequiredBlinks: 2, MaxVideoDurationMs: 4000})
	oneBlink := blinked(95)
	oneBlink.BlinksDetected, oneBlink.Reason = 1, "NOT_ENOUGH_BLINKS"
	live := scripted(oneBlink)
	svc.SetLivenessClient(live)

	challenges := make([]uuid.UUID, limit+1)
	for i := range challenges {
		challenges[i] = newChallenge(t, svc, user)
	}
	for i := 0; i < limit; i++ {
		res, err := svc.SubmitSelfie(ctx, user, uuid.New(), challenges[i])
		if err != nil || res.Reason != SelfieReasonNotEnoughBlinks || res.AttemptsRemaining != limit-1-i {
			t.Fatalf("attempt %d = %+v, %v", i+1, res, err)
		}
	}
	if _, err := svc.SubmitSelfie(ctx, user, uuid.New(), challenges[limit]); !errors.Is(err, store.ErrSelfieAttemptsExceeded) {
		t.Fatalf("attempt %d: err=%v, want ErrSelfieAttemptsExceeded", limit+1, err)
	}
	if live.callCount() != limit {
		t.Fatalf("media-service calls = %d, want %d", live.callCount(), limit)
	}
	if _, err := svc.CreateSelfieChallenge(ctx, user); !errors.Is(err, store.ErrSelfieAttemptsExceeded) {
		t.Fatalf("challenge past the limit: err=%v", err)
	}
}

// An over-long video, an unreadable video, missing media or an unavailable
// provider is no verdict: nothing is marked failed, but the consumed challenge
// counts toward the limit.
func TestSelfie_RefusalsAreNoVerdict(t *testing.T) {
	svc, st, pool := newSelfieSvc(t)
	ctx := context.Background()
	tooLong := blinked(99)
	tooLong.Reason, tooLong.DurationMs, tooLong.BlinksDetected = "VIDEO_TOO_LONG", 6200, 0
	cases := map[string]struct {
		client     *fakeLiveness
		wantErr    error
		wantReason string
	}{
		"video too long":   {scripted(tooLong), ErrSelfieVideoTooLong, "VIDEO_TOO_LONG"},
		"video unreadable": {&fakeLiveness{err: ErrSelfieVideoUnsupported}, ErrSelfieVideoUnsupported, "VIDEO_UNSUPPORTED"},
		"media not found":  {&fakeLiveness{err: ErrSelfieMediaNotFound}, ErrSelfieMediaNotFound, "MEDIA_NOT_FOUND"},
		"unavailable":      {&fakeLiveness{err: ErrFaceCompareUnavailable}, ErrFaceCompareUnavailable, "PROVIDER_UNAVAILABLE"},
	}
	for name, tc := range cases {
		user := uuid.New()
		seedPendingSelfie(t, st, user)
		svc.SetLivenessClient(tc.client)
		if _, err := svc.SubmitSelfie(ctx, user, uuid.New(), newChallenge(t, svc, user)); !errors.Is(err, tc.wantErr) {
			t.Fatalf("%s: err=%v", name, err)
		}
		var n int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*)::int FROM dating_verifications WHERE user_id = $1 AND selfie_status IS NOT NULL`, user).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s: a verdict was recorded (%d, %v)", name, n, err)
		}
		var outcome, reason string
		if err := pool.QueryRow(ctx, `SELECT outcome, COALESCE(reason, '') FROM dating_selfie_attempts WHERE user_id = $1`, user).Scan(&outcome, &reason); err != nil ||
			outcome != "error" || reason != tc.wantReason {
			t.Fatalf("%s: attempt = %q/%q, %v", name, outcome, reason, err)
		}
		if s := profileStatusOf(t, st, user); s != store.ProfileStatusPendingSelfie {
			t.Fatalf("%s: status = %s", name, s)
		}
	}
	svc.SetLivenessClient(nil)
	user := uuid.New()
	seedPendingSelfie(t, st, user)
	if _, err := svc.SubmitSelfie(ctx, user, uuid.New(), newChallenge(t, svc, user)); !errors.Is(err, ErrFaceCompareUnavailable) {
		t.Fatalf("no client: err=%v", err)
	}
}

func TestSelfie_PassDoesNotDemoteAadhaar(t *testing.T) {
	svc, st, _ := newSelfieSvc(t)
	ctx := context.Background()
	user := uuid.New()
	seedPendingSelfie(t, st, user)
	if err := st.RecordAadhaarVerification(ctx, user, "ref-d5", "h"); err != nil {
		t.Fatalf("seed aadhaar: %v", err)
	}
	if err := st.UpdateTrustTier(ctx, user, "aadhaar"); err != nil {
		t.Fatalf("seed tier: %v", err)
	}
	svc.SetLivenessClient(scripted(blinked(95)))
	res, err := svc.SubmitSelfie(ctx, user, uuid.New(), newChallenge(t, svc, user))
	if err != nil || res.TrustTier != "aadhaar" || res.ProfileStatus != store.ProfileStatusActive {
		t.Fatalf("pass with aadhaar = %+v, %v", res, err)
	}
}

// D2 onboarding makes the selfie mandatory: a pending_selfie profile is in
// no deck and can neither send nor receive a spark (so it can never reach a
// match or a message). The same profile appears once its selfie passes,
// proving the gate — not the seed — kept it out.
func TestSelfie_PendingSelfieProfileNotInDeckAndCannotSpark(t *testing.T) {
	svc, st, _ := newSelfieSvc(t)
	ctx := context.Background()
	gender := "d5-" + uuid.NewString()[:8]
	viewer, pending, control := uuid.New(), uuid.New(), uuid.New()
	seedActiveProfile(t, st, viewer)
	seedActiveProfile(t, st, control)
	seedPendingSelfie(t, st, pending)
	for _, id := range []uuid.UUID{pending, control} {
		if _, err := st.UpsertProfile(ctx, id, store.UpsertProfileParams{Gender: &gender}); err != nil {
			t.Fatalf("set gender: %v", err)
		}
	}
	if _, err := st.UpsertPreferences(ctx, viewer, store.UpsertPreferencesParams{InterestedInGender: &gender}); err != nil {
		t.Fatalf("set preference: %v", err)
	}
	inDeck := func(resp *PulseResponse, id uuid.UUID) bool {
		for _, c := range resp.Data {
			if c.CandidateID == id {
				return true
			}
		}
		return false
	}
	deck, err := svc.computePulseToday(ctx, viewer)
	if err != nil {
		t.Fatalf("deck: %v", err)
	}
	if !inDeck(deck, control) {
		t.Fatalf("active control missing from the deck (%d cards); the test would prove nothing", len(deck.Data))
	}
	if inDeck(deck, pending) {
		t.Fatalf("pending_selfie profile is in the deck")
	}
	cands, err := st.FetchCandidates(ctx, store.CandidateQuery{ViewerID: viewer, GenderFilter: gender, Limit: 50})
	if err != nil {
		t.Fatalf("fetch candidates: %v", err)
	}
	for _, c := range cands {
		if c.UserID == pending {
			t.Fatalf("pending_selfie profile returned by FetchCandidates")
		}
	}
	if _, _, err := svc.CreateSpark(ctx, pending, viewer, "photo", "0", ""); err == nil {
		t.Fatalf("pending_selfie profile sent a spark")
	}
	if _, _, err := svc.CreateSpark(ctx, viewer, pending, "photo", "0", ""); !errors.Is(err, ErrCandidateUnavailable) {
		t.Fatalf("spark to a pending_selfie profile: err=%v, want ErrCandidateUnavailable", err)
	}

	svc.SetLivenessClient(scripted(blinked(95)))
	if res, err := svc.SubmitSelfie(ctx, pending, uuid.New(), newChallenge(t, svc, pending)); err != nil || res.ProfileStatus != store.ProfileStatusActive {
		t.Fatalf("selfie pass = %+v, %v", res, err)
	}
	after, err := svc.computePulseToday(ctx, viewer)
	if err != nil {
		t.Fatalf("deck after pass: %v", err)
	}
	if !inDeck(after, pending) {
		t.Fatalf("profile missing from the deck after its selfie passed")
	}
	if _, _, err := svc.CreateSpark(ctx, viewer, pending, "photo", "0", ""); err != nil {
		t.Fatalf("spark after the selfie passed: %v", err)
	}
}
