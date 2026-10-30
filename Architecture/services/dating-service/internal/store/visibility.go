// Who may see whom (audit H1, 2026-10-30).
//
// candidateVisibleClauses is THE rule for "this dating profile may be shown
// to this viewer". The discovery deck (FetchCandidates), Nebula's passed list
// (ListPassedCandidates), the single-card loader behind Nebula and rewind
// (GetCandidateForViewer) and the point check (CandidateVisibleTo) all embed
// it, so a profile a person paused, hid, or that moderation holds can never
// reach a surface by a route the deck would have refused.
package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// SetRequireIdentityDOB makes discovery admit only profiles whose birth date
// came from identity (audit L4): a client-supplied birth date is never what
// puts someone in front of other people. On whenever the service has an
// identity reader (every deployment but local/dev without one).
func (s *Store) SetRequireIdentityDOB(on bool) { s.requireIdentityDOB = on }

// identityDOBSources is the SQL list of birth-date sources identity vouches
// for (IsIdentityBirthDateSource).
const identityDOBSources = `('` + BasicsSourceIdentity + `','` + BasicsSourceIdentityRegistration + `','` + BasicsSourceIdentityProfile + `')`

// viewerGender is the viewer's half of the two-way gender rule (lane D10), as
// SQL text expressions: wants is the viewer's interested_in_gender, gender
// the viewer's own gender. Either may evaluate to NULL or ”.
type viewerGender struct {
	wants  string
	gender string
}

// viewerGenderFromDB reads both halves from the viewer's own rows.
func viewerGenderFromDB(viewer string) viewerGender {
	return viewerGender{
		wants:  `(SELECT vpr.interested_in_gender FROM dating_preferences vpr WHERE vpr.user_id = ` + viewer + `)`,
		gender: `(SELECT vpp.gender FROM dating_profiles vpp WHERE vpp.user_id = ` + viewer + `)`,
	}
}

// genderClauses is the two-way gender rule. Forward: the viewer's preference
// must EQUAL the candidate's gender; no preference, an empty one or
// "everyone" applies no filter. Reverse: the candidate's own preference must
// be absent, empty, "everyone" or equal to the viewer's gender; a viewer with
// no gender skips it (nothing to compare, and an empty deck would be worse).
func genderClauses(alias string, g viewerGender) []string {
	return []string{
		`(COALESCE(NULLIF(btrim(` + g.wants + `), ''), 'everyone') = 'everyone' OR ` + alias + `.gender = ` + g.wants + `)`,
		`(COALESCE(btrim(` + g.gender + `), '') = ''
		    OR NOT EXISTS (SELECT 1 FROM dating_preferences cp WHERE cp.user_id = ` + alias + `.user_id)
		    OR EXISTS (SELECT 1 FROM dating_preferences cp
		        WHERE cp.user_id = ` + alias + `.user_id
		          AND COALESCE(NULLIF(btrim(cp.interested_in_gender), ''), 'everyone') IN ('everyone', ` + g.gender + `)))`,
	}
}

