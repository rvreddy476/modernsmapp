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
	// LevelOrganisation is the Official mark: a registry-confirmed GSTIN or
	// CIN, a code proven on the organisation's own e-mail domain, and a
	// person-verified signatory.
	LevelOrganisation = "organisation"
	// LevelGovernment is the Government mark: invitation-only, two distinct
	// admins approve the invitation.
	LevelGovernment = "government"
)

// badge_flags bits.
const (
	// BadgePersonVerified is set when the account's holder passed person
	// verification. It stays set when the account goes on to earn the
	// Official mark (the signatory is still a verified person).
	BadgePersonVerified = 1 << 0
	// BadgeOrganisation is set exactly when verification_level is
	// LevelOrganisation.
	BadgeOrganisation = 1 << 1
	// BadgeGovernment is set exactly when verification_level is
	// LevelGovernment.
	BadgeGovernment = 1 << 2

	// BadgeAllMarks is every verification bit: what a rename that loses
	// the mark clears.
	BadgeAllMarks = BadgePersonVerified | BadgeOrganisation | BadgeGovernment
)

// MarkedLevels are the levels that carry a mark a rename can lose.
var MarkedLevels = []string{LevelPerson, LevelOrganisation, LevelGovernment}

// Reasons carried on identity.verification.changed.
const (
	ReasonPersonVerified       = "person_verified"
	ReasonOrganisationVerified = "organisation_verified"
	ReasonGovernmentVerified   = "government_verified"
	ReasonDisplayNameChanged   = "display_name_changed"
)
