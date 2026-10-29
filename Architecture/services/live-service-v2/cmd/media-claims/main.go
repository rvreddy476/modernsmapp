// media-claims reconciles media-service's claims with the stream covers
// live_streams holds and the recording assets live_recording_imports holds
// (internal/claimsync has the rules).
//
//	media-claims             dry run: read every stream with a cover, send nothing, print the counts
//	media-claims -apply      claim what streams hold, release what they no longer hold
//	media-claims -all ...    visit every stream and import, not only those that name
//	                         media (repairs stale claims of covers that were cleared)
//
// Environment, the same as the server: POSTGRES_DSN,
// MEDIA_SERVICE_URL, INTERNAL_SERVICE_KEY. Run it once after the claim-aware
// live-service-v2 is deployed (the backfill), and again whenever the server
// logs a failed release. Re-running is always safe: every call is idempotent.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atpost/live-service-v2/internal/claimsync"
	"github.com/atpost/live-service-v2/internal/mediaclient"
	"github.com/atpost/live-service-v2/internal/store/postgres"
)

func main() {
	apply := flag.Bool("apply", false, "send the claims and releases (default: dry run)")
	all := flag.Bool("all", false, "visit every stream, not only those that name a cover")
	flag.Parse()
	os.Exit(run(*apply, *all))
}

func run(apply, all bool) int {
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
		fmt.Fprintln(os.Stderr, "media-claims: connect:", err)
		return 1
	}
	defer pool.Close()

	var claims claimsync.Claims
	if client != nil {
		claims = client
	}
	rep, err := claimsync.Run(ctx, claimsync.NewStoreSource(postgres.New(pool), all), claims, apply)
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
