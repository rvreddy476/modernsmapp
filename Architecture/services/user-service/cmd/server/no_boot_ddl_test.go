package main

import (
	"os"
	"regexp"
	"testing"
)

// US-L5: the schema lives in database/ (setup.sql and migrations/), applied
// by store.BootstrapSchema, which fails the boot on error. Boot code that
// runs its own DDL drifts from it and only logs its failures.
func TestServerRunsNoDDLOfItsOwn(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if m := regexp.MustCompile(`(?i)\b(CREATE\s+(TABLE|INDEX|UNIQUE\s+INDEX)|ALTER\s+TABLE|DROP\s+(TABLE|INDEX))\b`).Find(src); m != nil {
		t.Fatalf("main.go runs DDL (%q); put it in a database/migrations file", m)
	}
}
