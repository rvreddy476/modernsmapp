//go:build integration

package postgres

// A column default that its own CHECK constraint rejects.
//
// `products.approval_status` defaulted to 'pending' and `return_policy_type`
// to 'standard'; migration 001 replaced the first allow-list and the second
// never contained its default. Both defaults were therefore unusable — any
// INSERT omitting the column was rejected outright. Migration 020 realigns
// them; this test is what stops the shape coming back on a different column.
//
// It is a schema contract, not a fixture: it asks the live catalogue whether
// any text column in the database defaults to a value its own CHECK forbids.

import (
	"context"
	"strings"
	"testing"
)

func TestNoColumnDefaultsToAValueItsOwnCheckRejects(t *testing.T) {
	ctx := context.Background()

	rows, err := testPool.Query(ctx, `
		WITH defaults AS (
		    SELECT c.table_name, c.column_name,
		           btrim(split_part(c.column_default, '::', 1), '''') AS default_value
		      FROM information_schema.columns c
		     WHERE c.table_schema = 'public'
		       AND c.column_default LIKE '%''%::text'
		), checks AS (
		    SELECT cl.relname AS table_name, pg_get_constraintdef(co.oid) AS definition
		      FROM pg_constraint co
		      JOIN pg_class cl     ON cl.oid = co.conrelid
		      JOIN pg_namespace n  ON n.oid  = cl.relnamespace
		     WHERE co.contype = 'c' AND n.nspname = 'public'
		)
		SELECT d.table_name, d.column_name, d.default_value
		  FROM defaults d
		  JOIN checks  k ON k.table_name = d.table_name
		 WHERE k.definition LIKE '%(' || d.column_name || ' = ANY%'
		   AND k.definition NOT LIKE '%''' || d.default_value || '''%'
		 ORDER BY 1, 2`)
	if err != nil {
		t.Fatalf("catalogue query: %v", err)
	}
	defer rows.Close()

	var offenders []string
	for rows.Next() {
		var table, column, def string
		if err := rows.Scan(&table, &column, &def); err != nil {
			t.Fatal(err)
		}
		offenders = append(offenders, table+"."+column+" defaults to '"+def+"'")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	if len(offenders) > 0 {
		t.Fatalf("these columns default to a value their own CHECK rejects, so every INSERT "+
			"that omits them fails:\n  %s", strings.Join(offenders, "\n  "))
	}
}
