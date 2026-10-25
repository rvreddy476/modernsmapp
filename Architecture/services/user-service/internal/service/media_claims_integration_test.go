//go:build integration

package service

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/atpost/user-service/database"
	"github.com/atpost/user-service/internal/mediaclient"
	"github.com/atpost/user-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The claim protocol against real rows: media-service is the claim-keeping
// fake from media_claims_test.go; the channels, items and pages are real, as
// are the holder lock and the pending queue. TEST_PG_DSN on a "_test"
// database, CI only.

func claimsServiceIT(t *testing.T, assets ...*mediaclient.Asset) (*Service, *store.Store, *pgxpool.Pool, *fakeMedia) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping media claim integration tests")
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
	if err := store.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	st := store.New(pool)
	m := newFakeMedia(assets...)
	svc := New(st, nil).WithMedia(m)
	return svc, st, pool, m
}

func TestMediaClaimsIT_ChannelLifecycle(t *testing.T) {
	owner := uuid.New()
	first, second := ready(owner), ready(owner)
	svc, st, pool, m := claimsServiceIT(t, first, second)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, display_name, created_at, updated_at) VALUES ($1, 'o', now(), now())`, owner); err != nil {
		t.Fatal(err)
	}
	ch := &store.Channel{UserID: owner, Handle: "mcl" + strings.ReplaceAll(uuid.NewString(), "-", "")[:14], Name: "c", CollabStatus: "closed"}
	if err := svc.CreateChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	if err := svc.UpdateChannel(ctx, &store.ChannelUpdate{ID: ch.ID, UserID: owner, AvatarMediaID: ptr(first.ID)}); err != nil {
		t.Fatal(err)
	}
	if !m.holds(mediaclient.KindChannelAvatar, ch.ID, first.ID) {
		t.Fatal("the picture is not claimed")
	}
	if err := svc.UpdateChannel(ctx, &store.ChannelUpdate{ID: ch.ID, UserID: owner, AvatarMediaID: ptr(second.ID)}); err != nil {
		t.Fatal(err)
	}
	if m.holds(mediaclient.KindChannelAvatar, ch.ID, first.ID) || !m.holds(mediaclient.KindChannelAvatar, ch.ID, second.ID) {
		t.Fatal("the swap did not move the claim")
	}
	got, _ := st.GetChannelByID(ctx, ch.ID)
	if got == nil || got.AvatarMediaID == nil || *got.AvatarMediaID != second.ID {
		t.Fatalf("row %+v", got)
	}
	if err := svc.DeleteChannel(ctx, ch.ID, owner); err != nil {
		t.Fatal(err)
	}
	if m.holdsAny(mediaclient.KindChannelAvatar, ch.ID) {
		t.Fatal("a deleted channel still pins its picture")
	}
	// The delete's queued entry is settled by the drain and removed.
	if _, err := svc.DrainMediaClaims(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM media_claim_pending WHERE claim_ref = $1`, ch.ID).Scan(&n)
	if n != 0 {
		t.Fatal("the drain left a settled entry queued")
	}
}

// The real advisory lock serialises concurrent edits: the picture the row
// ends up showing is always claimed.
func TestMediaClaimsIT_ConcurrentEditsLeaveTheFinalPictureClaimed(t *testing.T) {
	owner := uuid.New()
	p, x := ready(owner), ready(owner)
	svc, st, pool, m := claimsServiceIT(t, p, x)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, display_name, created_at, updated_at) VALUES ($1, 'o', now(), now())`, owner); err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 10; round++ {
		ch := &store.Channel{UserID: owner, Handle: "mcc" + strings.ReplaceAll(uuid.NewString(), "-", "")[:14], Name: "c", CollabStatus: "closed", AvatarMediaID: ptr(p.ID)}
		if err := svc.CreateChannel(ctx, ch); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for _, id := range []uuid.UUID{x.ID, p.ID, x.ID, p.ID, x.ID} {
			wg.Add(1)
			go func(id uuid.UUID) {
				defer wg.Done()
				_ = svc.UpdateChannel(ctx, &store.ChannelUpdate{ID: ch.ID, UserID: owner, AvatarMediaID: ptr(id)})
			}(id)
		}
		wg.Wait()
		got, err := st.GetChannelByID(ctx, ch.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !m.holds(mediaclient.KindChannelAvatar, ch.ID, *got.AvatarMediaID) {
			t.Fatalf("round %d: the channel shows %s with no claim", round, got.AvatarMediaID)
		}
	}
}

func TestMediaClaimsIT_BusinessPageAndPortfolio(t *testing.T) {
	owner := uuid.New()
	av, item := ready(owner), ready(owner)
	svc, _, pool, m := claimsServiceIT(t, av, item)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, display_name, created_at, updated_at) VALUES ($1, 'o', now(), now())`, owner); err != nil {
		t.Fatal(err)
	}
	p := &store.BusinessPage{UserID: owner, PageHandle: "mcb-" + uuid.NewString()[:12], PageName: "p", Category: "shop",
		PageType: "business", AvatarMediaID: av.ID.String(), Status: "draft"}
	if err := svc.CreateBusinessPage(ctx, p); err != nil {
		t.Fatal(err)
	}
	if !m.holds(mediaclient.KindBusinessPageAvatar, p.ID, av.ID) {
		t.Fatal("page avatar not claimed against the page id")
	}
	if err := svc.DeleteBusinessPage(ctx, p.ID, owner); err != nil {
		t.Fatal(err)
	}
	if m.holdsAny(mediaclient.KindBusinessPageAvatar, p.ID) {
		t.Fatal("a disabled page still pins its avatar")
	}

	it, err := svc.AddPortfolioItem(ctx, owner, "t", "", "project", "", ptr(item.ID), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !m.holds(mediaclient.KindPortfolioItem, it.ID, item.ID) {
		t.Fatal("portfolio media not claimed against the stored id")
	}
	if err := svc.DeletePortfolioItem(ctx, it.ID, owner); err != nil {
		t.Fatal(err)
	}
	if m.holdsAny(mediaclient.KindPortfolioItem, it.ID) {
		t.Fatal("a deleted item still pins its media")
	}
}
