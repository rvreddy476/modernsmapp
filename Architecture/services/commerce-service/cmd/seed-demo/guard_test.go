package main

import (
	"errors"
	"testing"
)

func TestDatabaseNameIsReadFromEitherDSNForm(t *testing.T) {
	cases := map[string]string{
		"postgres://postgres:postgres@127.0.0.1:5432/commerce_it_test?sslmode=disable": "commerce_it_test",
		"postgres://u:p@postgres:5432/commerce_db?sslmode=disable":                     "commerce_db",
		"host=localhost user=postgres dbname=commerce_dev sslmode=disable":             "commerce_dev",
	}
	for dsn, want := range cases {
		got, err := databaseName(dsn)
		if err != nil {
			t.Errorf("%s: %v", dsn, err)
			continue
		}
		if got != want {
			t.Errorf("%s: database = %q, want %q", dsn, got, want)
		}
	}
}

func TestTheGuardRule(t *testing.T) {
	cases := []struct {
		name, allow string
		ok          bool
	}{
		// The two shapes the docs prescribe for throwaway databases.
		{"commerce_it_test", "", true},
		{"commerce_dev", "", true},
		{"dev_commerce", "", true},
		{"COMMERCE_DEV", "", true},
		// The compose stack's dev database matches neither and needs the
		// name typed back.
		{"commerce_db", "", false},
		{"commerce_db", "commerce_db", true},
		{"commerce_db", "Commerce_DB", false},
		{"commerce_db", "yes", false},
		// "prod" is refused whatever is passed. A production database that
		// happens to contain "dev" is refused too: "prod" wins.
		{"commerce_prod", "", false},
		{"commerce_prod", "commerce_prod", false},
		{"commerce_dev_production", "", false},
		{"production_test", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		err := allowedDatabase(c.name, c.allow)
		if c.ok && err != nil {
			t.Errorf("%q allow=%q: refused: %v", c.name, c.allow, err)
		}
		if !c.ok && err == nil {
			t.Errorf("%q allow=%q: allowed, want refusal", c.name, c.allow)
		}
		if err != nil && !errors.Is(err, errRefused) {
			t.Errorf("%q: error is not errRefused: %v", c.name, err)
		}
	}
}
