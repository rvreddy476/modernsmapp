// standing-v1 policy table (Copyright Match plan section 6.4, T4-3). No
// database: EvaluateStanding is a pure function of one snapshot and one
// clock. The store's filter (voided / expired) is exercised in the
// integration tests; here the snapshot is built by hand.
package service

import (
	"errors"
	"testing"
	"time"

	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

var policyNow = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

// strikeAt builds an active strike issued `age` ago that expires 90 days
// after issue.
func strikeAt(severity string, age time.Duration) postgres.UserStrike {
	issued := policyNow.Add(-age)
	return postgres.UserStrike{ID: uuid.New(), UserID: uuid.New(), Reason: "t", Severity: severity,
		PolicyVersion: postgres.StrikePolicyVersion, CreatedAt: issued, ExpiresAt: issued.Add(postgres.StrikeDuration)}
}

func strikes(sev string, n int) []postgres.UserStrike {
	out := make([]postgres.UserStrike, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, strikeAt(sev, time.Duration(i+1)*24*time.Hour))
	}
	return out
}

func TestEvaluateStanding_Table(t *testing.T) {
	day := 24 * time.Hour
	future := policyNow.Add(10 * day)
	past := policyNow.Add(-time.Minute)
	user := uuid.New()

	cases := []struct {
		name           string
		snap           *postgres.StandingSnapshot
		wantStanding   string
		wantSuspended  *time.Time // nil: must be null
		wantStrikeList int
	}{
		{"nil snapshot (no rows anywhere)", nil, StandingOK, nil, 0},
		{"zero strikes", &postgres.StandingSnapshot{}, StandingOK, nil, 0},
		{"one strike", &postgres.StandingSnapshot{ActiveStrikes: strikes(SeverityStrike, 1)}, StandingOK, nil, 1},
		{"two strikes", &postgres.StandingSnapshot{ActiveStrikes: strikes(SeverityStrike, 2)}, StandingOK, nil, 2},
		{"three strikes suspend until the newest expires",
			&postgres.StandingSnapshot{ActiveStrikes: strikes(SeverityStrike, 3)},
			StandingSuspended, ptr(policyNow.Add(-1 * day).Add(postgres.StrikeDuration)), 3},
		{"five warnings never block", &postgres.StandingSnapshot{ActiveStrikes: strikes(SeverityWarning, 5)}, StandingOK, nil, 5},
		{"two strikes plus warnings stay ok",
			&postgres.StandingSnapshot{ActiveStrikes: append(strikes(SeverityStrike, 2), strikes(SeverityWarning, 3)...)}, StandingOK, nil, 5},
		{"one severe strike suspends",
			&postgres.StandingSnapshot{ActiveStrikes: strikes(SeveritySevereStrike, 1)},
			StandingSuspended, ptr(policyNow.Add(-1 * day).Add(postgres.StrikeDuration)), 1},
		{"a strike older than the window does not count (still listed)",
			&postgres.StandingSnapshot{ActiveStrikes: []postgres.UserStrike{
				// Issued 100 days ago under a longer policy: still active, outside the 90-day window.
				func() postgres.UserStrike {
					s := strikeAt(SeverityStrike, 100*day)
					s.ExpiresAt = policyNow.Add(day)
					s.PolicyVersion = "strike-longer"
					return s
				}(),
				strikeAt(SeverityStrike, 2*day), strikeAt(SeverityStrike, 3*day),
			}}, StandingOK, nil, 3},
		{"suspended_until in the future suspends with no strikes",
			&postgres.StandingSnapshot{SuspendedUntil: &future}, StandingSuspended, &future, 0},
		{"suspended_until in the past is ignored",
			&postgres.StandingSnapshot{SuspendedUntil: &past}, StandingOK, nil, 0},
		{"manual suspension and strikes: the later end wins (manual)",
			&postgres.StandingSnapshot{SuspendedUntil: ptr(policyNow.Add(200 * day)), ActiveStrikes: strikes(SeveritySevereStrike, 1)},
			StandingSuspended, ptr(policyNow.Add(200 * day)), 1},
		{"manual suspension and strikes: the later end wins (strike)",
			&postgres.StandingSnapshot{SuspendedUntil: &future, ActiveStrikes: strikes(SeveritySevereStrike, 1)},
			StandingSuspended, ptr(policyNow.Add(-1 * day).Add(postgres.StrikeDuration)), 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EvaluateStanding(user, policyNow, tc.snap)
			if err != nil {
				t.Fatal(err)
			}
			if got.Standing != tc.wantStanding {
				t.Fatalf("standing=%q, want %q", got.Standing, tc.wantStanding)
			}
			if got.PolicyVersion != StandingPolicyVersion || got.UserID != user || !got.EvaluatedAt.Equal(policyNow) {
				t.Fatalf("policy/user/evaluated_at wrong: %+v", got)
			}
			switch {
			case tc.wantSuspended == nil && got.SuspendedUntil != nil:
				t.Fatalf("suspended_until=%v, want null", got.SuspendedUntil)
			case tc.wantSuspended != nil && (got.SuspendedUntil == nil || !got.SuspendedUntil.Equal(*tc.wantSuspended)):
				t.Fatalf("suspended_until=%v, want %v", got.SuspendedUntil, tc.wantSuspended)
			}
			if got.ActiveStrikes == nil || len(got.ActiveStrikes) != tc.wantStrikeList {
				t.Fatalf("active_strikes=%v, want %d entries and never nil", got.ActiveStrikes, tc.wantStrikeList)
			}
		})
	}
}

