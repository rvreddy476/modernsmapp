//go:build integration

package piibackfill

import "github.com/atpost/commerce-service/internal/testdsn"

// refuseTheLiveDatabase stops the integration suite from running against a
// database that is not disposable. One shared rule, in internal/testdsn:
// the name must end in _test, and the three databases the running stack
// serves are refused by name. This wrapper keeps every TestMain's call site
// unchanged.
func refuseTheLiveDatabase(dsn string) { testdsn.Refuse(dsn) }
