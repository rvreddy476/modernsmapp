package main

// The rule that keeps this tool off a database nobody named.
//
// It rewrites issued GST invoices — legal documents — in place. The founder
// approved that for the ₹0 invoices on DEV; whether, and how, a production
// buyer is told their invoice changed is still the founder's decision. So
// the tool refuses unless the operator types the target database's name back
// (--allow-db) AND the environment is a development one. Same shape as
// cmd/seed-demo/guard.go, stricter: there is no database name that is
// allowed without --allow-db, and ENV is checked as well.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// errRefused is the sentinel for every "not that database" outcome so main
// can print the rule rather than a connection error.
var errRefused = errors.New("refusing to correct invoices")

// target is where the DSN points, without its credentials.
type target struct {
	Host     string
	Port     uint16
	Database string
}

// String is safe to print: host, port and database, never the user's
// password (nor the user).
func (t target) String() string {
	return fmt.Sprintf("%s:%d/%s", t.Host, t.Port, t.Database)
}

// parseTarget reads the DSN without connecting. pgx's parser accepts both
// URL and keyword=value forms, the same set the service accepts for
// POSTGRES_DSN.
func parseTarget(dsn string) (target, error) {
	if strings.TrimSpace(dsn) == "" {
		return target{}, fmt.Errorf("%w: POSTGRES_DSN is empty", errRefused)
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		// pgx's parse errors can quote the DSN; never pass them on.
		return target{}, fmt.Errorf("%w: POSTGRES_DSN does not parse", errRefused)
	}
	return target{Host: cfg.Host, Port: cfg.Port, Database: cfg.Database}, nil
}

// developmentEnvironments is the closed list of ENV values this tool runs
// under — the service's own "local" class (cmd/server classifyPIIEnvironment).
// staging/stage/prod/production are refused, and so is anything unknown or
// blank: an environment that cannot be classified might be production.
var developmentEnvironments = map[string]bool{
	"dev": true, "development": true, "local": true, "test": true, "ci": true,
}

// checkTarget decides whether the tool may touch database `t` under ENV
// `env` with --allow-db=`allow`. The rule, in order:
//
//  1. ENV must be a development environment (closed list above).
//  2. The DSN must name a database, and a name containing "prod" is refused
//     whatever the flags say.
//  3. --allow-db must repeat the database name EXACTLY (case-sensitive). No
//     name is allowed without it — not even a *_test one — because typing
//     the name is the acknowledgement; --allow-db=yes is no check at all.
func checkTarget(t target, allow, env string) error {
	e := strings.ToLower(strings.TrimSpace(env))
	if !developmentEnvironments[e] {
		return fmt.Errorf("%w: ENV=%q is not a development environment (want one of dev, development, "+
			"local, test, ci); staging and production are never corrected by this tool", errRefused, env)
	}
	if t.Database == "" {
		return fmt.Errorf("%w: the DSN names no database", errRefused)
	}
	if strings.Contains(strings.ToLower(t.Database), "prod") {
		return fmt.Errorf("%w: %q looks like a production database and nothing overrides that", errRefused, t.Database)
	}
	if allow == "" || allow != t.Database {
		return fmt.Errorf("%w: --allow-db must name the target database exactly; pass --allow-db=%s to "+
			"correct invoices in %s", errRefused, t.Database, t)
	}
	return nil
}
