package service

import (
	"errors"
	"testing"

	"github.com/atpost/media-service/internal/config"
	"github.com/atpost/media-service/internal/processing"
)

func configWithBlocklist(list string) *config.Config {
	return &config.Config{VoiceSafetyBlocklist: list}
}

// Module 1 fixes-v2 / Codex P0-2.
//
// The central rule: AVAILABILITY IS NOT SAFETY. v1 approved a voice asset
// when (a) no transcription backend existed, or (b) a transcript merely
// existed. Neither is a verdict. These tests pin the corrected contract.

func TestUnavailableEvaluator_NeverReportsSafe(t *testing.T) {
	e := processing.UnavailableEvaluator{}
	for _, transcript := range []string{"", "anything at all", "hello world"} {
		v, err := e.EvaluateTranscript(t.Context(), transcript)
		if !errors.Is(err, processing.ErrSafetyUnavailable) {
			t.Fatalf("default evaluator must report unavailable, got err=%v verdict=%+v", err, v)
		}
		if v.IsSafe {
			t.Fatal("the default evaluator must NEVER return a safe verdict")
		}
	}
}

// A missing configuration must select the fail-closed evaluator, not a
// permissive one (the opposite of the image StubScanner's behavior).
func TestSelectAudioSafety_DefaultsToUnavailable(t *testing.T) {
	if got := selectAudioSafety(nil); got.Name() != "unavailable" {
		t.Fatalf("nil config must select the unavailable evaluator, got %q", got.Name())
	}
	if got := selectAudioSafety(configWithBlocklist("")); got.Name() != "unavailable" {
		t.Fatalf("empty blocklist must select the unavailable evaluator, got %q", got.Name())
	}
	if got := selectAudioSafety(configWithBlocklist("bomb,threat")); got.Name() != "keyword_transcript" {
		t.Fatalf("configured blocklist must select a real evaluator, got %q", got.Name())
	}
}

func TestKeywordTranscriptEvaluator(t *testing.T) {
	e := processing.NewKeywordTranscriptEvaluator("bomb, kill you ,threat")
	if e == nil {
		t.Fatal("a non-empty blocklist must produce an evaluator")
	}

	// An empty transcript is not evidence of safety.
	if _, err := e.EvaluateTranscript(t.Context(), "   "); !errors.Is(err, processing.ErrSafetyUnavailable) {
		t.Fatal("an empty transcript must be 'unavailable', not 'safe'")
	}

	// SUPERSEDED BY fixes-v3 / LB-2.
	//
	// This assertion used to read "clean content gets a genuine safe
	// verdict". That was the moderation-bypass contract: absence of ~N
	// configured terms is not evidence of safety across abuse, threats,
	// sexual content, self-harm, coded language, or Indian languages —
	// and combined with owner-editable captions it let a creator approve
	// their own harmful audio. A non-match is now "unavailable", so the
	// asset stays in manual review until a trusted, approval-capable
	// provider rules on it.
	if _, err := e.EvaluateTranscript(t.Context(), "good morning, here is my recipe for filter coffee"); !errors.Is(err, processing.ErrSafetyUnavailable) {
		t.Fatalf("a blocklist non-match must be 'unavailable', not 'safe'; got %v", err)
	}
	if e.CanApprove() {
		t.Fatal("a keyword blocklist must never be approval-capable")
	}

	// A blocked term is still caught — reject capability is retained.
	v, err := e.EvaluateTranscript(t.Context(), "I will bomb the place")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.IsSafe {
		t.Fatal("a blocked term must produce an unsafe verdict")
	}
	if v.Reason == "" {
		t.Error("an unsafe verdict must carry a reason")
	}

	// Word-boundary: a substring must not trip the filter.
	//
	// Under the fixes-v3 contract a non-match yields ErrSafetyUnavailable
	// (manual review) rather than a safe verdict, so the property to
	// assert is "not flagged as unsafe" — i.e. 'bombay' must not be
	// reported as a blocked-term match.
	v, err = e.EvaluateTranscript(t.Context(), "we went to bombay last week")
	if err == nil && !v.IsSafe {
		t.Errorf("substring 'bomb' inside 'bombay' must not trigger a match, got reason %q", v.Reason)
	}
	if err != nil && !errors.Is(err, processing.ErrSafetyUnavailable) {
		t.Errorf("a non-match must be 'unavailable', got %v", err)
	}
}

func TestNewKeywordTranscriptEvaluator_EmptyReturnsNil(t *testing.T) {
	if processing.NewKeywordTranscriptEvaluator("") != nil {
		t.Error("empty blocklist must return nil so callers fall back to fail-closed")
	}
	if processing.NewKeywordTranscriptEvaluator("  , ,, ") != nil {
		t.Error("whitespace-only blocklist must return nil")
	}
}

// The four safety states must stay distinct: 'failed' (no verdict →
// manual review) is NOT 'rejected' (evaluated and unsafe), and neither is
// 'approved'.
func TestVoiceSafetyStatesAreDistinct(t *testing.T) {
	all := map[string]bool{
		VoiceSafetyPending:  true,
		VoiceSafetyApproved: true,
		VoiceSafetyFailed:   true,
		VoiceSafetyRejected: true,
	}
	if len(all) != 4 {
		t.Fatal("the four voice safety states must be distinct values")
	}
	if VoiceSafetyFailed == VoiceSafetyApproved {
		t.Fatal("a missing verdict must never equal approval")
	}
}

func TestValidLanguageTag(t *testing.T) {
	for _, ok := range []string{"en", "hi", "en-IN", "pt-BR"} {
		if !validLanguageTag(ok) {
			t.Errorf("%q should be a valid language tag", ok)
		}
	}
	// VARCHAR(10) — anything longer must be rejected, not truncated.
	for _, bad := range []string{"", "english-language-code", "en_IN", "en 1", "12"} {
		if validLanguageTag(bad) {
			t.Errorf("%q should be rejected", bad)
		}
	}
}
