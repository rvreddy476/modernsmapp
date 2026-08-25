package processing

import (
	"context"
	"errors"
	"testing"
)

// Module 1 fixes-v3 / LB-2 — the trust boundary, at the contract level.
//
// The defect: a keyword blocklist returned a TERMINAL SAFE verdict for
// any non-empty transcript that happened to contain none of its terms.
// Combined with CorrectCaption feeding creator-authored text into the
// evaluator, a creator could approve their own harmful audio by writing
// a clean caption.

func TestKeywordEvaluator_IsNotApprovalCapable(t *testing.T) {
	e := NewKeywordTranscriptEvaluator("slur1,slur2")
	if e == nil {
		t.Fatal("evaluator should build from non-empty terms")
	}
	if e.CanApprove() {
		t.Fatal("a keyword blocklist must NEVER be approval-capable: " +
			"absence of ~N configured terms is not evidence of safety")
	}
}

func TestUnavailableEvaluator_IsNotApprovalCapable(t *testing.T) {
	if (UnavailableEvaluator{}).CanApprove() {
		t.Fatal("an absent provider must never be approval-capable")
	}
	_, err := (UnavailableEvaluator{}).EvaluateTranscript(context.Background(), "anything at all")
	if !errors.Is(err, ErrSafetyUnavailable) {
		t.Fatalf("absent provider must yield ErrSafetyUnavailable, got %v", err)
	}
}

// A keyword MATCH may still reject — that capability is retained.
func TestKeywordEvaluator_MatchRejects(t *testing.T) {
	e := NewKeywordTranscriptEvaluator("bomb,attack")
	v, err := e.EvaluateTranscript(context.Background(), "we will attack them tonight")
	if err != nil {
		t.Fatalf("a match must produce a verdict, got %v", err)
	}
	if v.IsSafe {
		t.Fatal("a blocklist match must be unsafe")
	}
	if v.Reason == "" {
		t.Fatal("rejection must carry an auditable reason")
	}
}

// The core fix: a NON-match is not a safe verdict.
func TestKeywordEvaluator_NonMatchCannotApprove(t *testing.T) {
	e := NewKeywordTranscriptEvaluator("bomb,attack")
	_, err := e.EvaluateTranscript(context.Background(),
		"a perfectly ordinary sentence about filter coffee")
	if !errors.Is(err, ErrSafetyUnavailable) {
		t.Fatalf("a non-match must yield ErrSafetyUnavailable (manual review), got %v", err)
	}
}

// Empty / whitespace-only evidence is never a safe verdict.
func TestKeywordEvaluator_EmptyEvidenceFailsClosed(t *testing.T) {
	e := NewKeywordTranscriptEvaluator("bomb")
	for _, in := range []string{"", "   ", "\n\t"} {
		if _, err := e.EvaluateTranscript(context.Background(), in); !errors.Is(err, ErrSafetyUnavailable) {
			t.Fatalf("empty evidence %q must fail closed, got %v", in, err)
		}
	}
}

// No configured terms ⇒ no evaluator, so callers fall back to
// UnavailableEvaluator rather than to a permissive one.
func TestKeywordEvaluator_NoTermsYieldsNilNotPermissive(t *testing.T) {
	for _, terms := range []string{"", "   ", ",,,", "\n"} {
		if e := NewKeywordTranscriptEvaluator(terms); e != nil {
			t.Fatalf("terms %q must not build a permissive evaluator", terms)
		}
	}
}

// A trusted, approval-capable provider CAN approve — proving the gate is
// a real boundary and not a blanket refusal.
type trustedTestEvaluator struct{ safe bool }

func (trustedTestEvaluator) Name() string     { return "trusted_test" }
func (trustedTestEvaluator) CanApprove() bool { return true }
func (e trustedTestEvaluator) EvaluateTranscript(context.Context, string) (AudioVerdict, error) {
	if e.safe {
		return AudioVerdict{IsSafe: true, Evaluator: "trusted_test", Confidence: 0.98}, nil
	}
	return AudioVerdict{IsSafe: false, Reason: "toxicity", Evaluator: "trusted_test"}, nil
}

func TestTrustedEvaluator_CanApproveAndReject(t *testing.T) {
	safe := trustedTestEvaluator{safe: true}
	if !safe.CanApprove() {
		t.Fatal("a trusted provider must be approval-capable")
	}
	v, err := safe.EvaluateTranscript(context.Background(), "hello")
	if err != nil || !v.IsSafe {
		t.Fatalf("trusted safe verdict expected, got %+v err=%v", v, err)
	}

	unsafe := trustedTestEvaluator{safe: false}
	v, err = unsafe.EvaluateTranscript(context.Background(), "hello")
	if err != nil || v.IsSafe {
		t.Fatalf("trusted unsafe verdict expected, got %+v err=%v", v, err)
	}
}

// Every evaluator in the package must satisfy the interface, so
// CanApprove cannot be forgotten by a future implementation.
func TestEvaluatorsSatisfyInterface(t *testing.T) {
	var _ AudioSafetyEvaluator = UnavailableEvaluator{}
	var _ AudioSafetyEvaluator = &KeywordTranscriptEvaluator{}
	var _ AudioSafetyEvaluator = trustedTestEvaluator{}
}
