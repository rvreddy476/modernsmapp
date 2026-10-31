// media-claims reconciles media-service's claims with the media ids
// post-service's non-FK columns hold — post covers, reel draft media, event
// covers, composer draft covers, video series covers and playlist covers
// (internal/claimsync has the rules).
//
//	media-claims            dry run: read every row, send nothing, print the counts
//	media-claims -apply     claim what live rows hold, release what dead or gone rows held
//
// Environment, the same as the server: POSTGRES_DSN, MEDIA_SERVICE_URL,
// INTERNAL_SERVICE_KEY. Run it once after the claim-aware post-service is
// deployed (the backfill), and again whenever the server logs a release it
// could not make ("run media-claims"). Re-running is always safe: every call
// is idempotent, and each row is locked and re-read before it is acted on.
//
// Exit status: 0 clean, 1 a row could not be reconciled (re-run), 2 misuse.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/atpost/post-service/internal/claimsync"
	"github.com/atpost/post-service/internal/mediaclaims"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	apply := flag.Bool("apply", false, "send the claims and releases (default: dry run)")
	flag.Parse()
	os.Exit(run(*apply))
}

func run(apply bool) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "media-claims: POSTGRES_DSN is required")
		return 2
	}
	mediaURL := os.Getenv("MEDIA_SERVICE_URL")
	if mediaURL == "" {
		mediaURL = "http://media-service:8087"
	}
	client := mediaclaims.New(mediaURL, os.Getenv("INTERNAL_SERVICE_KEY"))
	if client == nil && apply {
		fmt.Fprintln(os.Stderr, "media-claims: -apply needs MEDIA_SERVICE_URL and INTERNAL_SERVICE_KEY")
		return 2
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "media-claims: connect failed") // never the DSN: it carries the password
		return 1
	}
	defer pool.Close()

	var claims claimsync.Claims = client
	if client == nil {
		claims = dryRunOnly{}
	}
	rep, err := claimsync.Run(ctx, postgres.New(pool), claims, apply)
	mode := "dry run"
	if apply {
		mode = "applied"
	}
	fmt.Printf("media-claims (%s): %s\n", mode, rep)
	for _, d := range rep.Dangling {
		fmt.Printf("dangling: %s %s names missing media %s\n", d.Kind, d.Ref, d.MediaID)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "media-claims:", err)
		return 1
	}
	if rep.Failed > 0 {
		return 1
	}
	return 0
}
