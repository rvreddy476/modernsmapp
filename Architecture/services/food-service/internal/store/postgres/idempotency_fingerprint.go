package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/google/uuid"
)

// Audit L5 (2026-10-30): the generic Idempotency-Key routes (restaurant
// order status, rider steps, admin refunds, settlement generation, delivery
// code reissue) share food.idempotency_keys(user_id, key) and stored no
// request hash, so a key reused for a different order, step or amount got
// the other request's stored result back as success and the action never
// ran. The HTTP layer now puts a fingerprint of the request (method, path,
// query and a hash of the body) on the context; lockGenericIdempotency
// stores it, bound to the caller the row is keyed on, and refuses a reuse
// with a different fingerprint with ErrIdempotencyPayloadMismatch (409
// FOOD_IDEMPOTENCY_PAYLOAD_MISMATCH, as checkout already answers).

type requestFingerprintKey struct{}

// WithRequestFingerprint records the request's fingerprint for the generic
// idempotency check. Empty leaves ctx unchanged.
func WithRequestFingerprint(ctx context.Context, fingerprint string) context.Context {
	if fingerprint == "" {
		return ctx
	}
	return context.WithValue(ctx, requestFingerprintKey{}, fingerprint)
}

// RequestFingerprintOf is the fingerprint WithRequestFingerprint recorded,
// or empty.
func RequestFingerprintOf(ctx context.Context) string {
	v, _ := ctx.Value(requestFingerprintKey{}).(string)
	return v
}

// genericIdempotencyHash binds the request fingerprint to the caller. Empty
// when the request carried no fingerprint (an in-process caller): such a row
// is stored with a NULL hash, as rows written before L5 were.
func genericIdempotencyHash(ctx context.Context, userID uuid.UUID) string {
	fp := RequestFingerprintOf(ctx)
	if fp == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("food-generic-idem/v1\n" + userID.String() + "\n" + fp))
	return hex.EncodeToString(sum[:])
}

// idempotencyRequestMismatch reports whether a stored row belongs to another
// request. A row without a hash (written before L5) is not judged, exactly
// as lockIdempotency treats checkout rows.
func idempotencyRequestMismatch(savedHash, hash string) bool {
	return savedHash != "" && savedHash != hash
}

// genericIdempotencyMismatch is ErrIdempotencyPayloadMismatch with a message
// that does not say "checkout".
type genericIdempotencyMismatch struct{}

func (genericIdempotencyMismatch) Error() string {
	return "this Idempotency-Key was already used for a different request"
}

func (genericIdempotencyMismatch) Is(target error) bool {
	return target == ErrIdempotencyPayloadMismatch
}

var errGenericIdempotencyMismatch error = genericIdempotencyMismatch{}