// candidateVisibleClauses returns the WHERE clauses (ANDed) under which the
// dating_profiles row aliased alias may be shown to viewer (a SQL uuid
// expression):
//
//   - not the viewer; status active (not draft, pending, paused, under
//     review, restricted — the under-18 hold —, suspended or deleted); the
//     legacy paused flag off; visible_to_public on;
//   - an 18+ birth date (P0-5), from identity when SetRequireIdentityDOB;
//   - an approved primary photo (P0-6);
//   - no block either way (lane D3);
//   - no restrictive risk level (§P0-7): hide_from_discovery, chat_hold,
//     admin_review, suspend;
//   - incognito only toward people it sparked (§P1-3);
//   - the two-way gender rule (lane D10);
//   - hide-known (mechanic M16) while that mechanic is on.
//
// Discovery-only knobs (age window, intent, distance, passes, the decline
// cooldown, verified-only and the pass filters) stay in FetchCandidates.
func (s *Store) candidateVisibleClauses(alias, viewer string, g viewerGender) []string {
	out := []string{
		alias + `.user_id <> ` + viewer,
		// §P1-1 profile-status gate, with the legacy booleans kept as
		// belt-and-braces.
		profileListedPredicate(alias),
		// P0-5: absolute age floor, whatever the viewer's window.
		alias + `.birth_date IS NOT NULL`,
		`EXTRACT(YEAR FROM AGE(` + alias + `.birth_date)) >= 18`,
		// P0-6 / lane D6: an approved primary photo (a match_only or
		// sparked_only primary is served blurred, it does not hide).
		`EXISTS (SELECT 1 FROM dating_photos ph
		    WHERE ph.user_id = ` + alias + `.user_id
		      AND ph.is_primary = true
		      AND ph.moderation_status = 'approved')`,
		`NOT ` + blockedPairPredicate(viewer, alias+".user_id"),
		`NOT EXISTS (SELECT 1 FROM dating_account_risk rar
		    WHERE rar.user_id = ` + alias + `.user_id
		      AND rar.risk_level IN
		          ('hide_from_discovery','chat_hold','admin_review','suspend'))`,
		incognitoVisiblePredicate(alias, viewer),
	}
	out = append(out, genderClauses(alias, g)...)
	if s.requireIdentityDOB {
		out = append(out, alias+`.dob_source IN `+identityDOBSources)
	}
	if s.hideKnownEnabled {
		out = append(out, `NOT `+hideKnownPredicate(viewer, alias+".user_id"))
	}
	return out
}

// profileListedPredicate is true when the profile aliased alias is out in
// the open: active (not onboarding, paused, under review, restricted,
// suspended or deleted), not soft-deleted, the legacy paused flag off, and
// visible_to_public on. It is the owner-side half of candidateVisibleClauses
// and what the photo route requires of a non-match (audit L4).
func profileListedPredicate(alias string) string {
	return `(` + alias + `.profile_status = 'active'
		    AND ` + alias + `.deleted_at IS NULL
		    AND ` + alias + `.paused = false
		    AND ` + alias + `.visible_to_public = true)`
}

// andClauses joins clauses with AND.
func andClauses(clauses []string) string {
	out := ""
	for i, c := range clauses {
		if i > 0 {
			out += "\n          AND "
		}
		out += c
	}
	return out
}

// candidateVisiblePredicate is candidateVisibleClauses as one SQL boolean
// for the profile whose user id is the SQL expression col, with the viewer's
// gender read from the database.
func (s *Store) candidateVisiblePredicate(col, viewer string) string {
	return `EXISTS (SELECT 1 FROM dating_profiles cv
        WHERE cv.user_id = ` + col + `
          AND ` + andClauses(s.candidateVisibleClauses("cv", viewer, viewerGenderFromDB(viewer))) + `)`
}

// CandidateVisibleTo reports whether candidateID's profile may be shown to
// viewerID under candidateVisibleClauses.
func (s *Store) CandidateVisibleTo(ctx context.Context, viewerID, candidateID uuid.UUID) (bool, error) {
	if viewerID == uuid.Nil || candidateID == uuid.Nil {
		return false, nil
	}
	var ok bool
	if err := s.db.QueryRow(ctx, `SELECT `+s.candidateVisiblePredicate("$2::uuid", "$1::uuid"), viewerID, candidateID).Scan(&ok); err != nil {
		return false, fmt.Errorf("candidate visible: %w", err)
	}
	return ok, nil
}

// HasStandingPass reports whether viewerID's pass on candidateID stands: not
// rewound and inside the pass cooldown (database clock, as RecordPass).
func (s *Store) HasStandingPass(ctx context.Context, viewerID, candidateID uuid.UUID) (bool, error) {
	var ok bool
	if err := s.db.QueryRow(ctx, `
        SELECT EXISTS (SELECT 1 FROM dating_passes
            WHERE user_id = $1 AND candidate_id = $2
              AND rewound_at IS NULL
              AND passed_at > now() - make_interval(secs => $3))`,
		viewerID, candidateID, PassCooldown.Seconds()).Scan(&ok); err != nil {
		return false, fmt.Errorf("standing pass: %w", err)
	}
	return ok, nil
}
