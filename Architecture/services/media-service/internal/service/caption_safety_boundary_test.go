package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// Module 1 fixes-v3 / LB-2 — the moderation bypass, pinned structurally.
//
// The bypass was: CorrectCaption (creator-authored text, owner-only)
// called evaluateVoiceSafety, which is approval-capable. A creator could
// upload harmful audio, submit a sanitized caption while the asset was
// pending, and have their own words release the hold.
//
// Exercising this end-to-end needs Postgres, blob storage, and a
// transcription backend, so the behavioral proof lives in the integration
// suite. What CAN be proven here, deterministically and without any
// infrastructure, is the invariant that actually matters: the call edge
// from owner-editable caption text to the approval-capable safety path
// does not exist. A future edit that reintroduces it fails this test.

// parseServiceFile returns the AST for a file in this package.
func parseServiceFile(t *testing.T, filename string) (*token.FileSet, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}
	return fset, f
}

// callsWithin reports every function called inside the named function.
func callsWithin(f *ast.File, funcName string) []string {
	var calls []string
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != funcName {
			return true
		}
		ast.Inspect(fn.Body, func(inner ast.Node) bool {
			call, ok := inner.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				calls = append(calls, fun.Name)
			case *ast.SelectorExpr:
				calls = append(calls, fun.Sel.Name)
			}
			return true
		})
		return false
	})
	return calls
}

// approvalCapablePath lists functions that can move a media asset toward
// `approved`. Owner-editable text must never reach any of them.
var approvalCapablePath = []string{
	"evaluateVoiceSafety",
	"setVoiceSafety",
	"SetMediaModerationStatus",
	"publishVoiceSafetyResolved",
}

func TestCorrectCaption_DoesNotInvokeApprovalCapablePath(t *testing.T) {
	_, f := parseServiceFile(t, "voice.go")
	calls := callsWithin(f, "CorrectCaption")
	if len(calls) == 0 {
		t.Fatal("CorrectCaption not found in voice.go — has it been renamed?")
	}
	for _, call := range calls {
		for _, forbidden := range approvalCapablePath {
			if call == forbidden {
				t.Fatalf("LB-2 REGRESSION: CorrectCaption calls %q. "+
					"Owner-authored caption text must never reach an "+
					"approval-capable safety path — that is the moderation "+
					"bypass this fix closed.", forbidden)
			}
		}
	}
}

// The safety evaluation must be fed by the provider transcript, not by
// the stored (owner-overwritable) subtitle row.
func TestRunCaptionJob_UsesProviderEvidenceNotStoredRow(t *testing.T) {
	_, f := parseServiceFile(t, "voice.go")
	calls := callsWithin(f, "runCaptionJob")
	if len(calls) == 0 {
		t.Fatal("runCaptionJob not found in voice.go")
	}

	var usesEvidenceAPI bool
	for _, c := range calls {
		if c == "GenerateAutoCaptionsWithEvidence" {
			usesEvidenceAPI = true
		}
	}
	if !usesEvidenceAPI {
		t.Fatal("runCaptionJob must obtain the provider transcript via " +
			"GenerateAutoCaptionsWithEvidence; the stored subtitle row can be " +
			"owner-authored (CreateSubtitle returns the existing row when " +
			"edited_by_owner is true) and must not be used as safety evidence")
	}
}

// evaluateVoiceSafety must consult CanApprove before approving. This
// pins requirement 7: trust is declared, never inferred from IsSafe.
func TestEvaluateVoiceSafety_ChecksApprovalCapability(t *testing.T) {
	_, f := parseServiceFile(t, "voice.go")
	calls := callsWithin(f, "evaluateVoiceSafety")
	if len(calls) == 0 {
		t.Fatal("evaluateVoiceSafety not found in voice.go")
	}
	var checksCanApprove bool
	for _, c := range calls {
		if c == "CanApprove" {
			checksCanApprove = true
		}
	}
	if !checksCanApprove {
		t.Fatal("evaluateVoiceSafety must gate approval on evaluator.CanApprove(); " +
			"a safe-looking result from a signal-only evaluator must not release a hold")
	}
}

// Guard the comment-level contract too: the file must still document why
// the caption→safety edge is absent, so the next editor understands it
// was deliberate rather than an oversight.
func TestVoiceFileDocumentsTrustBoundary(t *testing.T) {
	_, f := parseServiceFile(t, "voice.go")
	var text strings.Builder
	for _, cg := range f.Comments {
		text.WriteString(cg.Text())
	}
	body := text.String()
	for _, phrase := range []string{"LB-2", "moderation"} {
		if !strings.Contains(body, phrase) {
			t.Errorf("voice.go should retain the %q rationale so the trust "+
				"boundary is not silently removed later", phrase)
		}
	}
}
