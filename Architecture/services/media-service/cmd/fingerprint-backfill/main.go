// fingerprint-backfill enqueues Copyright Match fingerprint jobs for ready
// videos that have none for their current generation, at backfill priority
// and under a rate limit (plan section 13, "Backfill").
//
//	POSTGRES_DSN=… COPYRIGHT_FINGERPRINT_BACKFILL_ENABLED=true \
//	  go run ./cmd/fingerprint-backfill [-rate 60] [-backlog-limit 10000] [-dry-run] [-max N]
//
// It only writes media_fingerprint_jobs rows. The worker claims them only
// while COPYRIGHT_FINGERPRINT_ENABLED and
// COPYRIGHT_FINGERPRINT_BACKFILL_ENABLED are both on, and never while a
// transcode is running or waiting. It stops when the queue of NEW-UPLOAD
// jobs exceeds -backlog-limit, so a backlog of fresh uploads is never
// buried under old catalogue.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/atpost/media-service/database"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	rate := flag.Int("rate", 60, "jobs enqueued per minute")
	backlogLimit := flag.Int("backlog-limit", 10000, "stop while this many new-upload jobs are queued")
	dryRun := flag.Bool("dry-run", false, "list what would be enqueued; write nothing")
	maxJobs := flag.Int("max", 0, "stop after this many jobs (0 = no limit)")
	flag.Parse()

	if !envFlag("COPYRIGHT_FINGERPRINT_BACKFILL_ENABLED") && !*dryRun {
		log.Fatal("COPYRIGHT_FINGERPRINT_BACKFILL_ENABLED is not set; refusing to enqueue (use -dry-run to preview)")
	}
	if *rate <= 0 {
		log.Fatal("-rate must be positive")
	}
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		log.Fatal("POSTGRES_DSN is required")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	if err := postgres.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		log.Fatalf("bootstrap schema: %v", err)
	}
	store := postgres.New(pool)

	interval := time.Minute / time.Duration(*rate)
	after := time.Time{}
	enqueued, skipped := 0, 0
	log.Printf("fingerprint backfill: %d/min, backlog limit %d, dry-run=%v", *rate, *backlogLimit, *dryRun)
	for {
		if ctx.Err() != nil {
			break
		}
		st, err := store.FingerprintQueueStats(ctx)
		if err != nil {
			log.Fatalf("queue stats: %v", err)
		}
		if st.QueuedNewUploads > *backlogLimit {
			log.Printf("paused: %d new-upload jobs queued (> %d); retrying in 1 min", st.QueuedNewUploads, *backlogLimit)
			select {
			case <-ctx.Done():
			case <-time.After(time.Minute):
			}
			continue
		}
		ids, times, err := store.ListReadyVideosWithoutFingerprint(ctx, after, 200)
		if err != nil {
			log.Fatalf("list: %v", err)
		}
		if len(ids) == 0 {
			break
		}
		for i, id := range ids {
			if ctx.Err() != nil {
				break
			}
			after = times[i]
			if *dryRun {
				fmt.Println(id)
				enqueued++
			} else {
				_, created, err := store.EnqueueFingerprintJob(ctx, id, postgres.FingerprintPriorityBackfill, false)
				switch {
				case err == nil && created:
					enqueued++
				case err == nil:
					skipped++ // a row appeared since the list was read
				default:
					skipped++
					log.Printf("skip %s: %v", id, err)
				}
			}
			if *maxJobs > 0 && enqueued >= *maxJobs {
				log.Printf("reached -max %d", *maxJobs)
				cancel()
				break
			}
			select {
			case <-ctx.Done():
			case <-time.After(interval):
			}
		}
	}
	log.Printf("fingerprint backfill finished: %d enqueued, %d skipped", enqueued, skipped)
}

func envFlag(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "on", "yes", "enabled":
		return true
	}
	return false
}
