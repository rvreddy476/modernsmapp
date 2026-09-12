package main

// The rule that keeps demo rows out of a real catalogue.
//
// internal/http's refuseTheLiveDatabase exists because a day of integration
// runs once left the served storefront holding four thousand "Test Product"
// listings. This seeder writes sixteen live, approved products, which is a
// smaller mess but the same kind, and one that would be visible to every
// shopper the moment it landed in production. So the check is on by
// default and the caller has to say, twice, that they mean it.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// errRefused is the sentinel for every "not that database" outcome so main
// can print the rule rather than a connection error.
var errRefused = errors.New("refusing to seed")

// databaseName pulls the database out of a DSN without connecting. pgx's
// parser accepts both URL and keyword=value forms, which is the same set the
// service itself accepts for POSTGRES_DSN.
func databaseName(dsn string) (string, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return "", fmt.Errorf("parse DSN: %w", err)
	}
	return cfg.Database, nil
}

// allowedDatabase decides whether the seeder may write to `name`.
//
// The rule, in the order it is applied:
//
//  1. A name containing "prod" is refused unconditionally. There is no flag
//     that overrides this; if a production database is genuinely named
//     something else, that is what --allow-db is for, and if it is named
//     with "prod" the seeder is simply the wrong tool.
//  2. A name containing "dev" or ending in "_test" is allowed. These are
//     the shapes the docs prescribe for throwaway databases, and
//     commerce_it_test, the integration suite's scratch database, matches.
//  3. Otherwise the exact name must be repeated in --allow-db. The compose
//     stack's dev database is `commerce_db`, which matches neither shape,
//     and renaming it is not this tool's decision. Typing the name is the
//     acknowledgement; a bare --allow-db=yes would be no check at all.
//
// The comparison is case-insensitive for the substrings and exact for the
// --allow-db match, so "Commerce_DB" is not accidentally allowed by a typo
// in the flag.
func allowedDatabase(name, allow string) error {
	if name == "" {
		return fmt.Errorf("%w: the DSN names no database", errRefused)
	}
	lower := strings.ToLower(name)
	if strings.Contains(lower, "prod") {
		return fmt.Errorf("%w: %q looks like a production database and nothing overrides that", errRefused, name)
	}
	if strings.Contains(lower, "dev") || strings.HasSuffix(lower, "_test") {
		return nil
	}
	if allow != "" && allow == name {
		return nil
	}
	return fmt.Errorf("%w: %q neither contains \"dev\" nor ends in \"_test\"; pass --allow-db=%s to seed it anyway", errRefused, name, name)
}
