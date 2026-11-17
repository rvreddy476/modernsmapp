package http

import (
	"testing"

	"github.com/atpost/shared/moderationcap"
	"github.com/google/uuid"
)

// Report resolution (lane post-service-2, 2026-11-18): a signed decision
// with policy_version "report-v1" is recorded as an upheld report (source
// "report", source_ref_id = the report id in decision_id); every other
// policy version stays an appeal outcome, as before.
func TestInternalModerationInputSourcesAReportFromItsPolicyVersion(t *testing.T) {
	post, report, actor := uuid.New(), uuid.New(), uuid.New()
	claims := moderationcap.Claims{
		SubjectID: post.String(), DecisionID: report.String(), ActorID: actor.String(),
		ContentRevision: 7, Decision: "reject", Reason: "Spam", PolicyVersion: "report-v1",
	}
	in, ok := internalModerationInput(claims)
	if !ok {
		t.Fatal("well-formed report claims refused")
	}
	if in.Source != "report" || in.SourceRefID == nil || *in.SourceRefID != report ||
		in.PostID != post || in.ActorID != actor || in.DecisionID != report ||
		in.Action != "reject" || in.Reason != "Spam" || in.ExpectedRevision != 7 {
		t.Fatalf("report input = %+v", in)
	}

	claims.PolicyVersion = "appeal-v1"
	claims.Decision = "approve"
	if in, _ := internalModerationInput(claims); in.Source != "appeal" {
		t.Fatalf("appeal-v1 source = %q, want appeal", in.Source)
	}
	claims.PolicyVersion = "v2-report" // only the prefix marks a report
	if in, _ := internalModerationInput(claims); in.Source != "appeal" {
		t.Fatalf("v2-report source = %q, want appeal", in.Source)
	}

	for name, mutate := range map[string]func(*moderationcap.Claims){
		"subject":  func(c *moderationcap.Claims) { c.SubjectID = "nope" },
		"decision": func(c *moderationcap.Claims) { c.DecisionID = "nope" },
		"actor":    func(c *moderationcap.Claims) { c.ActorID = "" },
	} {
		bad := claims
		mutate(&bad)
		if _, ok := internalModerationInput(bad); ok {
			t.Errorf("malformed %s accepted", name)
		}
	}
}
