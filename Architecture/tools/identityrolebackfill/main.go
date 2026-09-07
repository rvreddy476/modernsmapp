// Command identityrolebackfill grants ecosystem roles in identity to people
// who already have a seller / restaurant / delivery / rider record.
//
// It is ALSO the reconciler. Every operation it performs is idempotent, so it
// is safe to re-run — on a schedule, or after fixing whatever caused a batch
// of role intents to dead-letter. That matters because the in-process worker
// (shared/identityroles) can lose an intent in exactly one window: a crash
// between a NON-transactional domain write and its EnqueuePool. Nothing
// in-process closes that window. This does.
//
// # THE TRAP THIS TOOL EXISTS TO AVOID
//
// commerce_db.sellers currently holds 4773 rows and identity holds 49 users.
// Most of those sellers are fixture pollution: integration tests that were
// wrongly pointed at the live database, minting random UUIDs for user_id.
// auth.user_roles has NO foreign key to auth.users, so a naive backfill would
// cheerfully insert ~4700 role rows for people who do not exist, and nothing
// in the schema would stop it.
//
// So every candidate is checked against identity first, and the check is
// GET /v1/auth/internal/users/{id}, which 404s for an unknown account.
//
// It is specifically NOT GET /v1/auth/internal/roles/{id}. That endpoint
// answers 200 with an empty list for a user id that does not exist, because
// auth-service's ResolveRoles deliberately never errors — it degrades to the
// env allowlist on a DB failure. "No roles" and "no such user" are the same
// answer there, which makes it useless as an existence check and dangerous as
// one.
//
// # WHY THIS IS NOT A SQL JOIN
//
// identity lives in `identity_db` and commerce in `commerce_db` — different
// databases on the same server, so there is no join to write. food and rider
// live in `app`, which is a different database again from identity's. Going
// over the internal HTTP API is what makes one tool work for all four sources,
// and it has the side benefit of exercising the same contract the services
// use, with the same key.
//
// # USAGE
//
// DRY RUN IS THE DEFAULT. Nothing is written without -apply.
//
//	identityrolebackfill \
//	  -source=commerce \
//	  -dsn='postgres://postgres:postgres@127.0.0.1:5432/commerce_db?sslmode=disable' \
//	  -identity-url=http://127.0.0.1:8081 \
//	  -key="$INTERNAL_SERVICE_KEY"
//
// Add -apply to actually grant. Add -v to list the skipped ids. Add
// -requeue-dead (with -dsn pointing at the SERVICE's database) to clear
// dead_lettered_at on that service's identity_role_intents so the running
// worker retries them — use it after fixing the cause, never before.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/atpost/shared/identityroles"
	"github.com/jackc/pgx/v5/pgxpool"
)

// source describes one place partner records live.
type source struct {
	name string
	role string
	// query must select (user_id, status) and must already exclude records
	// whose status means the person has LEFT the journey. Keeping the filter
	// in SQL rather than in Go keeps the "what counts as terminal" decision
	// next to the table it is about.
	query string
	// mirrors names the file whose grant/revoke table this query must agree
	// with. These are unexported functions inside internal/ packages, so the
	// tool cannot import them; if you change one, change this.
	mirrors string
	// db names the database this source expects, for the safety check.
	db string
}

