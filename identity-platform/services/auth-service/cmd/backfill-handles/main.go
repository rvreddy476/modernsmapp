// Command backfill-handles gives a public handle to accounts created before
// registration started assigning one.
//
// Until 22 September 2026 registration wrote no username: every account
// created before then has profile.profiles.username = NULL and therefore no
// /u/<handle> address. This walks those rows and assigns handles by exactly
// the same rules a new registration uses — same derivation, same reserved
// and profanity lists, same uniqueness arbitration against the partial unique
// index — so a backfilled account is indistinguishable from a fresh one.
//
// It is a dry run unless -apply is passed. The dry run reports what it WOULD
// assign, which is the number to read before changing anyone's public
// address.
//
// It does not touch app.users. user-service's reconcile job copies username
// from profile.profiles on its own schedule (internal/reconcile), which is
// the path a normal registration takes too.
//
// Email addresses are never printed. The handle derived from one is, because
// that handle is about to become public anyway.
//
// Usage:
//
//	DATABASE_URL=... go run ./cmd/backfill-handles            # dry run
//	DATABASE_URL=... go run ./cmd/backfill-handles -apply
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/atpost/identity-auth-service/internal/handle"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type candidate struct {
	userID    uuid.UUID
	email     string
	firstName string
	lastName  string
}

func main() {
	apply := flag.Bool("apply", false, "write the handles (default: dry run)")
	limit := flag.Int("limit", 0, "stop after N accounts (0 = all)")
	flag.Parse()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("POSTGRES_DSN")
	}
	if dsn == "" {
		log.Fatal("DATABASE_URL (or POSTGRES_DSN) is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	rows, err := pool.Query(ctx, `
		SELECT p.user_id, COALESCE(u.email, ''), COALESCE(p.first_name, ''), COALESCE(p.last_name, '')
		FROM profile.profiles p
		JOIN auth.users u ON u.user_id = p.user_id
		WHERE p.username IS NULL
		ORDER BY p.created_at
	`)
	if err != nil {
		log.Fatalf("query: %v", err)
	}
	var todo []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.userID, &c.email, &c.firstName, &c.lastName); err != nil {
			rows.Close()
			log.Fatalf("scan: %v", err)
		}
		todo = append(todo, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Fatalf("iterate: %v", err)
	}
	if *limit > 0 && len(todo) > *limit {
		todo = todo[:*limit]
	}

	mode := "DRY RUN — nothing is written"
	if *apply {
		mode = "APPLYING"
	}
	fmt.Printf("%s: %d account(s) with no handle\n\n", mode, len(todo))

	// A dry run must not report two accounts the same handle, so it tracks
	// what it would have taken. Under -apply the unique index does this,
	// and claimed is only a mirror of it.
	claimed := map[string]bool{}
	assigned, failed := 0, 0

	for _, c := range todo {
		base := handle.Base(c.email, c.firstName, c.lastName)
		cands := append(handle.Candidates(base), handle.Fallback(), handle.Fallback())

		var got string
		var err error
		if *apply {
			got, err = claim(ctx, pool, c.userID, cands)
		} else {
			got, err = pretend(ctx, pool, cands, claimed)
		}
		if err != nil {
			fmt.Printf("  %s  FAILED: %v\n", c.userID, err)
			failed++
			continue
		}
		claimed[got] = true
		assigned++
		note := ""
		if got != base {
			note = fmt.Sprintf("   (preferred %q was taken)", base)
		}
		fmt.Printf("  %s  ->  @%s%s\n", c.userID, got, note)
	}

	fmt.Printf("\n%s: %d assigned, %d failed\n", mode, assigned, failed)
	if !*apply && assigned > 0 {
		fmt.Println("Re-run with -apply to write these.")
	}
	if failed > 0 {
		os.Exit(1)
	}
}

// claim takes the first free candidate, exactly as registration does: the
// unique index arbitrates, and a collision moves to the next candidate
// inside a SAVEPOINT rather than aborting.
func claim(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID, cands []string) (string, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	for _, cand := range cands {
		sp, err := tx.Begin(ctx)
		if err != nil {
			return "", err
		}
		var tag int64
		err = sp.QueryRow(ctx, `
			WITH upd AS (
				UPDATE profile.profiles SET username = $2, updated_at = NOW()
				WHERE user_id = $1 AND username IS NULL
				RETURNING 1
			)
			SELECT COUNT(*) FROM upd
		`, userID, cand).Scan(&tag)
		if err == nil {
			if tag == 0 {
				_ = sp.Rollback(ctx)
				return "", fmt.Errorf("already has a handle")
			}
			if err = sp.Commit(ctx); err == nil {
				return cand, tx.Commit(ctx)
			}
		}
		_ = sp.Rollback(ctx)
		if !isUniqueViolation(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("no candidate was available")
}

// pretend is the dry-run counterpart: it asks whether each candidate is free
// without taking it, and honours what earlier rows in this same run would
// have taken.
func pretend(ctx context.Context, pool *pgxpool.Pool, cands []string, claimed map[string]bool) (string, error) {
	for _, cand := range cands {
		if claimed[cand] {
			continue
		}
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM profile.profiles WHERE username = $1)`, cand,
		).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			return cand, nil
		}
	}
	return "", fmt.Errorf("no candidate was available")
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
