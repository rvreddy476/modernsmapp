package schemaguard

import (
	"context"
	"strings"
	"testing"
)

func TestSplitQualified(t *testing.T) {
	// A requirement that is not schema-qualified is a programming error, not a
	// deployment error. "users" would be resolved through search_path at query
	// time, which means the check could pass against a table in a completely
	// different schema than the one the service actually reads.
	cases := []struct {
		name       string
		in         string
		wantSchema string
		wantTable  string
		wantOK     bool
	}{
		{"qualified", "auth.users", "auth", "users", true},
		{"bare name", "users", "", "", false},
		{"empty schema", ".users", "", "", false},
		{"empty table", "auth.", "", "", false},
		{"empty string", "", "", "", false},
		{"three parts keeps the remainder as the table", "a.b.c", "a", "b.c", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema, table, ok := splitQualified(tc.in)
			if ok != tc.wantOK || schema != tc.wantSchema || table != tc.wantTable {
				t.Fatalf("splitQualified(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tc.in, schema, table, ok, tc.wantSchema, tc.wantTable, tc.wantOK)
			}
		})
	}
}

func TestVerifyNilPool(t *testing.T) {
	// Boot order bugs are real: a nil pool reaching this call means the caller
	// skipped connection setup. Returning an error beats a nil dereference
	// panic, because the caller's os.Exit path logs a cause.
	err := Verify(context.Background(), nil, "auth-service", []Requirement{
		{Table: "auth.users"},
	})
	if err == nil {
		t.Fatal("expected an error for a nil pool, got nil")
	}
	if !strings.Contains(err.Error(), "nil db pool") {
		t.Fatalf("error should name the cause, got: %v", err)
	}
}
