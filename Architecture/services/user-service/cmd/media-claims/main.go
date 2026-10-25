// media-claims reconciles media-service's claims with the media ids
// user-service's tables hold (internal/claimsync has the rules): channels'
// picture and banner, portfolio items' media, business pages' avatar and
// cover, and the media_claim_pending queue (deleted rows).
//
//	media-claims            dry run: read every row, send nothing, print the counts
//	media-claims -apply     claim what live rows hold, release what dead rows held
//
// Environment, the same as the server: POSTGRES_DSN, MEDIA_SERVICE_URL,
// INTERNAL_SERVICE_KEY. Run it once after the claim-aware user-service is
// deployed (the backfill), and again whenever the server logs a release it
// could not queue. Re-running is always safe: every call is idempotent.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/atpost/user-service/internal/claimsync"
	"github.com/atpost/user-service/internal/mediaclient"
	"github.com/atpost/user-service/internal/store"
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
	var claims claimsync.Claims
	if client := mediaclient.New(mediaURL, os.Getenv("INTERNAL_SERVICE_KEY")); client != nil {
		claims = client
	} else if apply {
		fmt.Fprintln(os.Stderr, "media-claims: -apply needs MEDIA_SERVICE_URL and INTERNAL_SERVICE_KEY")
		return 2
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "media-claims: connect:", err)
		return 1
	}
	defer pool.Close()

	rep, err := claimsync.Run(ctx, store.New(pool), claims, apply)
	mode := "dry run"
	if apply {
		mode = "applied"
	}
	fmt.Printf("media-claims (%s): %s\n", mode, rep)
	for _, d := range rep.Dangling {
		fmt.Printf("dangling: %s %s names missing media %s\n", d.Kind, d.Ref, d.MediaID)
	}
	for _, v := range rep.Invalid {
		fmt.Printf("invalid: %s %s holds %q, which is not a media id\n", v.Kind, v.Ref, v.Value)
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
