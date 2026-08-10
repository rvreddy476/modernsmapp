package processing

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

// Module 1 fixes-v2 / Codex P0-2 — real voice safety evaluation.
//
// The v1 code treated two non-answers as "safe":
//   * no transcription backend configured → approved
//   * a transcript exists → approved
//
// Neither is a safety verdict. Availability is not safety. This package
// separates the two explicitly:
//
//	ErrSafetyUnavailable → no evaluator configured / evaluation failed.
//	                        The caller MUST fail closed (manual review),
//	                        never approve.
//	AudioVerdict{IsSafe}  → an actual evaluation happened.

// ErrSafetyUnavailable means no verdict could be produced. Callers route
// the asset to manual review rather than approving it.
var ErrSafetyUnavailable = errors.New("audio safety evaluation unavailable")

// AudioVerdict is the outcome of evaluating a voice recording.
//
// The audit fields exist so a moderation decision can be explained after
// the fact (LB-2: "keep the trusted moderation evidence auditable").
type AudioVerdict struct {
	IsSafe    bool
	Reason    string  // "" when safe; otherwise the triggering category
	Score     float64 // 0.0 safe … 1.0 unsafe
	Evaluator string  // which evaluator produced this
	// Audit metadata. Populated on a best-effort basis by each provider.
	ModelVersion  string   // provider model / policy version
	PolicyVersion string   // our threshold-configuration version
	Language      string   // language the evidence was evaluated in
	Confidence    float64  // provider confidence, when reported
	Categories    []string // triggered categories, when reported
	Threshold     float64  // the threshold applied
}

// AudioSafetyEvaluator produces a verdict for a voice recording, given
// PROVIDER-GENERATED transcription evidence.
//
// Module 1 fixes-v3 / LB-2 requirement 7: trust is declared, never
// inferred. An implementation returning IsSafe=true is NOT sufficient to
// approve content — only an evaluator that also declares
// CanApprove() == true may release a hold. This makes "which providers
// are allowed to say yes" an explicit, reviewable property of the
// contract rather than an accident of which struct happens to be wired.
type AudioSafetyEvaluator interface {
	Name() string
	// CanApprove reports whether this evaluator is trusted to produce a
	// TERMINAL SAFE verdict in production. A signal-only evaluator (e.g.
	// a keyword blocklist) returns false: it may reject, never approve.
	CanApprove() bool
	// EvaluateTranscript returns ErrSafetyUnavailable when it cannot
	// produce a genuine verdict.
	EvaluateTranscript(ctx context.Context, transcript string) (AudioVerdict, error)
}

// UnavailableEvaluator is the DEFAULT when nothing is configured. It
// always returns ErrSafetyUnavailable — it never claims content is safe.
// This is the deliberate opposite of StubScanner (which returns safe) and
// is what makes MEDIA_VOICE_SAFETY_REQUIRED actually mean something.
type UnavailableEvaluator struct{}

func (UnavailableEvaluator) Name() string { return "unavailable" }

// CanApprove is false: an absent provider can never approve anything.
func (UnavailableEvaluator) CanApprove() bool { return false }

func (UnavailableEvaluator) EvaluateTranscript(context.Context, string) (AudioVerdict, error) {
	return AudioVerdict{}, ErrSafetyUnavailable
}

// KeywordTranscriptEvaluator is a real (if basic) evaluator: it screens a
// transcript against a configured blocklist. It is genuinely evaluating
// content, so it is allowed to return a safe verdict — but only when a
// transcript actually exists. An empty transcript is not evidence of
// safety and yields ErrSafetyUnavailable.
type KeywordTranscriptEvaluator struct {
	patterns []*regexp.Regexp
	labels   []string
}

// NewKeywordTranscriptEvaluator builds an evaluator from newline- or
// comma-separated terms. Returns nil when no terms are configured, so
// callers fall back to UnavailableEvaluator rather than a permissive one.
func NewKeywordTranscriptEvaluator(terms string) *KeywordTranscriptEvaluator {
	fields := strings.FieldsFunc(terms, func(r rune) bool {
		return r == ',' || r == '\n' || r == ';'
	})
	e := &KeywordTranscriptEvaluator{}
	for _, f := range fields {
		term := strings.TrimSpace(strings.ToLower(f))
		if term == "" {
			continue
		}
		// Word-boundary match so "class" doesn't trip on a substring.
		re, err := regexp.Compile(`(?i)\b` + regexp.QuoteMeta(term) + `\b`)
		if err != nil {
			continue
		}
		e.patterns = append(e.patterns, re)
		e.labels = append(e.labels, term)
	}
	if len(e.patterns) == 0 {
		return nil
	}
	return e
}

func (KeywordTranscriptEvaluator) Name() string { return "keyword_transcript" }

// CanApprove is FALSE — the core of LB-2 requirement 6.
//
// A keyword blocklist is a useful REJECT signal, but "none of my ~200
// configured terms appeared" is not evidence of safety across abuse,
// threats, sexual content, self-harm, coded language, or any of the
// Indian languages this product launches in. A non-match therefore
// yields no verdict, and the asset stays in manual review.
func (KeywordTranscriptEvaluator) CanApprove() bool { return false }

func (e *KeywordTranscriptEvaluator) EvaluateTranscript(_ context.Context, transcript string) (AudioVerdict, error) {
	if strings.TrimSpace(transcript) == "" {
		// Nothing to evaluate — not a safe verdict.
		return AudioVerdict{}, ErrSafetyUnavailable
	}
	for i, re := range e.patterns {
		if re.MatchString(transcript) {
			return AudioVerdict{
				IsSafe:     false,
				Reason:     "blocked_term:" + e.labels[i],
				Score:      1.0,
				Evaluator:  e.Name(),
				Categories: []string{"blocked_term"},
			}, nil
		}
	}
	// No match. NOT a safe verdict — see CanApprove. Returning
	// ErrSafetyUnavailable here (rather than IsSafe=true) means even a
	// caller that ignored CanApprove cannot be tricked into approving.
	return AudioVerdict{}, ErrSafetyUnavailable
}
