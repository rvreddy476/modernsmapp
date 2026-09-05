//go:build integration

package http

import (
	"fmt"
	"os"
	"strings"
)

// refuseTheLiveDatabase stops the integration suite from running against the
// database the app is actually served from.
//
// The fixtures here seed products with status='active' and
// approval_status='approved', and almost none of them tear down — they were
// written against a throwaway database, and several assert on what the whole
// catalogue looks like, so retrofitting teardown would change what they test.
// Pointed at `commerce_db`, one afternoon of runs left the real storefront
// holding roughly four thousand listings called "Test Product", crowding out
// the ones somebody is actually selling. Nothing broke; the shop just filled
// up with rubbish, which is the kind of damage nobody notices until a demo.
//
// Documentation did not prevent it, so this does. `commerce_it_test` is the
// database these belong in; see docs/COMMERCE-TESTING.md for keeping its
// migrations current.
func refuseTheLiveDatabase(dsn string) {
	for _, live := range []string{"/commerce_db", "/app", "/identity_db"} {
		if strings.Contains(dsn, live+"?") || strings.HasSuffix(dsn, live) {
			fmt.Printf("COMMERCE_TEST_DSN points at %s, which the running stack serves.\n"+
				"These fixtures write live products and do not clean up.\n"+
				"Use commerce_it_test instead — see docs/COMMERCE-TESTING.md\n",
				strings.TrimPrefix(live, "/"))
			os.Exit(1)
		}
	}
}