var sources = map[string]source{
	"commerce": {
		name: "commerce",
		role: identityroles.RoleSeller,
		// 'rejected' is terminal in commerce: SubmitSellerApplication only
		// accepts status='draft', so a rejected seller cannot resubmit.
		// 'suspended' is NOT terminal — a suspended seller is still a seller.
		query: `SELECT user_id::text, status
		          FROM sellers
		         WHERE status <> 'rejected'
		         ORDER BY created_at`,
		mirrors: "services/commerce-service/internal/store/postgres/onboarding.go",
		db:      "commerce_db",
	},
	"food-restaurant": {
		name: "food-restaurant",
		role: identityroles.RoleRestaurantOwner,
		// owner_user_id is not unique — DISTINCT, because one person may own
		// several restaurants and needs the role exactly once.
		query: `SELECT DISTINCT ON (owner_user_id) owner_user_id::text, status::text
		          FROM food.restaurant_partners
		         WHERE status NOT IN ('REJECTED','CLOSED')
		         ORDER BY owner_user_id`,
		mirrors: "services/food-service/internal/store/postgres/identity_roles.go",
		db:      "app",
	},
	"food-delivery": {
		name: "food-delivery",
		role: identityroles.RoleDeliveryPartner,
		query: `SELECT user_id::text, status::text
		          FROM food.delivery_partners
		         WHERE status NOT IN ('REJECTED','CLOSED')
		         ORDER BY created_at`,
		mirrors: "services/food-service/internal/store/postgres/identity_roles.go",
		db:      "app",
	},
	"rider": {
		name: "rider",
		role: identityroles.RoleRiderPartner,
		query: `SELECT user_id::text, status::text
		          FROM rider_partners
		         WHERE deleted_at IS NULL
		           AND status NOT IN ('rejected','blocked')
		         ORDER BY created_at`,
		mirrors: "services/rider-service/internal/store/identity_roles.go",
		db:      "app",
	},
}

type candidate struct {
	userID string
	status string
}

type tally struct {
	scanned        int
	notInIdentity  []string
	alreadyHeld    int
	granted        int
	wouldGrant     []string
	lookupFailed   []string
	grantFailed    []string
	distinctUserID int
}

