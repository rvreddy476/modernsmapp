package service

import (
	"testing"

	"github.com/google/uuid"
)

// LB-1: Canonical media moderation safety gate tests.
//
// Non-owner delivery requires an explicitly approved canonical media verdict
// ("passed" or "approved"). "pending", "manual_review", empty, unknown,
// scanner failure, and "rejected" must never produce protected URLs.
// Uploader preview remains permitted under the documented non-rejected/non-failed policy.

func TestCanonicalMediaModerationNonOwnerMatrix(t *testing.T) {
	viewerID := uuid.New()
	uploaderID := uuid.New() // Non-owner viewer

	tests := []struct {
		name             string
		processingStatus string
		moderationStatus string
		wantTerminal     bool
		wantAllowed      bool
		wantDecision     MediaAccessDecision
		wantReason       string
	}{
		{
			name:             "passed moderation proceeds to content visibility",
			processingStatus: "ready",
			moderationStatus: "passed",
			wantTerminal:     false, // proceeds to story/post checks
		},
		{
			name:             "approved moderation proceeds to content visibility",
			processingStatus: "ready",
			moderationStatus: "approved",
			wantTerminal:     false, // proceeds to story/post checks
		},
		{
			name:             "pending moderation denied",
			processingStatus: "ready",
			moderationStatus: "pending",
			wantTerminal:     true,
			wantAllowed:      false,
			wantDecision:     DecisionDenied,
			wantReason:       "moderation_not_approved",
		},
		{
			name:             "manual_review moderation denied",
			processingStatus: "ready",
			moderationStatus: "manual_review",
			wantTerminal:     true,
			wantAllowed:      false,
			wantDecision:     DecisionDenied,
			wantReason:       "moderation_not_approved",
		},
		{
			name:             "empty moderation status denied",
			processingStatus: "ready",
			moderationStatus: "",
			wantTerminal:     true,
			wantAllowed:      false,
			wantDecision:     DecisionDenied,
			wantReason:       "moderation_not_approved",
		},
		{
			name:             "unknown moderation status denied",
			processingStatus: "ready",
			moderationStatus: "unknown_verdict",
			wantTerminal:     true,
			wantAllowed:      false,
			wantDecision:     DecisionDenied,
			wantReason:       "moderation_not_approved",
		},
		{
			name:             "scanner failure moderation denied",
			processingStatus: "ready",
			moderationStatus: "scanner_failed",
			wantTerminal:     true,
			wantAllowed:      false,
			wantDecision:     DecisionDenied,
			wantReason:       "moderation_not_approved",
		},
		{
			name:             "rejected moderation denied",
			processingStatus: "ready",
			moderationStatus: "rejected",
			wantTerminal:     true,
			wantAllowed:      false,
			wantDecision:     DecisionDenied,
			wantReason:       "moderation_not_approved",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			terminal, res := EvaluateMediaAccessFacts(viewerID, uploaderID, tt.processingStatus, tt.moderationStatus)
			if terminal != tt.wantTerminal {
				t.Fatalf("terminal=%v want %v", terminal, tt.wantTerminal)
			}
			if terminal {
				if res.Allowed != tt.wantAllowed {
					t.Errorf("Allowed=%v want %v", res.Allowed, tt.wantAllowed)
				}
				if res.Decision != tt.wantDecision {
					t.Errorf("Decision=%q want %q", res.Decision, tt.wantDecision)
				}
				if res.Reason != tt.wantReason {
					t.Errorf("Reason=%q want %q", res.Reason, tt.wantReason)
				}
			}
		})
	}
}

func TestCanonicalMediaModerationUploaderPreviewMatrix(t *testing.T) {
	uploaderID := uuid.New()
	viewerID := uploaderID // Owner preview

	tests := []struct {
		name             string
		processingStatus string
		moderationStatus string
		wantAllowed      bool
		wantDecision     MediaAccessDecision
		wantReason       string
	}{
		{
			name:             "uploader preview ready with pending moderation allowed",
			processingStatus: "ready",
			moderationStatus: "pending",
			wantAllowed:      true,
			wantDecision:     DecisionAllowed,
			wantReason:       "uploader_allowed",
		},
		{
			name:             "uploader preview ready with manual_review moderation allowed",
			processingStatus: "ready",
			moderationStatus: "manual_review",
			wantAllowed:      true,
			wantDecision:     DecisionAllowed,
			wantReason:       "uploader_allowed",
		},
		{
			name:             "uploader preview processing not ready allowed",
			processingStatus: "processing",
			moderationStatus: "pending",
			wantAllowed:      true,
			wantDecision:     DecisionNotReady,
			wantReason:       "uploader_not_ready",
		},
		{
			name:             "uploader preview rejected moderation denied",
			processingStatus: "ready",
			moderationStatus: "rejected",
			wantAllowed:      false,
			wantDecision:     DecisionDenied,
			wantReason:       "uploader_rejected_or_failed",
		},
		{
			name:             "uploader preview processing failed denied",
			processingStatus: "failed",
			moderationStatus: "passed",
			wantAllowed:      false,
			wantDecision:     DecisionDenied,
			wantReason:       "uploader_rejected_or_failed",
		},
		{
			name:             "uploader preview processing rejected denied",
			processingStatus: "rejected",
			moderationStatus: "passed",
			wantAllowed:      false,
			wantDecision:     DecisionDenied,
			wantReason:       "uploader_rejected_or_failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			terminal, res := EvaluateMediaAccessFacts(viewerID, uploaderID, tt.processingStatus, tt.moderationStatus)
			if !terminal {
				t.Fatalf("uploader preview must be terminal, got terminal=false")
			}
			if res.Allowed != tt.wantAllowed {
				t.Errorf("Allowed=%v want %v", res.Allowed, tt.wantAllowed)
			}
			if res.Decision != tt.wantDecision {
				t.Errorf("Decision=%q want %q", res.Decision, tt.wantDecision)
			}
			if res.Reason != tt.wantReason {
				t.Errorf("Reason=%q want %q", res.Reason, tt.wantReason)
			}
		})
	}
}

// Negative control test: demonstrates that the flawed condition "deny only rejected"
// (facts.ModerationStatus == "rejected") fails closed-system safety by incorrectly
// permitting pending, manual_review, scanner failure, and uninitialized moderation states.
func TestNegativeControlDenyOnlyRejectedFails(t *testing.T) {
	unapprovedStates := []string{
		"pending",
		"manual_review",
		"",
		"unknown",
		"scanner_failed",
	}

	for _, state := range unapprovedStates {
		t.Run("state_"+state, func(t *testing.T) {
			// Flawed condition from previous commit:
			legacyCheckDenied := (state == "rejected")
			if legacyCheckDenied {
				t.Fatalf("legacy check unexpectedly denied unapproved non-rejected state %q", state)
			}

			// Correct fail-closed rule:
			terminal, res := EvaluateMediaAccessFacts(uuid.New(), uuid.New(), "ready", state)
			if !terminal || res.Allowed || res.Decision != DecisionDenied || res.Reason != "moderation_not_approved" {
				t.Fatalf("fail-closed gate failed to deny non-approved state %q: terminal=%v res=%+v", state, terminal, res)
			}
		})
	}
}
