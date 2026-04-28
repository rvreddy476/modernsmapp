package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSchemaCheckCatchesRealDrift seeds a fake service with a CHECK
// constraint and a Go struct literal that violates it, then runs the
// detector. Failure here means the regex parsing or the field map broke
// in a way that would let the bug class we hit 4× this session slip
// through silently.
func TestSchemaCheckCatchesRealDrift(t *testing.T) {
	tmp := t.TempDir()
	svc := filepath.Join(tmp, "services", "fake-service")
	must(t, os.MkdirAll(filepath.Join(svc, "database", "migrations"), 0o755))
	must(t, os.MkdirAll(filepath.Join(svc, "internal"), 0o755))

	// SQL: products.status only allows draft/active/archived. The struct
	// in the Go file below sets Status: "wibble" — should be flagged.
	must(t, os.WriteFile(filepath.Join(svc, "database", "setup.sql"), []byte(`
CREATE TABLE products (
    id UUID PRIMARY KEY,
    status TEXT NOT NULL CHECK (status IN ('draft','active','archived'))
);
`), 0o644))

	// Go: a Product struct with a db tag, then a composite literal
	// that violates the constraint.
	must(t, os.WriteFile(filepath.Join(svc, "internal", "model.go"), []byte(`
package internal

type Product struct {
    ID     string ` + "`db:\"id\"`" + `
    Status string ` + "`db:\"status\"`" + `
}

func New() *Product {
    return &Product{Status: "wibble"}
}

func NewValid() *Product {
    return &Product{Status: "active"}
}
`), 0o644))

	cs := harvestConstraints(svc)
	if len(cs) == 0 {
		t.Fatalf("expected to harvest at least one constraint, got 0")
	}
	unc := harvestUnconstrainedColumns(svc, cs)
	violations := scanGoSources(svc, cs, unc)
	if len(violations) != 1 {
		t.Fatalf("expected exactly 1 violation, got %d: %#v", len(violations), violations)
	}
	v := violations[0]
	if v.value != "wibble" {
		t.Errorf("expected violating value 'wibble', got %q", v.value)
	}
	if v.column != "status" {
		t.Errorf("expected column 'status', got %q", v.column)
	}
}

// TestSchemaCheckSkipsAmbiguousColumns confirms the false-positive guard:
// when one table has a column without a CHECK, we can't be sure which
// table the struct targets, so we skip rather than warn. Stops the noisy
// "every status field is wrong" output that masked real bugs in v1.
func TestSchemaCheckSkipsAmbiguousColumns(t *testing.T) {
	tmp := t.TempDir()
	svc := filepath.Join(tmp, "services", "fake-service")
	must(t, os.MkdirAll(filepath.Join(svc, "database"), 0o755))
	must(t, os.MkdirAll(filepath.Join(svc, "internal"), 0o755))

	// Two tables with the same `status` column. orders has a CHECK,
	// shipments does not — so any value should be accepted.
	must(t, os.WriteFile(filepath.Join(svc, "database", "setup.sql"), []byte(`
CREATE TABLE orders (
    id UUID PRIMARY KEY,
    status TEXT NOT NULL CHECK (status IN ('paid','shipped'))
);
CREATE TABLE shipments (
    id UUID PRIMARY KEY,
    status TEXT NOT NULL DEFAULT 'booked'
);
`), 0o644))

	must(t, os.WriteFile(filepath.Join(svc, "internal", "model.go"), []byte(`
package internal

type Shipment struct {
    ID     string ` + "`db:\"id\"`" + `
    Status string ` + "`db:\"status\"`" + `
}

func New() *Shipment {
    return &Shipment{Status: "booked"}
}
`), 0o644))

	cs := harvestConstraints(svc)
	unc := harvestUnconstrainedColumns(svc, cs)
	if _, ok := unc["status"]; !ok {
		t.Fatalf("expected 'status' in unconstrained set, got %v", unc)
	}
	violations := scanGoSources(svc, cs, unc)
	if len(violations) != 0 {
		t.Fatalf("expected 0 violations (ambiguous column), got %d: %#v", len(violations), violations)
	}
}

// TestNearestTableFindsContext anchors the heuristic that maps inline
// CHECK clauses to their parent CREATE TABLE. Without it the constraint
// loses table provenance, which we use both for reporting and to mark
// other-table-unconstrained columns as ambiguous.
func TestNearestTableFindsContext(t *testing.T) {
	body := `
CREATE TABLE foo (
    id UUID,
    status TEXT CHECK (status IN ('a','b'))
);
ALTER TABLE bar ADD COLUMN status TEXT CHECK (status IN ('c'));
`
	// Index of the first CHECK
	idx := strings.Index(body, "CHECK (status IN ('a'")
	if got := nearestTable(body, idx); got != "foo" {
		t.Errorf("expected 'foo', got %q", got)
	}
	// Index of the second CHECK (after ALTER TABLE bar)
	idx = strings.Index(body, "CHECK (status IN ('c'")
	if got := nearestTable(body, idx); got != "bar" {
		t.Errorf("expected 'bar', got %q", got)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
}