func main() {
	var (
		srcName     = flag.String("source", "", "one of: commerce, food-restaurant, food-delivery, rider")
		dsn         = flag.String("dsn", "", "Postgres DSN for the SERVICE's database (not identity's)")
		identityURL = flag.String("identity-url", "http://127.0.0.1:8081", "identity-auth-service base URL")
		key         = flag.String("key", "", "internal service key; defaults to $INTERNAL_SERVICE_KEY")
		apply       = flag.Bool("apply", false, "actually grant roles. WITHOUT THIS NOTHING IS WRITTEN.")
		limit       = flag.Int("limit", 0, "stop after N candidates (0 = all)")
		verbose     = flag.Bool("v", false, "list the ids behind each skip category")
		requeueDead = flag.Bool("requeue-dead", false, "clear dead_lettered_at on this service's identity_role_intents so the worker retries them, then exit")
		schema      = flag.String("intents-schema", "", "schema holding identity_role_intents for -requeue-dead (rider uses \"rider\"; commerce and food use the default, public)")
		pause       = flag.Duration("pause", 0, "sleep between candidates; use to be gentle on identity")
	)
	flag.Parse()

	if *dsn == "" {
		fail("-dsn is required")
	}
	internalKey := *key
	if internalKey == "" {
		internalKey = os.Getenv("INTERNAL_SERVICE_KEY")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		fail("connect to service database: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		fail("ping service database: %v", err)
	}

	if *requeueDead {
		requeueDeadLettered(ctx, pool, *schema, *apply)
		return
	}

	src, ok := sources[*srcName]
	if !ok {
		fail("-source must be one of: %s", strings.Join(sortedSourceNames(), ", "))
	}
	if internalKey == "" {
		fail("no internal service key: pass -key or set INTERNAL_SERVICE_KEY. " +
			"Without it every call to identity is a 403.")
	}

	client := identityroles.NewClient(*identityURL, internalKey, "identityrolebackfill")
	if !client.Configured() {
		fail("-identity-url is empty")
	}

	fmt.Printf("source        %s\n", src.name)
	fmt.Printf("role          %s\n", src.role)
	fmt.Printf("service db    %s (expects: %s)\n", redactDSN(*dsn), src.db)
	fmt.Printf("identity      %s\n", *identityURL)
	fmt.Printf("mirrors       %s\n", src.mirrors)
	if *apply {
		fmt.Printf("mode          APPLY — roles will be granted\n\n")
	} else {
		fmt.Printf("mode          DRY RUN — nothing will be written (pass -apply to grant)\n\n")
	}

	cands, err := loadCandidates(ctx, pool, src, *limit)
	if err != nil {
		fail("read candidates: %v", err)
	}

	t := run(ctx, client, src, cands, *apply, *pause)
	report(src, t, *apply, *verbose)

	// A non-zero exit when anything failed, so a scheduled reconciliation run
	// is visible in CI/cron rather than quietly half-working.
	if len(t.lookupFailed) > 0 || len(t.grantFailed) > 0 {
		os.Exit(1)
	}
}

func loadCandidates(ctx context.Context, pool *pgxpool.Pool, src source, limit int) ([]candidate, error) {
	q := src.query
	if limit > 0 {
		q = fmt.Sprintf("SELECT * FROM (%s) c LIMIT %d", q, limit)
	}
	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.userID, &c.status); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// run does the work. Sequential on purpose: the whole job is a few thousand
// requests against an in-cluster service, it finishes in under a minute, and a
// worker pool would buy seconds at the cost of a tool whose ordering and
// failure reporting a reviewer has to reason about.
func run(ctx context.Context, client *identityroles.Client, src source, cands []candidate, apply bool, pause time.Duration) tally {
	var t tally
	// Cache existence per user id: food-restaurant already deduplicates in
	// SQL, but a second source or a future query may not.
	exists := map[string]bool{}
	seen := map[string]bool{}

	for _, c := range cands {
		if ctx.Err() != nil {
			fmt.Fprintf(os.Stderr, "\ninterrupted after %d candidates; safe to re-run\n", t.scanned)
			break
		}
		t.scanned++
		if !seen[c.userID] {
			seen[c.userID] = true
			t.distinctUserID++
		}
		if pause > 0 {
			time.Sleep(pause)
		}

		ok, cached := exists[c.userID]
		if !cached {
			var err error
			ok, err = client.UserExists(ctx, c.userID)
			if err != nil {
				// Could not find out. NEVER treat this as "no such user" —
				// that is how you skip a real seller forever.
				t.lookupFailed = append(t.lookupFailed, fmt.Sprintf("%s (%v)", c.userID, err))
				continue
			}
			exists[c.userID] = ok
		}
		if !ok {
			t.notInIdentity = append(t.notInIdentity, c.userID)
			continue
		}

		held, err := client.Roles(ctx, c.userID)
		if err != nil {
			t.lookupFailed = append(t.lookupFailed, fmt.Sprintf("%s roles (%v)", c.userID, err))
			continue
		}
		if contains(held, src.role) {
			t.alreadyHeld++
			continue
		}

		if !apply {
			t.wouldGrant = append(t.wouldGrant, fmt.Sprintf("%s (status=%s)", c.userID, c.status))
			continue
		}
		reason := fmt.Sprintf("backfill from %s, record status=%s", src.name, c.status)
		if err := client.Grant(ctx, c.userID, src.role, reason); err != nil {
			t.grantFailed = append(t.grantFailed, fmt.Sprintf("%s (%v)", c.userID, err))
			continue
		}
		t.granted++
	}
	return t
}

func report(src source, t tally, apply bool, verbose bool) {
	fmt.Printf("scanned                     %6d  (%d distinct user ids)\n", t.scanned, t.distinctUserID)
	fmt.Printf("  skipped: not in identity  %6d\n", len(t.notInIdentity))
	fmt.Printf("  skipped: already held     %6d\n", t.alreadyHeld)
	if apply {
		fmt.Printf("  granted                   %6d\n", t.granted)
	} else {
		fmt.Printf("  would grant               %6d\n", len(t.wouldGrant))
	}
	fmt.Printf("  identity lookup failed    %6d\n", len(t.lookupFailed))
	if apply {
		fmt.Printf("  grant failed              %6d\n", len(t.grantFailed))
	}

	if len(t.notInIdentity) > 0 {
		fmt.Printf("\n%d record(s) name a user id that does not exist in identity.\n", len(t.notInIdentity))
		fmt.Printf("These were SKIPPED. auth.user_roles has no foreign key to auth.users,\n")
		fmt.Printf("so granting them would have created role rows for nobody.\n")
		fmt.Printf("In %s this is overwhelmingly integration-test fixture pollution.\n", src.db)
	}

	if verbose {
		printList("not in identity", t.notInIdentity)
		printList("would grant", t.wouldGrant)
	}
	printList("LOOKUP FAILED (re-run to retry)", t.lookupFailed)
	printList("GRANT FAILED (re-run to retry)", t.grantFailed)

	if !apply {
		fmt.Printf("\nDry run. Nothing was written. Re-run with -apply to grant.\n")
	}
}

// requeueDeadLettered clears dead_lettered_at so the service's running worker
// picks the intents up again. Dry run by default, like everything else here.
func requeueDeadLettered(ctx context.Context, pool *pgxpool.Pool, schema string, apply bool) {
	table := identityroles.TableName(schema)
	var n int64
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM `+table+` WHERE dead_lettered_at IS NOT NULL`).Scan(&n); err != nil {
		fail("count dead-lettered intents in %s: %v", table, err)
	}
	fmt.Printf("table                 %s\n", table)
	fmt.Printf("dead-lettered intents %d\n", n)
	if n == 0 {
		return
	}

	rows, err := pool.Query(ctx,
		`SELECT op, role_name, last_error, COUNT(*)
		   FROM `+table+`
		  WHERE dead_lettered_at IS NOT NULL
		  GROUP BY 1,2,3 ORDER BY 4 DESC LIMIT 20`)
	if err != nil {
		fail("summarise: %v", err)
	}
	fmt.Printf("\nwhy they died:\n")
	for rows.Next() {
		var op, role string
		var lastErr *string
		var count int64
		if err := rows.Scan(&op, &role, &lastErr, &count); err != nil {
			fail("scan: %v", err)
		}
		msg := "(no error recorded)"
		if lastErr != nil {
			msg = *lastErr
		}
		fmt.Printf("  %6d  %-6s %-18s %s\n", count, op, role, msg)
	}
	rows.Close()

	if !apply {
		fmt.Printf("\nDry run. Nothing was written. Re-run with -apply to requeue them.\n")
		fmt.Printf("FIX THE CAUSE FIRST — requeueing into a broken key or a down identity\n")
		fmt.Printf("just burns the retry budget again.\n")
		return
	}
	tag, err := pool.Exec(ctx,
		`UPDATE `+table+`
		    SET dead_lettered_at = NULL, attempts = 0, next_attempt_at = NOW(), last_error = NULL
		  WHERE dead_lettered_at IS NOT NULL`)
	if err != nil {
		fail("requeue: %v", err)
	}
	fmt.Printf("\nrequeued %d intent(s); the service's worker will retry within a second.\n", tag.RowsAffected())
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

func printList(label string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Printf("\n%s (%d):\n", label, len(items))
	for _, it := range items {
		fmt.Printf("  %s\n", it)
	}
}

func sortedSourceNames() []string {
	out := make([]string, 0, len(sources))
	for k := range sources {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// redactDSN strips the password so the banner can be pasted into a ticket.
func redactDSN(dsn string) string {
	at := strings.LastIndex(dsn, "@")
	scheme := strings.Index(dsn, "://")
	if at < 0 || scheme < 0 || at < scheme {
		return dsn
	}
	return dsn[:scheme+3] + "***@" + dsn[at+1:]
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "identityrolebackfill: "+format+"\n", args...)
	os.Exit(2)
}
