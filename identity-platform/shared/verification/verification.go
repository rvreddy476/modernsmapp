// Package verification is the one place the identity verification marks are
// defined: the profile.profiles.verification_level values and the
// profile.profiles.badge_flags bits. Every service that reads a profile (or
// the identity.verification.changed event) uses these, never its own copy.
//
// Premium is NOT a verification mark (docs/designs/short-posts-design.md
// section 3.4): it is an identity role and never sets a bit here.
package verification

// verification_level values.
const (
	// LevelNone is an unverified profile.
	LevelNone = ""
	// LevelPerson is a real, unique person: DigiLocker name and date of
	// birth plus selfie liveness and face match (the "Verified" tick).
	LevelPerson = "person"
	// LevelOrganisation is the Official mark (phase 3, not built yet).
	LevelOrganisation = "organisation"
	// LevelGovernment is the Government mark (phase 3, not built yet).
	LevelGovernment = "government"
)

// badge_flags bits.
const (
	// BadgePersonVerified is set exactly when verification_level is
	// LevelPerson and is_verified is true.
	BadgePersonVerified = 1 << 0
)

// Reasons carried on identity.verification.changed.
const (
	ReasonPersonVerified     = "person_verified"
	ReasonDisplayNameChanged = "display_name_changed"
)