// A voided or expired strike must never reach the policy: the store filters
// them, and the policy refuses one that slipped through rather than
// counting it (or, worse, silently ignoring a corrupt snapshot).
func TestEvaluateStanding_RefusesInactiveAndUnknown(t *testing.T) {
	user := uuid.New()
	voided := strikeAt(SeverityStrike, time.Hour)
	voided.VoidedAt = ptr(policyNow)
	if _, err := EvaluateStanding(user, policyNow, &postgres.StandingSnapshot{ActiveStrikes: []postgres.UserStrike{voided}}); err == nil {
		t.Fatal("a voided strike in the snapshot must be an error")
	}
	expired := strikeAt(SeverityStrike, 91*24*time.Hour)
	if _, err := EvaluateStanding(user, policyNow, &postgres.StandingSnapshot{ActiveStrikes: []postgres.UserStrike{expired}}); err == nil {
		t.Fatal("an expired strike in the snapshot must be an error")
	}
	unknown := strikeAt("ban", time.Hour)
	_, err := EvaluateStanding(user, policyNow, &postgres.StandingSnapshot{ActiveStrikes: []postgres.UserStrike{unknown}})
	if !errors.Is(err, ErrUnknownSeverity) {
		t.Fatalf("unknown severity: err=%v, want ErrUnknownSeverity (fail closed)", err)
	}
}

func TestIssueStrikeInputValidate(t *testing.T) {
	good := func() IssueStrikeInput {
		return IssueStrikeInput{UserID: uuid.New(), Reason: " spam ", Severity: " Strike ", IdempotencyKey: " k1 "}
	}
	in := good()
	if err := in.Validate(); err != nil || in.Reason != "spam" || in.Severity != "strike" || in.IdempotencyKey != "k1" {
		t.Fatalf("good input: err=%v in=%+v", err, in)
	}
	bad := map[string]func(*IssueStrikeInput){
		"nil user":          func(i *IssueStrikeInput) { i.UserID = uuid.Nil },
		"empty reason":      func(i *IssueStrikeInput) { i.Reason = "  " },
		"bad severity":      func(i *IssueStrikeInput) { i.Severity = "ban" },
		"no idempotency":    func(i *IssueStrikeInput) { i.IdempotencyKey = "" },
		"long idempotency":  func(i *IssueStrikeInput) { i.IdempotencyKey = string(make([]byte, 201)) },
		"nil case id":       func(i *IssueStrikeInput) { z := uuid.Nil; i.CaseID = &z },
		"long strike group": func(i *IssueStrikeInput) { i.StrikeGroup = string(make([]byte, 129)) },
	}
	for name, mutate := range bad {
		in := good()
		mutate(&in)
		if err := in.Validate(); !errors.Is(err, ErrInvalidStrike) {
			t.Fatalf("%s: err=%v, want ErrInvalidStrike", name, err)
		}
	}
}

func ptr(t time.Time) *time.Time { return &t }
