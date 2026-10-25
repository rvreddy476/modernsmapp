//go:build integration

package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/user-service/internal/mediaclient"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB-backed proof of the media-claim bookkeeping (media_claims.go,
// migration 010): every delete queues its holders' releases in its own
// transaction, a refused delete queues nothing, the holder lock serialises,
// and the row reads say what the reconcile acts on. TEST_PG_DSN on a "_test"
// database, CI only (reuses channelOwnerIT's guard).

func claimsIT(t *testing.T) (*Store, *pgxpool.Pool) { return channelOwnerIT(t) }

func seedClaimsUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, display_name, created_at, updated_at) VALUES ($1, 'Claims IT', now(), now())`, id); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

func pendingFor(t *testing.T, pool *pgxpool.Pool, kind string, ref uuid.UUID) bool {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM media_claim_pending WHERE claim_kind = $1 AND claim_ref = $2`, kind, ref).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func TestMediaClaimsIT_ChannelDeleteQueuesItsReleasesOnlyForTheOwner(t *testing.T) {
	st, pool := claimsIT(t)
	ctx := context.Background()
	owner := seedClaimsUser(t, pool)
	avatar := uuid.New()
	ch := &Channel{UserID: owner, Handle: "mc" + strings.ReplaceAll(uuid.NewString(), "-", "")[:14], Name: "c", AvatarMediaID: &avatar, CollabStatus: "closed"}
	if err := st.CreateChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}

	if err := st.DeleteChannel(ctx, ch.ID, uuid.New()); err == nil {
		t.Fatal("a non-owner deleted the channel")
	}
	if pendingFor(t, pool, mediaclient.KindChannelAvatar, ch.ID) {
		t.Fatal("a refused delete queued a release of the owner's picture claim")
	}
	if err := st.DeleteChannel(ctx, ch.ID, owner); err != nil {
		t.Fatal(err)
	}
	if !pendingFor(t, pool, mediaclient.KindChannelAvatar, ch.ID) {
		t.Fatal("the delete did not queue the picture's release")
	}
	if pendingFor(t, pool, mediaclient.KindChannelBanner, ch.ID) {
		t.Fatal("a channel with no banner queued a banner release")
	}
	row, err := st.MediaClaimRowFor(ctx, mediaclient.KindChannelAvatar, ch.ID)
	if err != nil || row.Live || len(row.MediaIDs) != 0 {
		t.Fatalf("a deleted channel reads as %+v, %v", row, err)
	}
}

func TestMediaClaimsIT_PortfolioCreateKeepsItsIDAndDeleteQueues(t *testing.T) {
	st, pool := claimsIT(t)
	ctx := context.Background()
	owner := seedClaimsUser(t, pool)
	media := uuid.New()
	item := &PortfolioItem{ID: uuid.New(), UserID: owner, Title: "t", Type: "project", MediaID: &media}
	want := item.ID
	if err := st.CreatePortfolioItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	if item.ID != want {
		t.Fatal("the store replaced the id the claim was taken against")
	}
	row, err := st.MediaClaimRowFor(ctx, mediaclient.KindPortfolioItem, item.ID)
	if err != nil || !row.Live || len(row.MediaIDs) != 1 || row.MediaIDs[0] != media {
		t.Fatalf("row %+v, %v", row, err)
	}
	if _, err := st.DeletePortfolioItem(ctx, item.ID, uuid.New()); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("non-owner delete: %v", err)
	}
	if pendingFor(t, pool, mediaclient.KindPortfolioItem, item.ID) {
		t.Fatal("a refused delete queued a release")
	}
	had, err := st.DeletePortfolioItem(ctx, item.ID, owner)
	if err != nil || !had {
		t.Fatalf("delete: had=%v err=%v", had, err)
	}
	if !pendingFor(t, pool, mediaclient.KindPortfolioItem, item.ID) {
		t.Fatal("the delete did not queue the release")
	}
}

