//go:build integration

package postgres

// The media holder queries the claim backfill reads, against a real Postgres
// (MONETIZATION_POSTGRES_DSN on a *_test database, CI only).

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/monetization-service/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func holdersStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	dsn := requireScratchDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	return New(pool), pool
}

func TestIntegrationMediaHolderRowsListBothColumns(t *testing.T) {
	s, pool := holdersStore(t)
	ctx := context.Background()
	cover, pdf := uuid.New(), uuid.New()
	var withCover, withoutCover, withPDF uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO fundraisers (creator_id, type, title, description, cover_media_id, goal_amount)
		VALUES ($1, 'personal', 'it', 'it', $2, 10) RETURNING id`, uuid.New(), cover).Scan(&withCover); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO fundraisers (creator_id, type, title, description, goal_amount)
		VALUES ($1, 'personal', 'it', 'it', 10) RETURNING id`, uuid.New()).Scan(&withoutCover); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO invoices (user_id, invoice_type, amount_paise, total_paise, pdf_media_id)
		VALUES ($1, 'payout', 100, 100, $2) RETURNING id`, uuid.New(), pdf).Scan(&withPDF); err != nil {
		t.Fatal(err)
	}

	list := func(table MediaHolderTable, all bool) map[uuid.UUID]*uuid.UUID {
		out := map[uuid.UUID]*uuid.UUID{}
		after := uuid.Nil
		for {
			rows, err := s.MediaHolderRows(ctx, table, after, 200, all)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rows {
				out[r.ID] = r.MediaID
			}
			if len(rows) < 200 {
				return out
			}
			after = rows[len(rows)-1].ID
		}
	}
	f := list(HolderFundraiserCover, false)
	if c := f[withCover]; c == nil || *c != cover {
		t.Fatalf("the fundraiser's cover was not listed: %v", c)
	}
	if _, ok := f[withoutCover]; ok {
		t.Fatal("a fundraiser without a cover was listed without -all")
	}
	if _, ok := list(HolderFundraiserCover, true)[withoutCover]; !ok {
		t.Fatal("-all did not list the fundraiser without a cover")
	}
	if p := list(HolderInvoicePDF, false)[withPDF]; p == nil || *p != pdf {
		t.Fatalf("the invoice PDF was not listed: %v", p)
	}

	row, err := s.MediaHolderRowFor(ctx, HolderInvoicePDF, withPDF)
	if err != nil || row.MediaID == nil || *row.MediaID != pdf {
		t.Fatalf("re-read = %+v, %v", row, err)
	}
	gone, err := s.MediaHolderRowFor(ctx, HolderFundraiserCover, uuid.New())
	if err != nil || gone.MediaID != nil {
		t.Fatalf("a missing row = %+v, %v", gone, err)
	}

	// The real create refuses a media id, against the real table too.
	if _, err := s.CreateFundraiser(ctx, &Fundraiser{CreatorID: uuid.New(), Type: "personal", Title: "t",
		Description: "d", GoalAmount: 1, Currency: "INR", CoverMediaID: &cover}); !errors.Is(err, ErrMediaClaimRequired) {
		t.Fatalf("create with a cover = %v", err)
	}
}

func TestIntegrationMediaHolderLockSerialises(t *testing.T) {
	s, _ := holdersStore(t)
	ctx := context.Background()
	id := uuid.New()
	unlock, err := s.LockMediaHolder(ctx, HolderInvoicePDF, id)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.LockMediaHolder(ctx, HolderFundraiserCover, id)
	if err != nil {
		t.Fatalf("the same id in another column waited: %v", err)
	}
	other()
	got := make(chan struct{})
	go func() {
		if u, err := s.LockMediaHolder(ctx, HolderInvoicePDF, id); err == nil {
			u()
		}
		close(got)
	}()
	select {
	case <-got:
		unlock()
		t.Fatal("a second holder of the same row did not wait")
	case <-time.After(300 * time.Millisecond):
	}
	unlock()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("unlock did not release the lock")
	}
}
