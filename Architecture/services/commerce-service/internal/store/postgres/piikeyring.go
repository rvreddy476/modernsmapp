package postgres

// The PII key ring.
//
// Durable storage for KMS-wrapped data keys, keyed by exactly what the
// ciphertext envelope records: (scope, version). See migration 015 for why
// this must exist — in short, KMS cannot regenerate a historical data key
// from an integer, so the wrapped blob has to be kept or the rows written
// under it become permanently undecryptable.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/atpost/commerce-service/internal/pii"
	"github.com/jackc/pgx/v5"
)

// PIIKeyRing adapts the store to pii.KeyRing.
//
// The port is defined in primitives, so this needs no shared types — only a
// translation of "no live key yet" into the sentinel the provider treats as
// a first run rather than a fault. The dependency points one way (store →
// pii) and pii never imports the store.
type PIIKeyRing struct{ store *Store }

func NewPIIKeyRing(store *Store) *PIIKeyRing { return &PIIKeyRing{store: store} }

func (r *PIIKeyRing) Active(ctx context.Context, scope string) (int, []byte, map[string]string, error) {
	rec, err := r.store.ActivePIIKey(ctx, scope)
	if err != nil {
		if errors.Is(err, ErrNoActiveKey) {
			return 0, nil, nil, pii.ErrNoActiveKey
		}
		return 0, nil, nil, err
	}
	return rec.Version, rec.WrappedKey, rec.EncryptionContext, nil
}

func (r *PIIKeyRing) ByVersion(ctx context.Context, scope string, version int) ([]byte, map[string]string, error) {
	rec, err := r.store.PIIKeyByVersion(ctx, scope, version)
	if err != nil {
		return nil, nil, err
	}
	return rec.WrappedKey, rec.EncryptionContext, nil
}

func (r *PIIKeyRing) Create(
	ctx context.Context,
	scope string,
	wrapped []byte,
	kmsKeyID string,
	encryptionContext map[string]string,
) (int, error) {
	version, err := r.store.CreatePIIKeyVersion(ctx, KeyRecord{
		Scope:             scope,
		WrappedKey:        wrapped,
		KMSKeyID:          kmsKeyID,
		EncryptionContext: encryptionContext,
	})
	if errors.Is(err, ErrActiveKeyExists) {
		// Translate for the provider, which re-reads the winner.
		return version, pii.ErrActiveKeyExists
	}
	return version, err
}

func (r *PIIKeyRing) Rotate(
	ctx context.Context,
	scope string,
	wrapped []byte,
	kmsKeyID string,
	encryptionContext map[string]string,
) (int, error) {
	return r.store.RotatePIIKey(ctx, KeyRecord{
		Scope:             scope,
		WrappedKey:        wrapped,
		KMSKeyID:          kmsKeyID,
		EncryptionContext: encryptionContext,
	})
}

// ErrNoActiveKey means the scope has no live key version yet. The caller
// creates one; it is a first-run state, not a fault.
var ErrNoActiveKey = errors.New("commerce: no active PII key for this scope")

// ErrKeyVersionNotFound means a ciphertext names a version the ring does not
// hold. That is unrecoverable for that value and must surface loudly rather
// than being papered over with the current key, which would decrypt to
// garbage or fail the AEAD tag in a confusing way.
var ErrKeyVersionNotFound = errors.New("commerce: no PII key for that version")

// ErrActiveKeyExists means a live key was installed by someone else between
// the caller reading "none" and asking to create one. The caller discards the
// key it generated — it has encrypted nothing — and re-reads the winner.
var ErrActiveKeyExists = errors.New("commerce: an active PII key already exists for this scope")

// KeyRecord is one wrapped data key.
type KeyRecord struct {
	Scope             string
	Version           int
	WrappedKey        []byte
	KMSKeyID          string
	EncryptionContext map[string]string
}

// ActivePIIKey returns the live key version for a scope.
func (s *Store) ActivePIIKey(ctx context.Context, scope string) (*KeyRecord, error) {
	var rec KeyRecord
	var encCtx []byte
	err := s.db.QueryRow(ctx, `
		SELECT scope, version, wrapped_key, kms_key_id, encryption_context
		  FROM pii_key_ring
		 WHERE scope = $1 AND retired_at IS NULL`, scope).
		Scan(&rec.Scope, &rec.Version, &rec.WrappedKey, &rec.KMSKeyID, &encCtx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNoActiveKey
		}
		return nil, err
	}
	if err := json.Unmarshal(encCtx, &rec.EncryptionContext); err != nil {
		return nil, fmt.Errorf("commerce: decoding encryption context for %s v%d: %w",
			scope, rec.Version, err)
	}
	return &rec, nil
}

