package store

import (
	"context"

	"github.com/atpost/identity-shared/store/schemabootstrap"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BootstrapSchema applies the auth-service schema required to run against a
// fresh identity database.
//
// The statement splitter that does the real work now lives in
// identity-shared/store/schemabootstrap. It moved because profile-service and
// user-service need exactly the same thing and could not import it from this
// internal package — which is a large part of why profile-service ended up
// with no schema of its own and served 500s from six tables that were never
// created anywhere.
//
// Moving it also gave it the tests it never had, despite having already caused
// an outage: it used to be `strings.Split(sql, ";")`, which cut setup.sql in
// half at a semicolon inside a comment, so every table declared after that
// line was silently never created. See schemabootstrap for the full account.
//
// This wrapper stays so callers (and the signup journey test) keep one obvious
// entry point for "the schema this service owns".
func BootstrapSchema(ctx context.Context, db *pgxpool.Pool, schemaSQL string) error {
	return schemabootstrap.Apply(ctx, db, schemaSQL)
}
