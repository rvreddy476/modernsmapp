// Command reissue-invoices corrects, in place, the invoices that went out at
// ₹0 before 31dc9eac.
//
// IssueInvoice built P0 invoices from the NUMERIC rupee columns the P0
// checkout leaves at 0.00, so every invoice it issued read ₹0. The fix
// covers new invoices only: invoices.order_id is UNIQUE and IssueInvoice
// returns an existing row. This tool rebuilds each wrong invoice from its
// order's stored paise under the SAME number, financial year, sequence and
// date, overwrites its documents at the SAME keys, updates its totals, and
// appends an audit row (actor system:invoice_reissue, migration 039). It
// allocates no number, emits no event and sends no notification.
//
// A candidate is an invoice whose order stored a GST split (P0) and whose
// stored grand total differs from the order's final amount, so a second run
// changes nothing. Orders without a stored split (pre-P0 legacy) are listed
// as skipped; an order whose money does not reconcile is skipped too.
//
// DRY RUN by default — it lists what it would change and writes nothing:
//
//	POSTGRES_DSN=... ENV=dev COMMERCE_PII_LOOKUP_SALT=... COMMERCE_PII_DEV_KEY_*=... \
//	  go run ./cmd/reissue-invoices --allow-db=commerce_db
//
// --apply writes (MINIO_ENDPOINT, MINIO_ACCESS_KEY, MINIO_SECRET_KEY,
// MINIO_USE_SSL and COMMERCE_INVOICE_BUCKET as the server reads them):
//
//	go run ./cmd/reissue-invoices --allow-db=commerce_db --apply
//
// It refuses unless --allow-db repeats the DSN's database name exactly and
// ENV is a development environment (guard.go). It prints the target
// host:port/database, never the credentials.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/blob"
	pgstore "github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	var (
		apply   = flag.Bool("apply", false, "correct the invoices; without it the tool only reports what it would do")
		allowDB = flag.String("allow-db", "", "the target database's exact name (required)")
		timeout = flag.Duration("timeout", 5*time.Minute, "overall timeout")
	)
	flag.Parse()

	t, err := parseTarget(os.Getenv("POSTGRES_DSN"))
	if err != nil {
		fail(err)
	}
	mode := "DRY RUN (nothing is written)"
	if *apply {
		mode = "APPLY"
	}
	fmt.Printf("reissue-invoices: target %s, ENV=%q, %s\n", t, os.Getenv("ENV"), mode)
	if err := checkTarget(t, *allowDB, os.Getenv("ENV")); err != nil {
		fail(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(os.Getenv("POSTGRES_DSN"))
	if err != nil {
		fail(errors.New("POSTGRES_DSN does not parse"))
	}
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		fail(fmt.Errorf("connect to %s: %w", t, err))
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		fail(fmt.Errorf("ping %s: %w", t, err))
	}

	store := pgstore.New(pool)
	svc := service.NewOffline(store)
	if err := attachPII(svc); err != nil {
		fail(err)
	}
	if *apply {
		b, bucket, err := blobFromEnv()
		if err != nil {
			fail(err)
		}
		svc.WithBlob(b)
		fmt.Printf("reissue-invoices: documents overwritten in bucket %q\n", bucket)
	}

	rep, err := svc.ReissueInvoices(ctx, *apply)
	if err != nil {
		fail(err)
	}
	printReport(rep, *apply)
	if rep.Count(service.ReissueFailed) > 0 {
		os.Exit(1)
	}
}

