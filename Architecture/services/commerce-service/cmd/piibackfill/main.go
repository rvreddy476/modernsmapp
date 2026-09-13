// Command piibackfill encrypts the existing address estate.
//
// B5. It is the step between "new writes are encrypted" and "the plaintext can
// be cleared". Run it after the dual-write image is live and before the gated
// scrub; the scrub refuses until this reports every table complete with zero
// failures.
//
// It is safe to run repeatedly, safe to interrupt, and safe to run while the
// service is serving traffic: rows that already have ciphertext are skipped,
// the cursor is durable, and each batch is a short transaction.
//
//	POSTGRES_DSN=...  ENV=staging  COMMERCE_KMS_KEY_ID=...  COMMERCE_PII_LOOKUP_SALT=...
//	go run ./cmd/piibackfill
//
// Options:
//
//	-report   print current progress and exit without doing any work
//	-batch N  rows per transaction (default 200)
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/atpost/commerce-service/internal/kmsclient"
	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/piibackfill"
	pgstore "github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/o11y/logging"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logging.Init(logging.Config{ServiceName: "commerce-pii-backfill"})

	report := flag.Bool("report", false, "print progress and exit")
	batch := flag.Int("batch", 200, "rows per transaction")
	set := flag.String("set", "all", "which cutover to backfill: address, kyc (migration 035), or all")
	flag.Parse()
	runAddress, runKYC := false, false
	switch *set {
	case "all":
		runAddress, runKYC = true, true
	case "address":
		runAddress = true
	case "kyc":
		runKYC = true
	default:
		fail("parsing -set", fmt.Errorf("-set=%q: want address, kyc or all", *set))
	}

	// Ctrl-C and SIGTERM stop the job cleanly. "Cleanly" means the durable
	// cursor is left pointing at the last COMMITTED row, so the next run
	// resumes there — an interrupted backfill is a normal state, not damage.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, os.Getenv("POSTGRES_DSN"))
	if err != nil {
		fail("connecting to postgres", err)
	}
	defer pool.Close()

	cipher, err := buildCipher(ctx, pgstore.New(pool))
	if err != nil {
		fail("building the PII cipher", err)
	}

	job, err := piibackfill.New(pool, cipher)
	if err != nil {
		fail("building the job", err)
	}
	job.BatchSize = *batch

	if *report {
		if runAddress {
			stats, err := job.AllStats(ctx)
			if err != nil {
				fail("reading address progress", err)
			}
			fmt.Println("address (gated/1000):")
			printStats(stats)
		}
		if runKYC {
			stats, err := job.KYCStats(ctx)
			if err != nil {
				fail("reading KYC progress", err)
			}
			fmt.Println("kyc (gated/1002):")
			printStats(stats)
		}
		return
	}

	slog.Info("pii backfill starting", "batch_size", job.BatchSize, "set", *set)
	incomplete := 0
	count := func(stats []piibackfill.Stats) {
		for _, s := range stats {
			if !s.Completed || s.Failed > 0 {
				incomplete++
			}
		}
	}
	if runAddress {
		stats, err := job.Run(ctx)
		fmt.Println("address (gated/1000):")
		printStats(stats)
		if err != nil {
			// A failure leaves the estate consistent and the cursor honest; the
			// operator fixes the cause and re-runs.
			fail("address backfill", err)
		}
		count(stats)
	}
	if runKYC {
		stats, err := job.RunKYC(ctx)
		fmt.Println("kyc (gated/1002):")
		printStats(stats)
		if err != nil {
			fail("KYC backfill", err)
		}
		count(stats)
	}

	if incomplete > 0 {
		slog.Error("pii backfill did not finish; the gated plaintext scrub will refuse",
			"incomplete_tables", incomplete)
		os.Exit(1)
	}
	slog.Info("pii backfill complete; the matching gated plaintext scrub may now run", "set", *set)
}

// buildCipher mirrors the service's own construction, so the backfill seals
// with exactly the key the service will later read with.
//
// A backfill that used a different key source would encrypt every row to
// something the service cannot open — and the failure would surface only
// after the scrub had removed the plaintext.
func buildCipher(ctx context.Context, store *pgstore.Store) (*pii.Cipher, error) {
	salt := os.Getenv("COMMERCE_PII_LOOKUP_SALT")
	if len(salt) < 16 {
		return nil, fmt.Errorf("COMMERCE_PII_LOOKUP_SALT must be at least 16 bytes")
	}
	env := strings.ToLower(strings.TrimSpace(os.Getenv("ENV")))

	switch env {
	case "prod", "production", "staging", "stage":
		keyID := os.Getenv("COMMERCE_KMS_KEY_ID")
		if keyID == "" {
			return nil, fmt.Errorf("COMMERCE_KMS_KEY_ID is required in %s", env)
		}
		client, err := kmsclient.New(ctx)
		if err != nil {
			return nil, err
		}
		canonical := "staging"
		if env == "prod" || env == "production" {
			canonical = "prod"
		}
		provider, err := pii.NewKMSKeyProvider(client, pgstore.NewPIIKeyRing(store), keyID, canonical)
		if err != nil {
			return nil, err
		}
		return pii.New(provider, []byte(salt))

	case "dev", "development", "local", "test", "ci":
		// The SAME provider the service builds (pii.LocalKeyProvider), including
		// the derived KYC key, so the backfill seals with what the service opens.
		provider, _, err := pii.LocalKeyProvider(
			[]byte(os.Getenv("COMMERCE_PII_DEV_KEY_PROFILE")),
			[]byte(os.Getenv("COMMERCE_PII_DEV_KEY_SNAPSHOT")),
			[]byte(os.Getenv("COMMERCE_PII_DEV_KEY_KYC")))
		if err != nil {
			return nil, err
		}
		return pii.New(provider, []byte(salt))

	default:
		// Same closed list as the service. An unclassifiable environment
		// might be production, and a backfill that guessed wrong would seal
		// real addresses under a throwaway key.
		return nil, fmt.Errorf("ENV=%q is not a recognised environment", env)
	}
}

func printStats(stats []piibackfill.Stats) {
	for _, s := range stats {
		fmt.Println("  " + s.String())
	}
}

func fail(what string, err error) {
	slog.Error("pii backfill: "+what+" failed", "error", err)
	os.Exit(1)
}
