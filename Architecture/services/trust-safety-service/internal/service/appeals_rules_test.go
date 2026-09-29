// Appeal binding rules (Copyright Match plan section 6.3, P-3), no
// database: what a subject read means at submission and at adjudication.
// The transitions themselves are in appeals_integration_test.go.
package service

import (
	"errors"
	"testing"

	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

func ptrUUID(id uuid.UUID) *uuid.UUID { return &id }
func ptrInt64(v int64) *int64         { return &v }

func TestAppealSubmissionCheck_EveryRule(t *testing.T) {
	t.Parallel()
	owner, other, d1, d2 := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	rejected := func() *PostModerationSubject {
		return &PostModerationSubject{PostID: uuid.New(), AuthorID: owner, ReviewStatus: "rejected", ContentRevision: 3, LastDecisionID: ptrUUID(d1), LastDecisionSource: "admin"}
	}
	cases := []struct {
		name      string
		subject   func() *PostModerationSubject
		user      uuid.UUID
		requested *uuid.UUID
		want      error
	}{
		{"nil subject", func() *PostModerationSubject { return nil }, owner, nil, ErrAppealNotEligible},
		{"not the author", rejected, other, nil, ErrAppealNotEligible},
		{"deleted", func() *PostModerationSubject { s := rejected(); s.Deleted = true; return s }, owner, nil, ErrAppealNotEligible},
		{"approved", func() *PostModerationSubject { s := rejected(); s.ReviewStatus = "approved"; return s }, owner, nil, ErrAppealNotEligible},
		{"pending", func() *PostModerationSubject { s := rejected(); s.ReviewStatus = "pending"; return s }, owner, nil, ErrAppealNotEligible},
		{"needs_changes is appealable", func() *PostModerationSubject { s := rejected(); s.ReviewStatus = "needs_changes"; return s }, owner, nil, nil},
		{"rejected is appealable", rejected, owner, nil, nil},
		// The copyright rule comes before the status rule: a copyright hold
		// on an approved base is turned to the counter-notice, not "not
		// appealable".
		{"copyright source, rejected", func() *PostModerationSubject { s := rejected(); s.LastDecisionSource = "copyright"; return s }, owner, nil, ErrAppealCopyrightCase},
		{"copyright source, approved", func() *PostModerationSubject {
			s := rejected()
			s.ReviewStatus, s.LastDecisionSource = "approved", "Copyright "
			return s
		}, owner, nil, ErrAppealCopyrightCase},
		// A non-author never learns the post is under a copyright case.
		{"copyright source, not the author", func() *PostModerationSubject { s := rejected(); s.LastDecisionSource = "copyright"; return s }, other, nil, ErrAppealNotEligible},
		{"absent source is not copyright", func() *PostModerationSubject { s := rejected(); s.LastDecisionSource = ""; return s }, owner, nil, nil},
		{"named decision is current", rejected, owner, ptrUUID(d1), nil},
		{"named decision is stale", rejected, owner, ptrUUID(d2), ErrAppealDecisionStale},
		// Without an exposed id the named decision cannot be checked and is
		// not stored; the appeal binds to the current decision.
		{"named decision, none exposed", func() *PostModerationSubject { s := rejected(); s.LastDecisionID = nil; return s }, owner, ptrUUID(d2), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := appealSubmissionCheck(tc.subject(), tc.user, tc.requested); !errors.Is(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAdjudicate_DecisionIdThenStatusThenRevision(t *testing.T) {
	t.Parallel()
	d1, d2 := uuid.New(), uuid.New()
	bound := func() *postgres.ContentAppeal {
		return &postgres.ContentAppeal{ActionTaken: "rejected", AppealedDecisionID: ptrUUID(d1), AppealedRevision: ptrInt64(7)}
	}
	legacy := func() *postgres.ContentAppeal { return &postgres.ContentAppeal{ActionTaken: "rejected"} }
	subj := func(status string, rev int64, id *uuid.UUID, source string) *PostModerationSubject {
		return &PostModerationSubject{ReviewStatus: status, ContentRevision: rev, LastDecisionID: id, LastDecisionSource: source}
	}
	cases := []struct {
		name    string
		appeal  *postgres.ContentAppeal
		subject *PostModerationSubject
		want    adjudication
	}{
		{"same id, same revision", bound(), subj("rejected", 7, ptrUUID(d1), "admin"), adjudicationStands},
		{"same id, revision moved", bound(), subj("rejected", 8, ptrUUID(d1), "admin"), adjudicationChanged},
		{"later decision", bound(), subj("rejected", 8, ptrUUID(d2), "admin"), adjudicationSuperseded},
		{"later decision, same revision", bound(), subj("rejected", 7, ptrUUID(d2), "admin"), adjudicationSuperseded},
		// Ids win over status: the same decision with a status another
		// writer changed is "changed" (the revision moved), not superseded.
		{"same id, status drifted", bound(), subj("approved", 8, ptrUUID(d1), "admin"), adjudicationChanged},
		{"copyright decision supersedes whatever the id", bound(), subj("rejected", 7, ptrUUID(d1), "copyright"), adjudicationSuperseded},
		// Bound appeal, post-service stopped exposing ids: the status is the
		// decision.
		{"bound, no id exposed, status same", bound(), subj("rejected", 7, nil, ""), adjudicationStands},
		{"bound, no id exposed, status changed", bound(), subj("approved", 7, nil, ""), adjudicationSuperseded},
		{"bound, no id exposed, revision moved", bound(), subj("rejected", 9, nil, ""), adjudicationChanged},
		// Legacy appeal (nothing captured): status is the decision and no
		// revision fence exists; the overturn binds one under the lock.
		{"legacy, status same", legacy(), subj("rejected", 5, ptrUUID(d2), "admin"), adjudicationStands},
		{"legacy, status changed", legacy(), subj("needs_changes", 5, nil, ""), adjudicationSuperseded},
		{"legacy, copyright", legacy(), subj("rejected", 5, nil, "copyright"), adjudicationSuperseded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := adjudicate(tc.appeal, tc.subject); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// The canonical reason is built from the note fixed at BeginOverturn, so a
// replay signs identical claims.
func TestOverturnReason_IsDeterministic(t *testing.T) {
	t.Parallel()
	note := "  restored after review  "
	if got := overturnReason(&note); got != "Appeal overturned: restored after review" {
		t.Fatalf("got %q", got)
	}
	blank := "   "
	for _, n := range []*string{nil, &blank} {
		if got := overturnReason(n); got != "Appeal overturned" {
			t.Fatalf("got %q", got)
		}
	}
}
