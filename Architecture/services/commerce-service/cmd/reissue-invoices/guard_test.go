package main

import (
	"errors"
	"strings"
	"testing"
)

func TestParseTargetNeverCarriesTheCredentials(t *testing.T) {
	for dsn, want := range map[string]string{
		"postgres://commerce:s3cr3t-pa55@127.0.0.1:5432/commerce_db?sslmode=disable": "127.0.0.1:5432/commerce_db",
		"postgres://u:p@postgres:5432/commerce_it_test":                              "postgres:5432/commerce_it_test",
		"host=localhost user=postgres password=s3cr3t-pa55 dbname=commerce_dev":      "localhost:5432/commerce_dev",
	} {
		tg, err := parseTarget(dsn)
		if err != nil {
			t.Errorf("%s: %v", want, err)
			continue
		}
		if got := tg.String(); got != want {
			t.Errorf("target = %q, want %q", got, want)
		}
		if strings.Contains(tg.String(), "s3cr3t") || strings.Contains(tg.String(), "commerce:") {
			t.Errorf("the printed target carries a credential: %q", tg.String())
		}
	}
	if _, err := parseTarget(""); !errors.Is(err, errRefused) {
		t.Errorf("empty DSN: %v, want errRefused", err)
	}
	// A parse failure must not echo the DSN (and so its password).
	_, err := parseTarget("postgres://u:s3cr3t-pa55@[bad/commerce_db")
	if err == nil || strings.Contains(err.Error(), "s3cr3t") {
		t.Errorf("bad DSN: %v", err)
	}
}

func TestTheGuardRule(t *testing.T) {
	cases := []struct {
		db, allow, env string
		ok             bool
	}{
		// The dev stack, named back, under a development ENV.
		{"commerce_db", "commerce_db", "dev", true},
		{"commerce_db", "commerce_db", "DEV", true},
		{"commerce_db", "commerce_db", " development ", true},
		{"commerce_it_test", "commerce_it_test", "test", true},
		{"commerce_db", "commerce_db", "local", true},
		{"commerce_db", "commerce_db", "ci", true},
		// --allow-db is always required and must match exactly.
		{"commerce_db", "", "dev", false},
		{"commerce_it_test", "", "dev", false},
		{"commerce_dev", "", "dev", false},
		{"commerce_db", "Commerce_DB", "dev", false},
		{"commerce_db", "commerce_it_test", "dev", false},
		{"commerce_db", "yes", "dev", false},
		{"commerce_db", "commerce_db ", "dev", false},
		// Staging and production are refused, whatever is named.
		{"commerce_db", "commerce_db", "prod", false},
		{"commerce_db", "commerce_db", "production", false},
		{"commerce_db", "commerce_db", "staging", false},
		{"commerce_db", "commerce_db", "stage", false},
		{"commerce_db", "commerce_db", "Prod", false},
		// Unknown or blank ENV might be production: refused.
		{"commerce_db", "commerce_db", "", false},
		{"commerce_db", "commerce_db", "qa", false},
		// A "prod" database is refused even when named and under dev.
		{"commerce_prod", "commerce_prod", "dev", false},
		{"production_test", "production_test", "test", false},
		// No database at all.
		{"", "", "dev", false},
	}
	for _, c := range cases {
		err := checkTarget(target{Host: "127.0.0.1", Port: 5432, Database: c.db}, c.allow, c.env)
		if c.ok && err != nil {
			t.Errorf("db=%q allow=%q ENV=%q: refused: %v", c.db, c.allow, c.env, err)
		}
		if !c.ok && err == nil {
			t.Errorf("db=%q allow=%q ENV=%q: allowed, want refusal", c.db, c.allow, c.env)
		}
		if err != nil && !errors.Is(err, errRefused) {
			t.Errorf("db=%q: error is not errRefused: %v", c.db, err)
		}
	}
}
