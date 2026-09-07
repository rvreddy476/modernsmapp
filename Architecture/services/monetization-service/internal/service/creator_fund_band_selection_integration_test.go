//go:build integration

package service

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Two bands, one effective_from, one right answer — every time.
//
// SetQualityBand stamps time.Now() on the new row and closes the previous
// one in the same transaction, so two writes in the same instant leave two
// open rows sharing an effective_from. Fixtures that INSERT directly do it
// too: this dev database carries 26 long_video rows, four of them tying on
// 2025-03-05.
//
// Until 2026-09-07 the lookup was `ORDER BY effective_from DESC LIMIT 1`
// with nothing after it. Postgres was free to return either row. That is
// not a cosmetic defect: creator-fund settlement runs on a lag, periods get
// re-settled, and a band decides the multiplier a payout is scaled by — so
// the same period could be priced two ways by two runs, with nothing in the
// data to say which was right.
//
// The test writes a low band and a high band at the same instant and reads
// back repeatedly. Before the fix it passes or fails on physical row order,
// which is the point; after it, the most recently written row wins always.
func TestActiveQualityBandIsDeterministicWhenTwoRowsShareAnEffectiveFrom(t *testing.T) {
	dsn := os.Getenv("MONETIZATION_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("MONETIZATION_POSTGRES_DSN is required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store := postgres.New(pool)

	// content_type is CHECK-constrained to the two real kinds, so this
	// isolates on region instead: a throwaway code no real band uses, which
	// keeps the fixture clear of IN and makes the cleanup exact.
	const contentType = "long_video"
	region := "ZZ" + strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", ""))[:8]
	sharedFrom := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	asOf := sharedFrom.Add(24 * time.Hour)

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx,
			`DELETE FROM monetization_quality_bands WHERE region_code = $1`, region)
	})

	// Insert directly rather than through SetQualityBand: the collision this
	// guards against is precisely the one the setter cannot prevent, because
	// it stamps its own effective_from.
	insert := func(floorBps, ceilingBps int64, createdAt time.Time) uuid.UUID {
		id := uuid.New()
		if _, err := pool.Exec(ctx, `
			INSERT INTO monetization_quality_bands (
				id, content_type, region_code, floor_bps, ceiling_bps,
				pivot_cqs, confidence_impressions, enabled, effective_from,
				created_at
			) VALUES ($1, $2, $3, $4, $5, 0.35, 1000, TRUE, $6, $7)
		`, id, contentType, region, floorBps, ceilingBps, sharedFrom, createdAt); err != nil {
			t.Fatal(err)
		}
		return id
	}

	insert(8500, 12500, sharedFrom)
	// Written second, an hour later, same effective_from. This is the one a
	// human would expect to be in force.
	winner := insert(9000, 11000, sharedFrom.Add(time.Hour))

	// Read it back more than once. A single read can be right by luck; the
	// property being asserted is that it is right every time.
	for i := 0; i < 8; i++ {
		got, err := store.GetActiveQualityBand(ctx, contentType, region, asOf)
		if err != nil {
			t.Fatal(err)
		}
		if got == nil {
			t.Fatalf("read %d: no active band", i)
		}
		if got.ID != winner {
			t.Fatalf("read %d: picked band %s (floor %d, ceiling %d); the most recently written row %s should win every time",
				i, got.ID, got.FloorBps, got.CeilingBps, winner)
		}
	}

	// ListActiveQualityBands takes the same tie and must resolve it the same
	// way, or the admin console shows a band the settlement is not using.
	bands, err := store.ListActiveQualityBands(ctx, asOf)
	if err != nil {
		t.Fatal(err)
	}
	var seen int
	for _, b := range bands {
		if b.RegionCode != region {
			continue
		}
		seen++
		if b.ID != winner {
			t.Fatalf("list picked band %s; GetActiveQualityBand picked %s — the console would show a band settlement is not using", b.ID, winner)
		}
	}
	if seen != 1 {
		t.Fatalf("list returned %d rows for region %s, want exactly 1", seen, region)
	}
}