func TestMediaClaimsIT_PageDisableQueuesAndReadsAsDead(t *testing.T) {
	st, pool := claimsIT(t)
	ctx := context.Background()
	owner := seedClaimsUser(t, pool)
	avatar := uuid.New()
	p := &BusinessPage{UserID: owner, PageHandle: "mcp-" + uuid.NewString()[:12], PageName: "p", Category: "shop",
		PageType: "business", AvatarMediaID: avatar.String(), CoverMediaID: "legacy.png"}
	if err := st.CreateBusinessPage(ctx, p); err != nil {
		t.Fatal(err)
	}
	row, err := st.MediaClaimRowFor(ctx, mediaclient.KindBusinessPageAvatar, p.ID)
	if err != nil || !row.Live || len(row.MediaIDs) != 1 || row.MediaIDs[0] != avatar {
		t.Fatalf("live page row %+v, %v", row, err)
	}
	cover, err := st.MediaClaimRowFor(ctx, mediaclient.KindBusinessPageCover, p.ID)
	if err != nil || len(cover.MediaIDs) != 0 || len(cover.Invalid) != 1 {
		t.Fatalf("a non-id cover must be reported, not claimed: %+v, %v", cover, err)
	}

	if err := st.DeleteBusinessPage(ctx, p.ID, uuid.New()); err == nil {
		t.Fatal("a non-owner disabled the page")
	}
	if pendingFor(t, pool, mediaclient.KindBusinessPageAvatar, p.ID) {
		t.Fatal("a refused disable queued a release")
	}
	if err := st.DeleteBusinessPage(ctx, p.ID, owner); err != nil {
		t.Fatal(err)
	}
	if !pendingFor(t, pool, mediaclient.KindBusinessPageAvatar, p.ID) || !pendingFor(t, pool, mediaclient.KindBusinessPageCover, p.ID) {
		t.Fatal("disabling the page did not queue its releases")
	}
	row, _ = st.MediaClaimRowFor(ctx, mediaclient.KindBusinessPageAvatar, p.ID)
	if row.Live {
		t.Fatal("a disabled page reads as live; its claims would be kept forever")
	}

	// The admin path (applyPageStatus) queues too.
	p2 := &BusinessPage{UserID: owner, PageHandle: "mcq-" + uuid.NewString()[:12], PageName: "p", Category: "shop",
		PageType: "business", CoverMediaID: uuid.NewString()}
	if err := st.CreateBusinessPage(ctx, p2); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdatePageStatus(ctx, p2.ID, "disabled", uuid.New(), "test"); err != nil {
		t.Fatal(err)
	}
	if !pendingFor(t, pool, mediaclient.KindBusinessPageCover, p2.ID) {
		t.Fatal("an admin disable did not queue the page's releases")
	}
}

func TestMediaClaimsIT_PurgeQueuesEveryHolder(t *testing.T) {
	st, pool := claimsIT(t)
	ctx := context.Background()
	owner := seedClaimsUser(t, pool)
	banner := uuid.New()
	ch := &Channel{UserID: owner, Handle: "mpg" + strings.ReplaceAll(uuid.NewString(), "-", "")[:14], Name: "c", BannerMediaID: &banner, CollabStatus: "closed"}
	if err := st.CreateChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	media := uuid.New()
	item := &PortfolioItem{UserID: owner, Title: "t", Type: "project", MediaID: &media}
	if err := st.CreatePortfolioItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	if err := st.PurgeUser(ctx, owner); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if !pendingFor(t, pool, mediaclient.KindChannelBanner, ch.ID) || !pendingFor(t, pool, mediaclient.KindPortfolioItem, item.ID) {
		t.Fatal("the purge did not queue the erased rows' releases")
	}
	if err := st.PurgeUser(ctx, owner); err != nil {
		t.Fatalf("a repeated purge must stay idempotent: %v", err)
	}
}

func TestMediaClaimsIT_ClearKeepsAReEnqueuedEntry(t *testing.T) {
	st, pool := claimsIT(t)
	ctx := context.Background()
	ref := uuid.New()
	if err := st.MarkMediaClaimHolderPending(ctx, mediaclient.KindPortfolioItem, ref); err != nil {
		t.Fatal(err)
	}
	var first PendingMediaClaimHolder
	if err := pool.QueryRow(ctx, `SELECT claim_kind, claim_ref, enqueued_at FROM media_claim_pending WHERE claim_ref = $1`, ref).
		Scan(&first.Kind, &first.Ref, &first.EnqueuedAt); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := st.MarkMediaClaimHolderPending(ctx, mediaclient.KindPortfolioItem, ref); err != nil {
		t.Fatal(err)
	}
	if err := st.ClearMediaClaimHolderPending(ctx, first); err != nil {
		t.Fatal(err)
	}
	if !pendingFor(t, pool, mediaclient.KindPortfolioItem, ref) {
		t.Fatal("clearing a settled entry removed one enqueued after it")
	}
	_, _ = pool.Exec(ctx, `DELETE FROM media_claim_pending WHERE claim_ref = $1`, ref)
}

func TestMediaClaimsIT_HolderLockSerialises(t *testing.T) {
	st, _ := claimsIT(t)
	ctx := context.Background()
	ref := uuid.New()
	unlock, err := st.LockMediaHolder(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan struct{})
	go func() {
		u2, err := st.LockMediaHolder(ctx, ref)
		if err == nil {
			u2()
		}
		close(got)
	}()
	select {
	case <-got:
		t.Fatal("a second writer took the holder lock while the first held it")
	case <-time.After(300 * time.Millisecond):
	}
	unlock()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("the lock was not released by unlock")
	}
	// Another holder is not blocked by this one.
	u3, err := st.LockMediaHolder(ctx, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	u3()
}
