// Package store is doorstep-service's Postgres access (schema `doorstep`).
// Every statement names its schema, so the connection's search_path only has
// to reach the PostGIS types in public.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/atpost/shared/identityroles"
	"github.com/atpost/shared/outbox"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sentinel errors the service layer maps to stable codes.
var (
	ErrNotFound     = errors.New("store: not found")
	ErrConflict     = errors.New("store: conflict")         // unique violation
	ErrOverlap      = errors.New("store: period overlap")   // exclusion violation
	ErrBadReference = errors.New("store: bad reference")    // foreign key violation
	ErrInvalid      = errors.New("store: invalid value")    // check / type violation
	ErrZoneInvalid  = errors.New("store: invalid boundary") // PostGIS refused the boundary
)

// Store is the pgx-backed store.
type Store struct {
	db *pgxpool.Pool
	// roles is the identity role queue (identity_roles.go); nil is a no-op.
	roles *identityroles.Outbox
	// events is the schema-local doorstep.events outbox.
	events *outbox.Queuer
}

// New wraps a pool (nil is allowed for route tests that never reach it).
func New(db *pgxpool.Pool) *Store {
	return &Store{db: db, events: outbox.NewQueuer("doorstep")}
}

// Pool exposes the pool (outbox, health).
func (s *Store) Pool() *pgxpool.Pool { return s.db }

// Actor is the admin a write is audited under: the admin-service token's
// signed act claim and the permission the route required.
type Actor struct {
	UserID     uuid.UUID
	Permission string
}

// mapErr turns driver errors into the sentinels, keeping the cause.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23505":
			return fmt.Errorf("%w: %s", ErrConflict, pg.ConstraintName)
		case "23P01":
			return fmt.Errorf("%w: %s", ErrOverlap, pg.ConstraintName)
		case "23503":
			return fmt.Errorf("%w: %s", ErrBadReference, pg.ConstraintName)
		case "23514", "23502", "22P02", "22007", "22008", "22003", "22023":
			return fmt.Errorf("%w: %s", ErrInvalid, pg.Message)
		}
	}
	return err
}

// adminWrite runs fn and its audit row in one transaction.
func (s *Store) adminWrite(ctx context.Context, a Actor, action, entity string, details any, fn func(tx pgx.Tx) (string, error)) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	entityID, err := fn(tx)
	if err != nil {
		return mapErr(err)
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO doorstep.admin_audit_log (actor_user_id, permission, action, entity, entity_id, details)
		VALUES ($1, $2, $3, $4, $5, $6)`, a.UserID, a.Permission, action, entity, entityID, raw); err != nil {
		return err
	}
	return mapErr(tx.Commit(ctx))
}

// collect scans every row with scan.
func collect[T any](rows pgx.Rows, scan func(pgx.Rows) (T, error)) ([]T, error) {
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
