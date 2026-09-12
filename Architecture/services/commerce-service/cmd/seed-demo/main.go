// Command seed-demo puts a small, realistic catalogue into an EMPTY or
// development commerce database so the storefront's home page, deals rail,
// category strip and discount badges have something to draw.
//
//	POSTGRES_DSN=postgres://postgres:postgres@127.0.0.1:5432/commerce_it_test?sslmode=disable \
//	  go run ./cmd/seed-demo --yes
//
// It refuses to run unless BOTH of these hold:
//
//   - --yes is passed, and
//   - the DSN's database name contains "dev" or ends in "_test", OR the
//     exact name is repeated in --allow-db (the compose stack's dev database
//     is `commerce_db`, so that one needs --allow-db=commerce_db). A name
//     containing "prod" is refused whatever the flags say. See guard.go.
//
// It is idempotent: every id is a fixed literal and every insert is ON
// CONFLICT DO NOTHING, so running it twice inserts nothing the second time
// and the summary says so. It does not delete or update rows it finds; to
// change the dataset, edit catalogue.go and delete the demo rows by hand
// (they all sit in the 00000000-0000-4000-8000-0000000de* id block).
//
// What it writes, and why each part exists, is in catalogue.go and seed.go.
// The short version: one approved seller, sixteen products across eight of
// the categories migration 023 seeded, one or two active variants each with
// stock, half of them discounted so discount_pct is non-nil, and three home
// banners. Images are external placeholder URLs in products.source_image_url;
// nothing is written to product_media because those rows reference
// media-service assets that a seeder cannot create.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	var (
		yes     = flag.Bool("yes", false, "actually write; without it the tool only reports what it would do")
		allowDB = flag.String("allow-db", "", "exact database name to seed when it neither contains \"dev\" nor ends in \"_test\"")
		timeout = flag.Duration("timeout", 2*time.Minute, "overall timeout")
	)
	flag.Parse()

	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		fail("POSTGRES_DSN is required")
	}
	name, err := databaseName(dsn)
	if err != nil {
		fail(err.Error())
	}
	if err := allowedDatabase(name, *allowDB); err != nil {
		fail(err.Error())
	}

	cat := catalogue()
	if err := cat.validate(); err != nil {
		fail(err.Error())
	}
	variants := 0
	for _, p := range cat.Products {
		variants += len(p.Variants)
	}
	fmt.Printf("seed-demo: database %q; dataset is 1 seller, %d products, %d variants, %d banners across %d categories\n",
		name, len(cat.Products), variants, len(cat.Banners), len(cat.categorySlugs()))

	if !*yes {
		fmt.Println("seed-demo: --yes not passed; nothing written")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		fail("bad DSN: " + err.Error())
	}
	// One transaction, one connection; a pool is only used because the
	// store's own tests and jobs take one and the shape is familiar.
	cfg.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		fail("connect: " + err.Error())
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		fail("ping: " + err.Error())
	}

	sum, err := seed(ctx, pool, cat)
	if err != nil {
		if errors.Is(err, errRefused) {
			fail(err.Error())
		}
		fail("seed failed, transaction rolled back: " + err.Error())
	}
	fmt.Print(sum.String())
	fmt.Println("seed-demo: done")
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "seed-demo: "+msg)
	os.Exit(1)
}