// attachPII gives the service the address and KYC ciphers exactly as
// cmd/server builds them for a development ENV (pii.LocalKeyProvider). The
// guard has already refused every other ENV, so the KMS branch never applies.
// Without the keys an encrypted ship-to cannot be opened, and the invoice
// would be rewritten with a blank one — the reissue refuses that per invoice,
// but failing here says why once.
func attachPII(svc *service.Service) error {
	salt := os.Getenv("COMMERCE_PII_LOOKUP_SALT")
	if len(salt) < 16 {
		return errors.New("COMMERCE_PII_LOOKUP_SALT must be set (at least 16 bytes), as for the server")
	}
	provider, _, err := pii.LocalKeyProvider(
		[]byte(os.Getenv("COMMERCE_PII_DEV_KEY_PROFILE")),
		[]byte(os.Getenv("COMMERCE_PII_DEV_KEY_SNAPSHOT")),
		[]byte(os.Getenv("COMMERCE_PII_DEV_KEY_KYC")))
	if err != nil {
		return err
	}
	cipher, err := pii.New(provider, []byte(salt))
	if err != nil {
		return err
	}
	addrMode, err := pii.ParseMode(os.Getenv("COMMERCE_PII_CUTOVER"))
	if err != nil {
		return err
	}
	kycMode, err := pii.ParseModeFor("COMMERCE_KYC_PII_CUTOVER", os.Getenv("COMMERCE_KYC_PII_CUTOVER"))
	if err != nil {
		return err
	}
	svc.WithPII(cipher).WithPIICutover(addrMode).WithKYCCutover(kycMode)
	return nil
}

// blobFromEnv builds the invoice blob store from the same variables and
// defaults as cmd/server.
func blobFromEnv() (*blob.Store, string, error) {
	endpoint := envOr("MINIO_ENDPOINT", "minio:9000")
	bucket := envOr("COMMERCE_INVOICE_BUCKET", "commerce-invoices")
	b, err := blob.New(endpoint,
		envOr("MINIO_ACCESS_KEY", "minioadmin"), envOr("MINIO_SECRET_KEY", "minioadmin"),
		bucket, envOr("MINIO_USE_SSL", "false") == "true", os.Getenv("MINIO_PUBLIC_ENDPOINT"))
	if err != nil {
		return nil, "", fmt.Errorf("invoice blob store at %s: %w", endpoint, err)
	}
	return b, bucket, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func rupees(p int64) string {
	sign := ""
	if p < 0 {
		sign, p = "-", -p
	}
	return fmt.Sprintf("%s₹%d.%02d", sign, p/100, p%100)
}

func keyOrDash(k *string) string {
	if k == nil {
		return "-"
	}
	return *k
}

func printReport(rep *service.InvoiceReissueReport, apply bool) {
	for _, r := range rep.Results {
		row := r.Row
		fmt.Printf("\n[%s] invoice %s  order %s\n", strings.ToUpper(string(r.Outcome)), row.InvoiceNumber, orDash(row.OrderNumber))
		fmt.Printf("    stored grand total %s   order final %s\n", rupees(row.Totals.GrandTotal), rupees(row.OrderFinalMinor))
		if r.After != nil && (r.Outcome == service.ReissueWouldCorrect || r.Outcome == service.ReissueCorrected) {
			a := r.After
			fmt.Printf("    grand_total %s → %s; CGST %s → %s; SGST %s → %s; IGST %s → %s; is_interstate %v → %v\n",
				rupees(row.Totals.GrandTotal), rupees(a.GrandTotal),
				rupees(row.Totals.CGST), rupees(a.CGST), rupees(row.Totals.SGST), rupees(a.SGST),
				rupees(row.Totals.IGST), rupees(a.IGST), row.Totals.IsInterstate, a.IsInterstate)
			fmt.Printf("    same number/FY/sequence (%s, %d), date %s; documents at html=%s pdf=%s\n",
				row.FinancialYear, row.Sequence, row.IssuedAt.UTC().Format("2006-01-02 15:04 MST"),
				keyOrDash(row.HTMLMediaKey), keyOrDash(row.PDFMediaKey))
		}
		if r.Reason != "" {
			fmt.Printf("    reason: %s\n", r.Reason)
		}
	}
	fmt.Printf("\nreissue-invoices: %d would-correct, %d corrected, %d skipped, %d failed, %d already correct\n",
		rep.Count(service.ReissueWouldCorrect), rep.Count(service.ReissueCorrected),
		rep.Count(service.ReissueSkipped), rep.Count(service.ReissueFailed), rep.AlreadyCorrect)
	if !apply {
		fmt.Println("reissue-invoices: dry run; nothing written. Re-run with --apply to correct the would-correct invoices.")
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "reissue-invoices: "+err.Error())
	os.Exit(1)
}
