// media-claims reconciles media-service's claims with the media ids the
// question and answer tables hold (internal/claimsync has the rules).
//
//	media-claims            dry run: read every row, send nothing, print the counts
//	media-claims -apply     claim what live rows hold, release what dead rows held
//
// Environment, the same as the server: POSTGRES_DSN, MEDIA_SERVICE_URL,
// INTERNAL_SERVICE_KEY. Run it once after the claim-aware qa-service is
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

	"github.com/atpost/qa-service/internal/claimsync"
	"github.com/atpost/qa-service/internal/mediaclient"
	"github.com/atpost/qa-service/internal/store"
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
		fmt.Fprintln(os.Stderr, "media-claims: connect:", err)
		return 1
	}
	defer pool.Close()

	st := store.New(pool)
	var claims claimsync.Claims
	if client != nil {
		claims = client
	}
	rep, err := claimsync.Run(ctx, st, claims, []claimsync.Pager{st.QuestionMediaClaimRows, st.AnswerMediaClaimRows}, apply)
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
