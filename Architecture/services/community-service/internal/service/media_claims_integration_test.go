// DB-backed proof of the media claim protocol for communities
// (media_claims.go): a community's avatar and banner hold a claim in
// media-service from before the row names them until after it stops. The
// media-service side is the claim-keeping fake from media_claims_test.go; the
// community rows are real. TEST_PG_DSN on a "_test" database, CI only.
package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/community-service/database"
	"github.com/atpost/community-service/internal/claimsync"
	"github.com/atpost/community-service/internal/mediaclaims"
	"github.com/atpost/community-service/internal/mediaclient"
	"github.com/atpost/community-service/internal/store"
	pgstore "github.com/atpost/community-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupMediaClaimsIT(t *testing.T) (*Service, *store.Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping community-service media claim integration tests")
	}
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatal("TEST_PG_DSN must parse and name a database ending in _test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pgstore.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	st := store.New(pool)
	return New(st, nil), st, pool
}

func itHandle() string { return "mc" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16] }

func TestMediaClaimsIT_CommunityLifecycle(t *testing.T) {
	svc, _, _ := setupMediaClaimsIT(t)
	ctx := context.Background()
	owner := uuid.New()
	avatar, banner, next := readyAsset(owner), readyAsset(owner), readyAsset(owner)
	f := newFakeMedia(avatar, banner, next)
	svc.WithMedia(mediaclaims.New(f, f))

	c, err := svc.CreateCommunity(ctx, owner, CreateCommunityParams{
		Handle: itHandle(), Name: "claims", AvatarMediaID: &avatar.ID, BannerMediaID: &banner.ID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !f.holds(mediaclient.KindCommunityAvatar, c.ID, avatar.ID) || !f.holds(mediaclient.KindCommunityBanner, c.ID, banner.ID) {
		t.Fatalf("a created community does not hold claims on its media: %v", f.held)
	}

	if _, err := svc.UpdateCommunity(ctx, c.ID, owner, UpdateCommunityParams{AvatarMediaID: &next.ID}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if !f.holds(mediaclient.KindCommunityAvatar, c.ID, next.ID) || f.holds(mediaclient.KindCommunityAvatar, c.ID, avatar.ID) {
		t.Fatalf("after replacing the avatar: %v", f.held)
	}
	if !f.holds(mediaclient.KindCommunityBanner, c.ID, banner.ID) {
		t.Fatal("a save that did not touch the banner released it")
	}

	if err := svc.DeleteCommunity(ctx, c.ID, owner); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(f.held) != 0 {
		t.Fatalf("a deleted community still holds media: %v", f.held)
	}
}

// Another user's asset, or one the owner deleted, is never written.
func TestMediaClaimsIT_RefusedAttachWritesNothing(t *testing.T) {
	svc, st, _ := setupMediaClaimsIT(t)
	ctx := context.Background()
	owner := uuid.New()
	mine, theirs := readyAsset(owner), readyAsset(uuid.New())
	f := newFakeMedia(mine, theirs)
	svc.WithMedia(mediaclaims.New(f, f))

	c, err := svc.CreateCommunity(ctx, owner, CreateCommunityParams{Handle: itHandle(), Name: "claims", AvatarMediaID: &mine.ID})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.UpdateCommunity(ctx, c.ID, owner, UpdateCommunityParams{BannerMediaID: &theirs.ID}); !errors.Is(err, mediaclaims.ErrMediaNotOwned) {
		t.Fatalf("someone else's banner = %v", err)
	}
	gone := uuid.New()
	if _, err := svc.UpdateCommunity(ctx, c.ID, owner, UpdateCommunityParams{BannerMediaID: &gone}); !errors.Is(err, mediaclaims.ErrMediaNotFound) {
		t.Fatalf("a missing banner = %v", err)
	}
	got, err := st.GetCommunityByID(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.BannerMediaID != nil {
		t.Fatal("a refused banner was written")
	}

	// Unconfigured: a save that adds media is refused, one that adds none is not.
	svc.WithMedia(nil)
	if _, err := svc.CreateCommunity(ctx, owner, CreateCommunityParams{Handle: itHandle(), Name: "x", AvatarMediaID: &mine.ID}); !errors.Is(err, mediaclaims.ErrMediaUnavailable) {
		t.Fatalf("create with no media-service = %v", err)
	}
	name := "renamed"
	if _, err := svc.UpdateCommunity(ctx, c.ID, owner, UpdateCommunityParams{Name: &name}); err != nil {
		t.Fatalf("a save without new media needed media-service: %v", err)
	}
}

func TestMediaClaimsIT_ReconcileBackfillsAndReleases(t *testing.T) {
	_, st, pool := setupMediaClaimsIT(t)
	ctx := context.Background()
	owner := uuid.New()
	avatar := readyAsset(owner)
	f := newFakeMedia(avatar)
	var id uuid.UUID
	// A row written before the protocol: it names media with no claim.
	if err := pool.QueryRow(ctx, `INSERT INTO communities (owner_id, handle, name, avatar_media_id) VALUES ($1, $2, 'legacy', $3) RETURNING id`,
		owner, itHandle(), avatar.ID).Scan(&id); err != nil {
		t.Fatalf("seed: %v", err)
	}
	pagers := []claimsync.Pager{st.CommunityMediaClaimRows}
	if _, err := claimsync.Run(ctx, st, f, pagers, false); err != nil || len(f.held) != 0 {
		t.Fatalf("a dry run sent claims: %v %v", err, f.held)
	}
	if rep, err := claimsync.Run(ctx, st, f, pagers, true); err != nil || rep.Failed != 0 {
		t.Fatalf("apply: %v %s", err, rep)
	}
	if !f.holds(mediaclient.KindCommunityAvatar, id, avatar.ID) {
		t.Fatal("the backfill did not claim a legacy community's avatar")
	}
	if _, err := pool.Exec(ctx, `UPDATE communities SET status = 'deleted' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := claimsync.Run(ctx, st, f, pagers, true); err != nil {
		t.Fatal(err)
	}
	if f.holds(mediaclient.KindCommunityAvatar, id, avatar.ID) {
		t.Fatal("the repair did not release a deleted community's avatar")
	}
}

// The second writer of one row waits for the first; another row does not.
func TestMediaClaimsIT_HolderLockSerialises(t *testing.T) {
	_, st, _ := setupMediaClaimsIT(t)
	ctx := context.Background()
	ref := uuid.New()
	unlock, err := st.LockMediaHolder(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.LockMediaHolder(ctx, uuid.New())
	if err != nil {
		t.Fatalf("a different row's lock waited: %v", err)
	}
	other()
	got := make(chan struct{})
	go func() {
		u, err := st.LockMediaHolder(ctx, ref)
		if err == nil {
			u()
		}
		close(got)
	}()
	select {
	case <-got:
		t.Fatal("a second writer took the lock while the first held it")
	case <-time.After(300 * time.Millisecond):
	}
	unlock()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("the lock was not freed by unlock")
	}
}
