package pii

import (
	"fmt"
	"strings"
)

// Mode selects how identifying address fields are written and read.
//
// B4/B5. Encrypting existing PII cannot be one deploy. A single image that
// both stops writing plaintext AND stops reading it would be unable to serve
// any row written before it shipped, and rolling it back would be unable to
// serve any row written after. So the cutover is two deploys with a proven
// state in between:
//
//	ModeDual        write ciphertext AND plaintext; read ciphertext, fall back
//	                to plaintext. Safe to roll back to the previous image,
//	                because the previous image can still read every row.
//	                The backfill runs in this state.
//
//	ModeCiphertext  write ciphertext ONLY; read ciphertext ONLY. Deployed only
//	                once the backfill proves every row has ciphertext. A row
//	                without it is now an error rather than a silent fallback,
//	                because after this point a missing ciphertext means data
//	                loss, not a legacy row.
//
// The gated plaintext scrub runs after ModeCiphertext is live and the old
// writers are drained — never before, because plaintext is the only thing
// that can rebuild a row whose ciphertext turns out to be wrong.
type Mode int

const (
	// ModeDual is the default because it is the only mode that is safe
	// against an unmigrated database.
	ModeDual Mode = iota
	ModeCiphertext
)

func (m Mode) String() string {
	switch m {
	case ModeCiphertext:
		return "ciphertext"
	default:
		return "dual"
	}
}

// WritesPlaintext reports whether identifying plaintext should still be
// written alongside the ciphertext.
func (m Mode) WritesPlaintext() bool { return m == ModeDual }

// AllowsPlaintextRead reports whether a row with no ciphertext may be served
// from its plaintext columns.
//
// False after cutover. At that point every row is supposed to have ciphertext,
// so a row without it is a defect that must surface — silently serving the
// plaintext would hide exactly the failure the cutover exists to eliminate,
// and would keep working right up until the scrub cleared it.
func (m Mode) AllowsPlaintextRead() bool { return m == ModeDual }

// ParseMode resolves the configured cutover mode.
//
// An unrecognised value fails closed rather than defaulting. The two modes
// have opposite failure behaviours, and guessing which one a misspelt manifest
// meant is not a guess worth making.
func ParseMode(raw string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "dual", "dual_write", "dual-write":
		return ModeDual, nil
	case "ciphertext", "ciphertext_only", "ciphertext-only":
		return ModeCiphertext, nil
	default:
		return ModeDual, fmt.Errorf(
			"pii: COMMERCE_PII_CUTOVER=%q is not a recognised cutover mode "+
				"(want \"dual\" or \"ciphertext\")", raw)
	}
}
