//go:build integration

package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/atpost/identity-profile-service/internal/claimsync"
	"github.com/atpost/identity-profile-service/internal/mediaclient"
	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB-backed proof of the media claim protocol (media_claims.go): a profile
// that names an avatar or cover holds a claim on it in media-service from
// before the row is written until after it stops naming it. media-service is
// the claim-keeping fake below; the profile rows are real.
//
//	PROFILE_IT_POSTGRES_DSN=postgres://.../identity_profile_it_test \
//	  go test -tags integration -p 1 ./internal/service/ -run MediaClaimsIT -v

// keepingClaims records claims the way media-service does: one per
// (kind, ref, media), idempotent; ids in missing do not exist.
type keepingClaims struct {
	mu      sync.Mutex
	held    map[string]bool
	missing map[uuid.UUID]bool
}

func newKeepingClaims() *keepingClaims {
	return &keepingClaims{held: map[string]bool{}, missing: map[uuid.UUID]bool{}}
}

func claimKey(kind string, ref, id uuid.UUID) string {
	return kind + "|" + ref.String() + "|" + id.String()
}

func (k *keepingClaims) Claim(_ context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, id := range ids {
		if k.missing[id] {
			return mediaclient.ErrNotFound
		}
	}
	for _, id := range ids {
		k.held[claimKey(kind, ref, id)] = true
	}
	return nil
}

func (k *keepingClaims) release(kind string, ref uuid.UUID, keep func(uuid.UUID) bool) {
	prefix := kind + "|" + ref.String() + "|"
	for key := range k.held {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			if id := uuid.MustParse(key[len(prefix):]); !keep(id) {
				delete(k.held, key)
			}
		}
	}
}

func (k *keepingClaims) Release(_ context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(ids) == 0 {
		k.release(kind, ref, func(uuid.UUID) bool { return false })
		return nil
	}
	for _, id := range ids {
		delete(k.held, claimKey(kind, ref, id))
	}
	return nil
}

func (k *keepingClaims) ReleaseExcept(_ context.Context, kind string, ref uuid.UUID, keep []uuid.UUID) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	keepSet := map[uuid.UUID]bool{}
	for _, id := range keep {
		keepSet[id] = true
	}
	k.release(kind, ref, func(id uuid.UUID) bool { return keepSet[id] })
	return nil
}

func (k *keepingClaims) holds(kind string, ref, id uuid.UUID) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.held[claimKey(kind, ref, id)]
}

func mediaITService(t *testing.T) (*Service, *store.Store, *keepingClaims, *pgxpool.Pool) {
	t.Helper()
	pool := serviceITPool(t)
	svc, st := itService(pool)
	k := newKeepingClaims()
	svc.WithMediaClaims(k)
	return svc, st, k, pool
}

func storedMedia(t *testing.T, st *store.Store, id uuid.UUID) (avatar, cover *uuid.UUID) {
	t.Helper()
	p, err := st.GetProfile(context.Background(), id)
	if err != nil || p == nil {
		t.Fatalf("GetProfile: %v", err)
	}
	return p.AvatarMediaID, p.CoverMediaID
}

func TestMediaClaimsIT_AvatarAndCoverLifecycle(t *testing.T) {
	svc, st, k, pool := mediaITService(t)
	ctx := context.Background()
	user := seedITAccount(t, pool, nil)
	first, second, cover := uuid.New(), uuid.New(), uuid.New()

	if err := svc.UpdateAvatar(ctx, user, first); err != nil {
		t.Fatalf("set avatar: %v", err)
	}
	if !k.holds(mediaclient.KindProfileAvatar, user, first) {
		t.Fatal("a set avatar holds no claim")
	}
	if err := svc.UpdateCover(ctx, user, cover); err != nil {
		t.Fatalf("set cover: %v", err)
	}
	if err := svc.UpdateAvatar(ctx, user, second); err != nil {
		t.Fatalf("replace avatar: %v", err)
	}
	if !k.holds(mediaclient.KindProfileAvatar, user, second) || k.holds(mediaclient.KindProfileAvatar, user, first) {
		t.Fatal("replacing the avatar must claim the new image and release the old one")
	}
	if !k.holds(mediaclient.KindProfileCover, user, cover) {
		t.Fatal("replacing the avatar released the cover")
	}
	a, c := storedMedia(t, st, user)
	if a == nil || *a != second || c == nil || *c != cover {
		t.Fatalf("stored avatar %v cover %v", a, c)
	}
}

