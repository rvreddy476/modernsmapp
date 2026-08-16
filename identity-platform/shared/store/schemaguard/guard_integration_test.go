//go:build integration

package schemaguard

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// These run against live PostgreSQL, deliberately.
//
//	POSTGRES_DSN=postgres://... go test -tags integration ./store/schemaguard/ -v
//
// A fake cannot prove any of this. The whole value of the guard is that it
// reads the real catalog instead of a ledger that records a claim, so a test
// against a stub would only prove that the stub agrees with itself.

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// scratchSchema creates an isolated schema so a failed run cannot leave state
// that changes the next run's result.
func scratchSchema(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	name := fmt.Sprintf("schemaguard_test_%d", time.Now().UnixNano())
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+name); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "DROP SCHEMA "+name+" CASCADE"); err != nil {
			t.Logf("cleanup drop schema %s: %v", name, err)
		}
	})
	if _, err := pool.Exec(ctx,
		"CREATE TABLE "+name+".present (id int PRIMARY KEY, kept text)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	return name
}

func TestVerifyPassesWhenEverythingExists(t *testing.T) {
	pool := testPool(t)
	sch := scratchSchema(t, pool)

	err := Verify(context.Background(), pool, "svc", []Requirement{
		{Table: sch + ".present", Columns: []string{"id", "kept"}},
	})
	if err != nil {
		t.Fatalf("expected pass, got: %v", err)
	}
}

func TestVerifyNamesMissingTable(t *testing.T) {
	pool := testPool(t)
	sch := scratchSchema(t, pool)

	err := Verify(context.Background(), pool, "svc", []Requirement{
		{Table: sch + ".present"},
		{Table: sch + ".absent"},
	})
	if err == nil {
		t.Fatal("expected failure for a missing table")
	}
	if !strings.Contains(err.Error(), "table "+sch+".absent") {
		t.Fatalf("error must name the missing table, got: %v", err)
	}
	if strings.Contains(err.Error(), "table "+sch+".present") {
		t.Fatalf("error must not accuse an existing table, got: %v", err)
	}
}

func TestVerifyNamesMissingColumn(t *testing.T) {
	pool := testPool(t)
	sch := scratchSchema(t, pool)

	err := Verify(context.Background(), pool, "svc", []Requirement{
		{Table: sch + ".present", Columns: []string{"id", "gone"}},
	})
	if err == nil {
		t.Fatal("expected failure for a missing column")
	}
	if !strings.Contains(err.Error(), "column "+sch+".present.gone") {
		t.Fatalf("error must name the missing column, got: %v", err)
	}
}

// TestVerifyReportsEveryFailure is the one that matters operationally. An
// operator repairing a half-applied pipeline run needs the whole list; a guard
// that stops at the first problem turns one fix into N restarts.
func TestVerifyReportsEveryFailure(t *testing.T) {
	pool := testPool(t)
	sch := scratchSchema(t, pool)

	err := Verify(context.Background(), pool, "svc", []Requirement{
		{Table: sch + ".absent_one"},
		{Table: sch + ".present", Columns: []string{"gone_a", "gone_b"}},
		{Table: sch + ".absent_two"},
	})
	if err == nil {
		t.Fatal("expected failure")
	}
	for _, want := range []string{
		"table " + sch + ".absent_one",
		"table " + sch + ".absent_two",
		"column " + sch + ".present.gone_a",
		"column " + sch + ".present.gone_b",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error is missing %q; got: %v", want, err)
		}
	}
	if !strings.Contains(err.Error(), "4 required object(s) missing") {
		t.Errorf("error should count the failures, got: %v", err)
	}
}

// TestVerifySkipsColumnChecksForMissingTables keeps the operator-facing message
// honest: reporting every column of an absent table as separately missing would
// bury the one fact that matters.
func TestVerifySkipsColumnChecksForMissingTables(t *testing.T) {
	pool := testPool(t)
	sch := scratchSchema(t, pool)

	err := Verify(context.Background(), pool, "svc", []Requirement{
		{Table: sch + ".absent", Columns: []string{"a", "b", "c"}},
	})
	if err == nil {
		t.Fatal("expected failure")
	}
	if strings.Contains(err.Error(), "column ") {
		t.Fatalf("columns of an absent table must not be listed, got: %v", err)
	}
	if !strings.Contains(err.Error(), "1 required object(s) missing") {
		t.Fatalf("expected exactly one reported object, got: %v", err)
	}
}

func TestVerifyRejectsUnqualifiedRequirement(t *testing.T) {
	pool := testPool(t)

	// Unqualified names would resolve through search_path, so the check could
	// pass against a table in a different schema than the service reads. That
	// is a false negative in the direction that hurts, so it is a hard error
	// rather than an entry in the missing list.
	err := Verify(context.Background(), pool, "svc", []Requirement{{Table: "users"}})
	if err == nil {
		t.Fatal("expected failure for an unqualified table name")
	}
	if !strings.Contains(err.Error(), "not schema-qualified") {
		t.Fatalf("error should explain the cause, got: %v", err)
	}
}

// TestVerifyRespectsContextCancellation proves the boot check inherits the
// caller's deadline. Without this, an unreachable database turns a fast,
// legible refusal into a hung pod that a readiness probe has to kill.
func TestVerifyRespectsContextCancellation(t *testing.T) {
	pool := testPool(t)
	sch := scratchSchema(t, pool)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Verify(ctx, pool, "svc", []Requirement{{Table: sch + ".present"}})
	if err == nil {
		t.Fatal("expected an error from a cancelled context")
	}
	if strings.Contains(err.Error(), "missing from the database") {
		t.Fatalf("a cancelled context must not be reported as a missing object: %v", err)
	}
}
