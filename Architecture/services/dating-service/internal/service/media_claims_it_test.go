// DB-backed proof of the media claim protocol (media_claims.go): every dating
// row that names media holds a claim on it in media-service from before the
// row is written until it stops naming it, and a holder's claims are released
// before the holder is deleted. media-service is the claim-keeping fake; the
// rows are real. TEST_PG_DSN on a "_test" database, CI only.
package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/dating-service/internal/mediaclient"
	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMediaClaimsIT_PhotoLifecycle(t *testing.T) {
	e := newPhotoEnv(t)
	ctx := context.Background()
	owner := uuid.New()
	seedAtPendingPhoto(t, e.st, owner)
	media := e.media.add(owner, nil)

	photo, err := e.svc.CreatePhoto(ctx, owner, store.CreatePhotoParams{MediaID: media, Visibility: "public"})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	ref := photo.ID.String()
	if !e.claims.holds(mediaclient.KindPhoto, ref, media) {
		t.Fatal("an attached photo does not hold a claim on its media (ref must be the row id)")
	}

	heldAtPurge := -1
	e.media.onDelete = func(uuid.UUID) { heldAtPurge = e.claims.count(mediaclient.KindPhoto, ref) }
	if err := e.svc.DeletePhoto(ctx, owner, photo.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if heldAtPurge != 0 {
		t.Fatalf("the media purge was asked with %d claims still held; media-service refuses a claimed asset", heldAtPurge)
	}
	if e.claims.count(mediaclient.KindPhoto, ref) != 0 {
		t.Fatal("a deleted photo still holds its media")
	}
}

func TestMediaClaimsIT_PhotoAttachRefusedWithoutAClaim(t *testing.T) {
	e := newPhotoEnv(t)
	ctx := context.Background()
	owner := uuid.New()
	seedAtPendingPhoto(t, e.st, owner)

	gone := e.media.add(owner, nil)
	e.claims.gone[gone] = true // the owner deleted it after the readiness check
	if _, err := e.svc.CreatePhoto(ctx, owner, store.CreatePhotoParams{MediaID: gone}); !errors.Is(err, ErrPhotoMediaNotFound) {
		t.Fatalf("claim refused: err=%v, want ErrPhotoMediaNotFound", err)
	}
	e.claims.claimErr = mediaclient.ErrUnavailable
	if _, err := e.svc.CreatePhoto(ctx, owner, store.CreatePhotoParams{MediaID: e.media.add(owner, nil)}); !errors.Is(err, ErrPhotoMediaUnavailable) {
		t.Fatalf("media-service down: err=%v, want ErrPhotoMediaUnavailable", err)
	}
	e.claims.claimErr = nil
	e.svc.SetMediaClaims(nil)
	if _, err := e.svc.CreatePhoto(ctx, owner, store.CreatePhotoParams{MediaID: e.media.add(owner, nil)}); !errors.Is(err, ErrPhotoMediaUnavailable) {
		t.Fatalf("no claim client: err=%v, want ErrPhotoMediaUnavailable", err)
	}
	if photos, _ := e.st.ListPhotos(ctx, owner); len(photos) != 0 {
		t.Fatalf("%d photos written without a claim", len(photos))
	}
}

// A delete media-service cannot confirm the release of is refused: once the
// row is gone nothing would name the claim. A media purge that fails takes
// the claim back.
func TestMediaClaimsIT_PhotoDeleteKeepsTheClaimWhileThePhotoStays(t *testing.T) {
	e := newPhotoEnv(t)
	ctx := context.Background()
	owner := uuid.New()
	seedAtPendingPhoto(t, e.st, owner)
	media := e.media.add(owner, nil)
	photo, err := e.svc.CreatePhoto(ctx, owner, store.CreatePhotoParams{MediaID: media})
	if err != nil {
		t.Fatal(err)
	}
	ref := photo.ID.String()

	e.claims.releaseErr = mediaclient.ErrUnavailable
	if err := e.svc.DeletePhoto(ctx, owner, photo.ID); !errors.Is(err, ErrPhotoMediaUnavailable) {
		t.Fatalf("release down: err=%v, want ErrPhotoMediaUnavailable", err)
	}
	if _, deletes := e.media.counts(); deletes != 0 {
		t.Fatal("the media purge ran although the claim was not released")
	}
	e.claims.releaseErr = nil

	e.media.deleteErr = ErrPhotoMediaUnavailable
	if err := e.svc.DeletePhoto(ctx, owner, photo.ID); !errors.Is(err, ErrPhotoMediaUnavailable) {
		t.Fatalf("purge down: err=%v", err)
	}
	if _, err := e.st.GetPhotoForUser(ctx, owner, photo.ID); err != nil {
		t.Fatalf("the photo went although its media purge failed: %v", err)
	}
	if !e.claims.holds(mediaclient.KindPhoto, ref, media) {
		t.Fatal("the photo stays but its claim was not taken back")
	}
}

func selfieAttemptIDs(t *testing.T, pool *pgxpool.Pool, user uuid.UUID) []uuid.UUID {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT id FROM dating_selfie_attempts WHERE user_id = $1 ORDER BY created_at`, user)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	return out
}

func TestMediaClaimsIT_SelfieClaimsTheAttemptAndTheVerification(t *testing.T) {
	svc, st, pool := newSelfieSvc(t)
	ctx := context.Background()
	claims := newFakeMediaClaims()
	svc.SetMediaClaims(claims)
	user := uuid.New()
	seedPendingSelfie(t, st, user)

	// A failed verdict is written to the verification row with its video.
	first := uuid.New()
	svc.SetLivenessClient(scripted(blinked(10)))
	if res, err := svc.SubmitSelfie(ctx, user, first, newChallenge(t, svc, user)); err != nil || res.Passed {
		t.Fatalf("first: %+v %v", res, err)
	}
	ver := user.String()
	if !claims.holds(mediaclient.KindVerificationSelfie, ver, first) {
		t.Fatal("the verification row names the video but holds no claim on it")
	}

	// A pass replaces it: the new video is held, the replaced one released.
	second := uuid.New()
	svc.SetLivenessClient(scripted(blinked(97)))
	if res, err := svc.SubmitSelfie(ctx, user, second, newChallenge(t, svc, user)); err != nil || !res.Passed {
		t.Fatalf("second: %+v %v", res, err)
	}
	if !claims.holds(mediaclient.KindVerificationSelfie, ver, second) || claims.holds(mediaclient.KindVerificationSelfie, ver, first) {
		t.Fatalf("verification claims after the pass: %v", claims.held)
	}
	ids := selfieAttemptIDs(t, pool, user)
	if len(ids) != 2 || !claims.holds(mediaclient.KindSelfieAttempt, ids[0].String(), first) ||
		!claims.holds(mediaclient.KindSelfieAttempt, ids[1].String(), second) {
		t.Fatalf("each attempt must hold its own video: attempts %v, claims %v", ids, claims.held)
	}
}

// Without proof the video is the caller's, the attempt does not keep a claim:
// otherwise anyone could pin another user's media by submitting its id.
func TestMediaClaimsIT_AnUnconfirmedSelfieVideoIsNotPinned(t *testing.T) {
	svc, st, pool := newSelfieSvc(t)
	ctx := context.Background()
	claims := newFakeMediaClaims()
	svc.SetMediaClaims(claims)
	user := uuid.New()
	seedPendingSelfie(t, st, user)
	foreign := uuid.New()
	svc.SetLivenessClient(&fakeLiveness{err: ErrSelfieMediaNotFound})
	if _, err := svc.SubmitSelfie(ctx, user, foreign, newChallenge(t, svc, user)); !errors.Is(err, ErrSelfieMediaNotFound) {
		t.Fatalf("err=%v, want ErrSelfieMediaNotFound", err)
	}
	ids := selfieAttemptIDs(t, pool, user)
	if len(ids) != 1 {
		t.Fatalf("attempts = %v", ids)
	}
	if claims.count(mediaclient.KindSelfieAttempt, ids[0].String()) != 0 {
		t.Fatal("an attempt whose video media-service would not confirm still pins it")
	}
	if claims.count(mediaclient.KindVerificationSelfie, user.String()) != 0 {
		t.Fatal("a no-verdict attempt claimed the verification row")
	}

	// No claim client: the selfie is refused before an attempt is spent.
	svc.SetMediaClaims(nil)
	svc.SetLivenessClient(scripted(blinked(97)))
	if _, err := svc.SubmitSelfie(ctx, user, uuid.New(), newChallenge(t, svc, user)); !errors.Is(err, ErrFaceCompareUnavailable) {
		t.Fatalf("no claim client: err=%v, want ErrFaceCompareUnavailable", err)
	}
	if got := selfieAttemptIDs(t, pool, user); len(got) != 1 {
		t.Fatalf("a refused selfie spent an attempt: %v", got)
	}
}

func TestMediaClaimsIT_PurgesReleaseBeforeErasing(t *testing.T) {
	svc, st, pool := newSelfieSvc(t)
	ctx := context.Background()
	claims := newFakeMediaClaims()
	svc.SetMediaClaims(claims)
	user := uuid.New()
	seedPendingSelfie(t, st, user)
	video := uuid.New()
	svc.SetLivenessClient(scripted(blinked(97)))
	if _, err := svc.SubmitSelfie(ctx, user, video, newChallenge(t, svc, user)); err != nil {
		t.Fatal(err)
	}
	photos, err := st.ListPhotos(ctx, user)
	if err != nil || len(photos) == 0 {
		t.Fatalf("seeded photos: %v %v", photos, err)
	}
	photoRef := photos[0].ID.String()
	claims.held[claimKey(mediaclient.KindPhoto, photoRef)] = map[uuid.UUID]bool{photos[0].MediaID: true}

	// media-service down: the purge fails and erases nothing (it is retried).
	claims.releaseErr = mediaclient.ErrUnavailable
	if err := svc.PurgeProfile(ctx, user); err == nil {
		t.Fatal("a purge whose claims could not be released succeeded")
	}
	if left, _ := st.ListPhotos(ctx, user); len(left) == 0 {
		t.Fatal("the purge erased rows although their claims were not released")
	}
	claims.releaseErr = nil

	if err := svc.PurgeProfile(ctx, user); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if claims.count(mediaclient.KindPhoto, photoRef) != 0 || claims.count(mediaclient.KindVerificationSelfie, user.String()) != 0 {
		t.Fatalf("claims after the profile purge: %v", claims.held)
	}
	attempts := selfieAttemptIDs(t, pool, user)
	if len(attempts) != 1 || claims.count(mediaclient.KindSelfieAttempt, attempts[0].String()) != 1 {
		t.Fatalf("the profile purge touched the attempts (they are the auxiliary erase's): %v", claims.held)
	}
	if err := svc.PurgeUserAuxiliary(ctx, user); err != nil {
		t.Fatalf("aux purge: %v", err)
	}
	if claims.count(mediaclient.KindSelfieAttempt, attempts[0].String()) != 0 {
		t.Fatal("a purged attempt still holds its video")
	}
	if left := selfieAttemptIDs(t, pool, user); len(left) != 0 {
		t.Fatalf("attempts not erased: %v", left)
	}
}