// PIIKeyByVersion returns a specific version, retired or not.
//
// A retired key must still be readable: it is what every value written while
// it was current is sealed under. Only rotation stops it being used for NEW
// writes.
func (s *Store) PIIKeyByVersion(ctx context.Context, scope string, version int) (*KeyRecord, error) {
	var rec KeyRecord
	var encCtx []byte
	err := s.db.QueryRow(ctx, `
		SELECT scope, version, wrapped_key, kms_key_id, encryption_context
		  FROM pii_key_ring
		 WHERE scope = $1 AND version = $2`, scope, version).
		Scan(&rec.Scope, &rec.Version, &rec.WrappedKey, &rec.KMSKeyID, &encCtx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s v%d", ErrKeyVersionNotFound, scope, version)
		}
		return nil, err
	}
	if err := json.Unmarshal(encCtx, &rec.EncryptionContext); err != nil {
		return nil, fmt.Errorf("commerce: decoding encryption context for %s v%d: %w",
			scope, version, err)
	}
	return &rec, nil
}

// CreatePIIKeyVersion installs a new live key for a scope and retires the
// previous one, in ONE transaction.
//
// The race this closes: two pods start with an empty ring and both call KMS.
// Each holds a different plaintext key. If both persisted, each would write
// rows the other cannot read, and the damage would be silent until someone
// tried to open an address sealed by the loser.
//
// The partial unique index on (scope) WHERE retired_at IS NULL makes that
// impossible at the database rather than by convention. The loser gets a
// unique violation, discards its key — which has encrypted nothing — and
// re-reads the winner's version. That is why this returns
// [ErrActiveKeyExists] rather than upserting: silently taking over would
// leave whichever pod wrote first with unreadable rows.
func (s *Store) CreatePIIKeyVersion(ctx context.Context, rec KeyRecord) (int, error) {
	return s.writeKeyVersion(ctx, rec, false)
}

// RotatePIIKey retires the live version and installs a new one.
//
// Separate from [Store.CreatePIIKeyVersion] because the two want opposite
// things from an existing active key: first use must NOT create a second one,
// rotation must. Collapsing them is what made eight concurrent first-uses
// produce eight key versions — found by
// TestConcurrentFirstUseConvergesOnOneKey, not by inspection.
func (s *Store) RotatePIIKey(ctx context.Context, rec KeyRecord) (int, error) {
	return s.writeKeyVersion(ctx, rec, true)
}

func (s *Store) writeKeyVersion(ctx context.Context, rec KeyRecord, rotating bool) (int, error) {
	if len(rec.WrappedKey) == 0 {
		return 0, fmt.Errorf("commerce: refusing to store an empty wrapped key")
	}
	if rec.KMSKeyID == "" {
		return 0, fmt.Errorf("commerce: refusing to store a key with no CMK id")
	}
	encCtx, err := json.Marshal(rec.EncryptionContext)
	if err != nil {
		return 0, err
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Serialise writers for this scope so the version number cannot be
	// handed to two of them. Released on commit or rollback.
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"pii_key_ring:"+rec.Scope); err != nil {
		return 0, err
	}

	// Re-check INSIDE the lock. Callers reach here having already asked KMS
	// for a key, so without this every concurrent first-use would install
	// its own version: the first would win the read, the rest would each
	// rotate on top, and callers would walk away holding different keys for
	// what they each believed was version 1.
	var activeVersion int
	err = tx.QueryRow(ctx,
		`SELECT version FROM pii_key_ring WHERE scope = $1 AND retired_at IS NULL`,
		rec.Scope).Scan(&activeVersion)
	switch {
	case err == nil && !rotating:
		// Someone else got there first. The caller discards its key — which
		// has encrypted nothing — and re-reads this one.
		return activeVersion, ErrActiveKeyExists
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		return 0, err
	}

	var next int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(version), 0) + 1 FROM pii_key_ring WHERE scope = $1`,
		rec.Scope).Scan(&next); err != nil {
		return 0, err
	}

	// Retire the current one first, so the partial unique index is satisfied
	// by the insert that follows.
	if _, err := tx.Exec(ctx,
		`UPDATE pii_key_ring SET retired_at = NOW()
		  WHERE scope = $1 AND retired_at IS NULL`, rec.Scope); err != nil {
		return 0, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO pii_key_ring
		    (scope, version, wrapped_key, kms_key_id, encryption_context)
		VALUES ($1,$2,$3,$4,$5)`,
		rec.Scope, next, rec.WrappedKey, rec.KMSKeyID, encCtx); err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return next, nil
}
