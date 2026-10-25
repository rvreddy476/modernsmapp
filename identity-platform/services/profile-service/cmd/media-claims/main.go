// media-claims reconciles media-service's claims with the avatar and cover
// ids profile.profiles holds (internal/claimsync has the rules).
//
//	media-claims            dry run: read every profile, send nothing, print the counts
//	media-claims -apply     claim what profiles show, release what they no longer do
//
// Environment, the same as the server: POSTGRES_DSN, MEDIA_SERVICE_URL,
// INTERNAL_SERVICE_KEY. Run it once after the claim-aware profile-service is
// deployed (the backfill), and again whenever the server logs a failed
// release. Re-running is always safe: every call is idempotent.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/atpost/identity-profile-service/internal/claimsync"
	"github.com/atpost/identity-profile-service/internal/mediaclient"
	"github.com/atpost/identity-profile-service/internal/store"
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
	client := mediaclient.New(mediaURL, os.Getenv("INTERNAL_SERVICE_KEY"))
	if client == nil && apply {
		fmt.Fprintln(os.Stderr, "media-claims: -apply needs MEDIA_SERVICE_URL and INTERNAL_SERVICE_KEY")
		return 2
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		// Not printing err: it can echo the connection string.
		fmt.Fprintln(os.Stderr, "media-claims: could not connect to POSTGRES_DSN")
		return 1
	}
	defer pool.Close()

	rep, err := claimsync.Run(ctx, store.New(pool), client, apply)
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
