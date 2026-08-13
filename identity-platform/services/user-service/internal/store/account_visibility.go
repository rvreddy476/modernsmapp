package store

// Module 3 M3-P0-5 / SR-5 — the storage-level guarantee.
//
// The HTTP handler refuses a non-public account_visibility, which is what
// tells a client author the feature does not exist. This clamp is the
// guarantee: whatever reaches the store, the column can only ever hold a value
// the platform actually enforces.
//
// Both matter, and they are not redundant. The handler check is a message; the
// clamp is an invariant. Without the clamp, any other path into the store — an
// internal caller, a data backfill, a handler added next month — can persist
// "private", and a stored "private" that nothing honours is precisely the
// false promise this item removes: the user believes they are protected and
// their posts still reach the public feed and search.

// PublicAccountVisibility is the only value the platform can enforce today.
const PublicAccountVisibility = "public"

// ClampAccountVisibility forces any value to the one the platform enforces.
// requested is accepted and ignored on purpose: the signature documents that
// a caller may ask for anything, and the answer is always the enforceable
// value. When private accounts are genuinely built, this is the single place
// that changes.
func ClampAccountVisibility(requested string) string {
	_ = requested
	return PublicAccountVisibility
}
