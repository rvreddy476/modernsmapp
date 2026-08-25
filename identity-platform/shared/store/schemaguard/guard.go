// Package schemaguard verifies at boot that the database actually contains the
// objects a service depends on, and refuses to start when it does not.
//
// # WHY THIS EXISTS INSTEAD OF A BOOT-TIME MIGRATION RUNNER
//
// Schema changes are owned by a deployment pipeline and executed against the
// server deliberately, with data handled as a decision rather than a side
// effect. An application that migrates its own database on boot fights that
// model: every replica races to mutate schema during a rolling deploy, and a
// migration that needs judgement gets executed by whichever pod started first.
//
// The application's job is therefore the opposite one — to *verify* that the
// pipeline has already done its work, and to fail loudly when it has not. A
// service that starts against a schema it cannot rely on does not degrade, it
// corrupts.
//
// This also closes a real incident. `identity_db` once carried a migration
// ledger asserting seventeen applied migrations whose objects were provably
// absent, because the ledger recorded a *claim* rather than an observation.
// A precondition check cannot lie in that direction: it looks at the catalog.
package schemaguard

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Requirement is one object a service cannot run without.
//
// Columns are optional and should be used sparingly — listing every column
// turns this into a schema copy that rots. List the ones whose absence would
// be silently wrong rather than loudly wrong: a missing column the code writes
// to fails at the first request, which is late but obvious; a missing column
// the code *reads* can surface as an empty value that looks like real data.
type Requirement struct {
	// Table is schema-qualified, e.g. "auth.users".
	Table   string
	Columns []string
}

// Verify checks every requirement and returns a single error naming everything
// that is missing.
//
// All requirements are checked before returning, deliberately: an operator
// fixing a broken deploy needs the whole list, not the first item followed by
// another restart to discover the second.
func Verify(ctx context.Context, db *pgxpool.Pool, service string, reqs []Requirement) error {
	if db == nil {
		return fmt.Errorf("schemaguard: nil db pool")
	}

	var missing []string

	for _, req := range reqs {
		schema, table, ok := splitQualified(req.Table)
		if !ok {
			return fmt.Errorf("schemaguard: %q is not schema-qualified", req.Table)
		}

		var exists bool
		err := db.QueryRow(ctx, `
			SELECT EXISTS (
			    SELECT 1 FROM information_schema.tables
			    WHERE table_schema = $1 AND table_name = $2
			)`, schema, table).Scan(&exists)
		if err != nil {
			return fmt.Errorf("schemaguard: checking %s: %w", req.Table, err)
		}
		if !exists {
			missing = append(missing, "table "+req.Table)
			// No point asking about columns of a table that is not there.
			continue
		}

		for _, col := range req.Columns {
			var colExists bool
			err := db.QueryRow(ctx, `
				SELECT EXISTS (
				    SELECT 1 FROM information_schema.columns
				    WHERE table_schema = $1 AND table_name = $2 AND column_name = $3
				)`, schema, table, col).Scan(&colExists)
			if err != nil {
				return fmt.Errorf("schemaguard: checking %s.%s: %w", req.Table, col, err)
			}
			if !colExists {
				missing = append(missing, "column "+req.Table+"."+col)
			}
		}
	}

	if len(missing) == 0 {
		return nil
	}

	sort.Strings(missing)
	return fmt.Errorf(
		"schemaguard: %s cannot start — %d required object(s) missing from the database: %s. "+
			"Run the schema pipeline for this environment before deploying this build",
		service, len(missing), strings.Join(missing, ", "),
	)
}

func splitQualified(name string) (schema, table string, ok bool) {
	parts := strings.SplitN(name, ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
