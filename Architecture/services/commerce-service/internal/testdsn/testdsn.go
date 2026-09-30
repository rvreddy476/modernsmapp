// Package testdsn is the ONE guard every integration suite runs before it
// touches COMMERCE_TEST_DSN.
//
// The fixtures in this service seed products with status='active' and
// approval_status='approved', and almost none of them tear down — they were
// written against a throwaway database, and several assert on what the whole
// catalogue looks like, so retrofitting teardown would change what they test.
// Pointed at `commerce_db`, one afternoon of runs left the real storefront
// holding roughly four thousand listings called "Test Product", crowding out
// the ones somebody is actually selling. Nothing broke; the shop just filled
// up with rubbish, which is the kind of damage nobody notices until a demo.
//
// The first guard was a denylist of three names. A denylist admits every
// name it has not heard of, which is the wrong default for a suite that
// publishes live listings: the next database an operator points it at is
// admitted until someone adds it to the list. So this is an ALLOWLIST by
// shape — the database name must end in `_test` — with the three known live
// names refused by name as well, so the message can say what the caller
// nearly did.
//
// Four packages held their own copy of the old guard; each now calls this.
// See docs/COMMERCE-TESTING.md.
package testdsn

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// liveDatabases are the names the running stack serves the app from.
var liveDatabases = []string{"commerce_db", "app", "identity_db"}

// DatabaseName extracts the database from a libpq URL or key=value DSN.
// Empty when it cannot be found.
func DatabaseName(dsn string) string {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return ""
	}
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" && u.Host != "" {
		return strings.TrimPrefix(u.Path, "/")
	}
	// key=value form: dbname=commerce_it_test host=…
	for _, kv := range strings.Fields(dsn) {
		if strings.HasPrefix(kv, "dbname=") {
			return strings.TrimPrefix(kv, "dbname=")
		}
	}
	return ""
}

// Check reports why a DSN must not be used by the integration suites, or ""
// when it may. An empty DSN is admitted here: TestMain treats it as "skip".
func Check(dsn string) string {
	if strings.TrimSpace(dsn) == "" {
		return ""
	}
	name := DatabaseName(dsn)
	for _, live := range liveDatabases {
		if name == live {
			return fmt.Sprintf("COMMERCE_TEST_DSN points at %s, which the running stack serves.\n"+
				"These fixtures write live products and do not clean up.\n"+
				"Use commerce_it_test instead — see docs/COMMERCE-TESTING.md", live)
		}
	}
	if !strings.HasSuffix(name, "_test") {
		return fmt.Sprintf("COMMERCE_TEST_DSN names database %q, which does not end in _test.\n"+
			"The integration fixtures publish live listings and never tear down, so they run only\n"+
			"against a database whose name says it is disposable. Use commerce_it_test —\n"+
			"see docs/COMMERCE-TESTING.md", name)
	}
	return ""
}

// Refuse exits the test binary when the DSN is not a throwaway database.
// Called from every integration TestMain before the pool is opened.
func Refuse(dsn string) {
	if why := Check(dsn); why != "" {
		fmt.Println(why)
		os.Exit(1)
	}
}

// SwapDatabase replaces the database name in a libpq URL, for suites that
// create a scratch database beside the one COMMERCE_TEST_DSN names.
func SwapDatabase(dsn, name string) string {
	q := ""
	if i := strings.Index(dsn, "?"); i >= 0 {
		q = dsn[i:]
		dsn = dsn[:i]
	}
	if i := strings.LastIndex(dsn, "/"); i >= 0 {
		dsn = dsn[:i+1] + name
	}
	return dsn + q
}