// A claim media-service refuses (the image was deleted after it was checked)
// leaves the profile exactly as it was.
func TestMediaClaimsIT_RefusedClaimWritesNothing(t *testing.T) {
	svc, st, k, pool := mediaITService(t)
	ctx := context.Background()
	user := seedITAccount(t, pool, nil)
	kept, gone := uuid.New(), uuid.New()
	if err := svc.UpdateAvatar(ctx, user, kept); err != nil {
		t.Fatal(err)
	}
	k.missing[gone] = true
	if err := svc.UpdateAvatar(ctx, user, gone); !errors.Is(err, ErrMediaNotFound) {
		t.Fatalf("err = %v, want ErrMediaNotFound", err)
	}
	if a, _ := storedMedia(t, st, user); a == nil || *a != kept {
		t.Fatalf("stored avatar %v after a refused claim; want %s", a, kept)
	}
	if !k.holds(mediaclient.KindProfileAvatar, user, kept) {
		t.Fatal("a refused claim released the avatar the profile still shows")
	}
}

func TestMediaClaimsIT_NoProfileClaimsNothing(t *testing.T) {
	svc, _, k, _ := mediaITService(t)
	nobody, id := uuid.New(), uuid.New()
	if err := svc.UpdateAvatar(context.Background(), nobody, id); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("err = %v, want ErrProfileNotFound", err)
	}
	if k.holds(mediaclient.KindProfileAvatar, nobody, id) {
		t.Fatal("a profile that does not exist was left holding a claim")
	}
}

// The per-profile lock is real: a save waits while another holds it.
func TestMediaClaimsIT_HolderLockSerialises(t *testing.T) {
	svc, st, _, pool := mediaITService(t)
	ctx := context.Background()
	user := seedITAccount(t, pool, nil)
	unlock, err := st.LockMediaHolder(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- svc.UpdateAvatar(ctx, user, uuid.New()) }()
	select {
	case err := <-done:
		unlock()
		t.Fatalf("the save did not wait for the holder lock (err %v)", err)
	case <-time.After(300 * time.Millisecond):
	}
	unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the save never got the lock after it was released")
	}
}

// Backfill and repair: a pre-protocol avatar is claimed; a stale claim (a
// release lost after a change) is dropped; a cleared column keeps nothing.
func TestMediaClaimsIT_ReconcileBackfillsAndRepairs(t *testing.T) {
	_, st, k, pool := mediaITService(t)
	ctx := context.Background()
	user := seedITAccount(t, pool, nil)
	avatar, cover, stale := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx,
		`UPDATE profile.profiles SET avatar_media_id = $2, cover_media_id = $3 WHERE user_id = $1`,
		user, avatar, cover); err != nil {
		t.Fatal(err)
	}
	_ = k.Claim(ctx, mediaclient.KindProfileAvatar, user, []uuid.UUID{stale})

	dry, err := claimsync.Run(ctx, st, k, false)
	if err != nil {
		t.Fatal(err)
	}
	if dry.Rows < 2 || k.holds(mediaclient.KindProfileAvatar, user, avatar) {
		t.Fatalf("dry run %s sent a claim, or did not see the profile", dry)
	}

	rep, err := claimsync.Run(ctx, st, k, true)
	if err != nil || rep.Failed != 0 {
		t.Fatalf("apply: %s, %v", rep, err)
	}
	if !k.holds(mediaclient.KindProfileAvatar, user, avatar) || !k.holds(mediaclient.KindProfileCover, user, cover) {
		t.Fatal("the backfill did not claim the profile's images")
	}
	if k.holds(mediaclient.KindProfileAvatar, user, stale) {
		t.Fatal("the repair kept a claim the profile no longer holds")
	}

	if _, err := pool.Exec(ctx, `UPDATE profile.profiles SET cover_media_id = NULL WHERE user_id = $1`, user); err != nil {
		t.Fatal(err)
	}
	row, err := st.MediaClaimRowFor(ctx, mediaclient.KindProfileCover, user)
	if err != nil || !row.Live || len(row.MediaIDs) != 0 {
		t.Fatalf("re-read of a cleared cover = %+v, %v", row, err)
	}
	var one claimsync.Report
	claimsync.Reconcile(ctx, st, k, []store.MediaClaimRow{row}, true, &one)
	if k.holds(mediaclient.KindProfileCover, user, cover) || one.Released != 1 {
		t.Fatalf("a cleared cover kept its claim (%s)", one)
	}
	if gone, err := st.MediaClaimRowFor(ctx, mediaclient.KindProfileAvatar, uuid.New()); err != nil || gone.Live {
		t.Fatalf("a missing profile re-read as %+v, %v", gone, err)
	}
}
