//go:build integration

package main

// The seeder against a real schema, twice.
//
//	COMMERCE_TEST_DSN=postgres://postgres:postgres@127.0.0.1:5432/commerce_it_test?sslmode=disable \
//	  go test -tags=integration ./cmd/seed-demo/ -v
//
// Two things a unit test cannot prove: that every column the INSERTs name
// exists with the type the value has (a migration renaming one is a failure
// here and nowhere else), and that the rows land on the storefront's own
// queries: DealProducts, ListCategoryCards, LiveBanners. The second run is
// the idempotency claim made good: it must insert nothing.
//
// The guard is the seeder's own: COMMERCE_TEST_DSN has to name a database
// the rule allows, and this test does not pass --allow-db, so commerce_db is
// refused here exactly as it is on the command line.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("COMMERCE_TEST_DSN")
	if dsn == "" {
		t.Skip("COMMERCE_TEST_DSN not set")
	}
	name, err := databaseName(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := allowedDatabase(name, ""); err != nil {
		t.Fatalf("COMMERCE_TEST_DSN: %v", err)
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestSeedIsIdempotentAndLandsOnTheStorefront(t *testing.T) {
	pool := testPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cat := catalogue()

	// A previous run may have left the rows, so the first run's tally is
	// not asserted; only that it ran. The second run is the assertion.
	if _, err := seed(ctx, pool, cat); err != nil {
		t.Fatalf("first run: %v", err)
	}
	second, err := seed(ctx, pool, cat)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	for table, n := range second.inserted {
		if n != 0 {
			t.Errorf("second run inserted %d rows into %s; the seed is not idempotent", n, table)
		}
	}

	store := postgres.New(pool)

	// Every demo product must be visible to a shopper: the offer is live,
	// the seller is active, there is a priced variant and stock. The
	// storefront's summary projection is the judge.
	demo := map[string]demoProduct{}
	for _, p := range cat.Products {
		demo[p.Slug] = p
	}
	arrivals, err := store.NewArrivalProducts(ctx, 500)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]*postgres.Product{}
	for _, p := range arrivals {
		if _, ours := demo[p.Slug]; ours {
			seen[p.Slug] = p
		}
	}
	for slug, want := range demo {
		got, ok := seen[slug]
		if !ok {
			t.Errorf("%s is not on the storefront (offer, seller status, variant or stock is wrong)", slug)
			continue
		}
		if got.InStock == nil || !*got.InStock {
			t.Errorf("%s is out of stock on the grid", slug)
		}
		if got.MinPriceMinor == nil || *got.MinPriceMinor != want.Variants[0].PriceMinor {
			t.Errorf("%s grid price = %v, want %d paise", slug, got.MinPriceMinor, want.Variants[0].PriceMinor)
		}
		if got.SourceImageURL == nil || *got.SourceImageURL == "" {
			t.Errorf("%s has no source_image_url", slug)
		}
		pct := postgres.DiscountPct(got.MinPriceMinor, got.MRPMinor)
		if want.hasDiscount() != (pct != nil) {
			t.Errorf("%s discount_pct = %v, want discounted=%v", slug, pct, want.hasDiscount())
		}
	}

	// "Deals of the day" is built from price < mrp on the cheapest variant.
	deals, err := store.DealProducts(ctx, 500)
	if err != nil {
		t.Fatal(err)
	}
	dealSlugs := map[string]bool{}
	for _, p := range deals {
		dealSlugs[p.Slug] = true
	}
	for _, p := range cat.Products {
		if p.hasDiscount() && !dealSlugs[p.Slug] {
			t.Errorf("%s is discounted but missing from DealProducts", p.Slug)
		}
		if !p.hasDiscount() && dealSlugs[p.Slug] {
			t.Errorf("%s is at MRP but listed in DealProducts", p.Slug)
		}
	}

	// The category strip's product_count must be non-zero for every
	// category the dataset touches.
	cards, err := store.ListCategoryCards(ctx)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, c := range cards {
		counts[c.Slug] = c.ProductCount
	}
	for _, slug := range cat.categorySlugs() {
		if counts[slug] == 0 {
			t.Errorf("category %s shows product_count 0", slug)
		}
	}

	// The three banners are live.
	banners, err := store.LiveBanners(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	live := map[string]bool{}
	for _, b := range banners {
		live[b.ID.String()] = true
	}
	for _, b := range cat.Banners {
		if !live[b.ID.String()] {
			t.Errorf("banner %q is not live", b.Title)
		}
	}
}
