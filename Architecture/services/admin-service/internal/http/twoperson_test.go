package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/admin-service/internal/approvals"
	"github.com/atpost/admin-service/internal/store/postgres"
)

const (
	remittance  = "9a0c6c1e-0f3a-4d59-9a57-3c1f7d1c2b10"
	payoutBatch = "8b0c6c1e-0f3a-4d59-9a57-3c1f7d1c2b10"
	settlePath  = "/v1/admin/commerce/cod-remittances/" + remittance + "/settle"
	settleBody  = `{"payout_batch_id":"` + payoutBatch + `","reason":"batch 42 paid out"}`
)

// twoHolders is a rig where adminA and adminB both hold cod.settle and identity
// reports another enrolled holder.
func twoHolders(t *testing.T, store approvals.Store) *rig {
	t.Helper()
	rg := newRig(t, rigOpts{store: store})
	rg.perms.grant(adminA, permCODSettle)
	rg.perms.grant(adminB, permCODSettle)
	rg.holders.n = 1
	return rg
}

func approvalFrom(t *testing.T, w *httptest.ResponseRecorder) approvals.Approval {
	t.Helper()
	var env struct {
		Data struct {
			Approval approvals.Approval `json:"approval"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil || env.Data.Approval.ID == "" {
		t.Fatalf("no approval in %s (%v)", w.Body.String(), err)
	}
	return env.Data.Approval
}

// requestSettle submits the settle as adminA and returns the pending approval.
func requestSettle(t *testing.T, rg *rig) approvals.Approval {
	t.Helper()
	w := rg.do(http.MethodPost, settlePath, settleBody, adminA)
	if w.Code != http.StatusAccepted {
		t.Fatalf("first call: status %d %s, want 202", w.Code, w.Body.String())
	}
	return approvalFrom(t, w)
}

func approve(rg *rig, id, actor string, opts ...reqOpt) *httptest.ResponseRecorder {
	return rg.do(http.MethodPost, "/v1/admin/approvals/"+id+"/approve", `{"reason":"checked the batch"}`, actor, opts...)
}

func TestTwoPersonFirstCallCreatesAPendingApproval(t *testing.T) {
	store := newMemStore()
	rg := twoHolders(t, store)
	a := requestSettle(t, rg)

	if hits, _, _, _ := rg.seen.snapshot(); hits != 0 {
		t.Fatal("the first admin's call executed")
	}
	if rg.holders.lastPerm != permCODSettle || rg.holders.lastExclude != adminA {
		t.Fatalf("holders asked for %q excluding %q", rg.holders.lastPerm, rg.holders.lastExclude)
	}
	if a.Status != approvals.StatusPending || a.Requester != adminA || a.RequiredPermission != permCODSettle ||
		a.App != "commerce" || a.Operation != opCODSettle || a.TargetType != "cod_remittance" || a.TargetID != remittance ||
		a.RequesterReason != "batch 42 paid out" || a.PayloadHash == "" {
		t.Fatalf("approval %+v", a)
	}
	if got := a.ExpiresAt.Sub(a.CreatedAt); got != 24*time.Hour {
		t.Fatalf("expiry window %v", got)
	}
	e := rg.onlyEntry(t)
	if e.Actor != adminA || e.Outcome != postgres.AuditOutcomePending || e.StatusCode != http.StatusAccepted ||
		e.Payload["approval"] != "requested" || e.Payload["approval_id"] != a.ID || e.TargetID != remittance || e.Reason != "batch 42 paid out" {
		t.Fatalf("audit %+v", e)
	}

	// A two-person request needs a reason.
	w := rg.do(http.MethodPost, settlePath, `{}`, adminA)
	if w.Code != http.StatusBadRequest || !hasCode(w, CodeReasonRequired) {
		t.Fatalf("no reason: %d %s", w.Code, w.Body.String())
	}
}

func TestTheRequesterCannotApproveTheirOwnRequest(t *testing.T) {
	store := newMemStore()
	rg := twoHolders(t, store)
	a := requestSettle(t, rg)
	rg.rec.entries = nil

	w := approve(rg, a.ID, adminA)
	if w.Code != http.StatusForbidden || !hasCode(w, CodeSelfApproval) {
		t.Fatalf("self-approval: status %d %s", w.Code, w.Body.String())
	}
	if hits, _, _, _ := rg.seen.snapshot(); hits != 0 {
		t.Fatal("self-approval executed")
	}
	if got, _ := store.GetApproval(context.Background(), a.ID); got.Status != approvals.StatusPending {
		t.Fatalf("status after self-approval = %s", got.Status)
	}
	if e := rg.onlyEntry(t); e.Outcome != postgres.AuditOutcomeDenied || e.Payload["code"] != CodeSelfApproval || e.TargetID != remittance {
		t.Fatalf("audit %+v", e)
	}
	// Nor reject it.
	w = rg.do(http.MethodPost, "/v1/admin/approvals/"+a.ID+"/reject", `{"reason":"never mind"}`, adminA)
	if w.Code != http.StatusForbidden || !hasCode(w, CodeSelfApproval) {
		t.Fatalf("self-reject: %d", w.Code)
	}
}

func TestASecondHolderApprovesAndTheExactPayloadExecutesOnce(t *testing.T) {
	store := newMemStore()
	rg := twoHolders(t, store)
	a := requestSettle(t, rg)
	rg.rec.entries = nil

	w := approve(rg, a.ID, adminB)
	if w.Code != http.StatusOK {
		t.Fatalf("approve: status %d %s", w.Code, w.Body.String())
	}
	hits, actor, paths, bodies := rg.seen.snapshot()
	if hits != 1 || actor != adminB || paths[0] != "/v1/commerce/internal/cod-remittances/"+remittance+"/settle" {
		t.Fatalf("commerce saw hits=%d actor=%s paths=%v", hits, actor, paths)
	}
	var sent map[string]string
	if err := json.Unmarshal([]byte(bodies[0]), &sent); err != nil || len(sent) != 1 || sent["payout_batch_id"] != payoutBatch {
		t.Fatalf("commerce body %q", bodies[0])
	}
	got, _ := store.GetApproval(context.Background(), a.ID)
	if got.Status != approvals.StatusExecuted || got.Approver == nil || *got.Approver != adminB ||
		got.ResultStatus == nil || *got.ResultStatus != 200 || *got.ResultOutcome != "success" {
		t.Fatalf("stored approval %+v", got)
	}
	e := rg.onlyEntry(t)
	if e.Actor != adminB || e.App != "commerce" || e.Operation != opCODSettle || e.TargetType != "cod_remittance" ||
		e.TargetID != remittance || e.Outcome != postgres.AuditOutcomeSuccess || e.Payload["approval"] != "approved" ||
		e.Payload["requester"] != adminA || e.Payload["approval_id"] != a.ID || e.Reason != "checked the batch" {
		t.Fatalf("audit %+v", e)
	}

	// A retry, by the same approver or another holder, replays and runs nothing.
	rg.perms.grant(nobody, permCODSettle)
	for _, who := range []string{adminB, nobody} {
		w = approve(rg, a.ID, who)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"replayed":true`) {
			t.Fatalf("retry by %s: %d %s", who, w.Code, w.Body.String())
		}
	}
	if hits, _, _, _ := rg.seen.snapshot(); hits != 1 {
		t.Fatalf("executed %d times", hits)
	}
}

func TestApprovingNeedsTheApprovalsPermissionAndAStepUp(t *testing.T) {
	rg := twoHolders(t, newMemStore())
	a := requestSettle(t, rg)
	rg.perms.grant(nobody, permSellerApprove) // an admin, but not of this permission

	if w := approve(rg, a.ID, nobody); w.Code != http.StatusForbidden || !hasCode(w, CodePermissionDenied) {
		t.Fatalf("approver without cod.settle: %d %s", w.Code, w.Body.String())
	}
	if w := approve(rg, a.ID, adminB, stepUpAgo(10*time.Minute)); w.Code != http.StatusForbidden || !hasCode(w, "STEP_UP_REQUIRED") {
		t.Fatalf("approve with a stale step-up: %d %s", w.Code, w.Body.String())
	}
	if w := rg.do(http.MethodPost, "/v1/admin/approvals/"+a.ID+"/approve", `{}`, adminB); !hasCode(w, CodeReasonRequired) {
		t.Fatalf("approve without a reason: %d", w.Code)
	}
	if hits, _, _, _ := rg.seen.snapshot(); hits != 0 {
		t.Fatal("a refused approval executed")
	}
}

func TestATamperedPayloadIsRefused(t *testing.T) {
	for name, tamper := range map[string]func(*approvals.Approval){
		"payload": func(a *approvals.Approval) {
			a.Payload = []byte(`{"payout_batch_id":"` + payoutBatch + `","remittance_id":"00000000-0000-4000-8000-000000000001"}`)
		},
		"target":    func(a *approvals.Approval) { a.TargetID = "00000000-0000-4000-8000-000000000001" },
		"operation": func(a *approvals.Approval) { a.Operation = "cod.settle.twice" },
	} {
		t.Run(name, func(t *testing.T) {
			store := newMemStore()
			rg := twoHolders(t, store)
			a := requestSettle(t, rg)
			store.mutate(a.ID, tamper)

			w := approve(rg, a.ID, adminB)
			if w.Code != http.StatusConflict || !hasCode(w, CodePayloadHashMismatch) {
				t.Fatalf("status %d %s, want 409 PAYLOAD_HASH_MISMATCH", w.Code, w.Body.String())
			}
			if hits, _, _, _ := rg.seen.snapshot(); hits != 0 {
				t.Fatal("a tampered approval executed")
			}
			if got, _ := store.GetApproval(context.Background(), a.ID); got.Status != approvals.StatusRejected {
				t.Fatalf("tampered approval left %s", got.Status)
			}
			if w := approve(rg, a.ID, adminB); w.Code != http.StatusConflict {
				t.Fatalf("second attempt on a tampered approval: %d", w.Code)
			}
		})
	}
}

func TestAnExpiredApprovalCannotBeApproved(t *testing.T) {
	store := newMemStore()
	rg := twoHolders(t, store)
	a := requestSettle(t, rg)
	store.mutate(a.ID, func(x *approvals.Approval) { x.ExpiresAt = time.Now().Add(-time.Second) })

	w := approve(rg, a.ID, adminB)
	if w.Code != http.StatusGone || !hasCode(w, CodeApprovalExpired) {
		t.Fatalf("status %d %s, want 410 APPROVAL_EXPIRED", w.Code, w.Body.String())
	}
	if hits, _, _, _ := rg.seen.snapshot(); hits != 0 {
		t.Fatal("an expired approval executed")
	}
	if got, _ := store.GetApproval(context.Background(), a.ID); got.Status != approvals.StatusExpired {
		t.Fatalf("status %s, want expired", got.Status)
	}
	if w := rg.do(http.MethodGet, "/v1/admin/approvals", "", adminB); strings.Contains(w.Body.String(), a.ID) {
		t.Fatal("an expired approval is still in the inbox")
	}
}

func TestTheSoleHolderActsAloneAndItIsRecorded(t *testing.T) {
	store := newMemStore()
	rg := newRig(t, rigOpts{store: store})
	rg.perms.grant(adminA, permCODSettle)
	rg.holders.n = 0

	w := rg.do(http.MethodPost, settlePath, settleBody, adminA)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s, want immediate execution", w.Code, w.Body.String())
	}
	hits, actor, _, bodies := rg.seen.snapshot()
	if hits != 1 || actor != adminA || !strings.Contains(bodies[0], payoutBatch) {
		t.Fatalf("commerce saw hits=%d actor=%s body=%v", hits, actor, bodies)
	}
	if len(store.rows) != 0 {
		t.Fatal("a sole-holder action left a pending approval")
	}
	e := rg.onlyEntry(t)
	if e.Actor != adminA || e.Outcome != postgres.AuditOutcomeSuccess || e.Payload["approval"] != "sole_holder" ||
		e.Operation != opCODSettle || e.TargetID != remittance || e.Reason != "batch 42 paid out" {
		t.Fatalf("audit %+v", e)
	}
}

func TestAHoldersCountErrorFailsClosed(t *testing.T) {
	store := newMemStore()
	rg := newRig(t, rigOpts{store: store})
	rg.perms.grant(adminA, permCODSettle)
	rg.holders.err = errIdentityDown

	w := rg.do(http.MethodPost, settlePath, settleBody, adminA)
	if w.Code != http.StatusServiceUnavailable || !hasCode(w, CodeApprovalUnavailable) {
		t.Fatalf("status %d %s, want 503 APPROVAL_UNAVAILABLE", w.Code, w.Body.String())
	}
	if hits, _, _, _ := rg.seen.snapshot(); hits != 0 || len(store.rows) != 0 {
		t.Fatalf("holders error still acted: hits=%d rows=%d", hits, len(store.rows))
	}
	if e := rg.onlyEntry(t); e.Outcome != postgres.AuditOutcomeDenied {
		t.Fatalf("audit %+v", e)
	}
}

func TestTheInboxListsWhatTheCallerMayDecide(t *testing.T) {
	rg := twoHolders(t, newMemStore())
	a := requestSettle(t, rg)
	rg.perms.grant(nobody, permSellerApprove)

	for who, want := range map[string]bool{adminB: true, adminA: false, nobody: false} {
		w := rg.do(http.MethodGet, "/v1/admin/approvals", "", who)
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), a.ID) != want {
			t.Fatalf("inbox for %s: %d %s (want listed=%v)", who, w.Code, w.Body.String(), want)
		}
	}
}

func TestARejectedApprovalNeverExecutes(t *testing.T) {
	store := newMemStore()
	rg := twoHolders(t, store)
	a := requestSettle(t, rg)
	rg.rec.entries = nil

	w := rg.do(http.MethodPost, "/v1/admin/approvals/"+a.ID+"/reject", `{"reason":"batch not paid"}`, adminB)
	if w.Code != http.StatusOK {
		t.Fatalf("reject: %d %s", w.Code, w.Body.String())
	}
	if e := rg.onlyEntry(t); e.Outcome != postgres.AuditOutcomeRejected || e.Actor != adminB || e.TargetID != remittance {
		t.Fatalf("audit %+v", e)
	}
	if w := approve(rg, a.ID, adminB); w.Code != http.StatusConflict || !hasCode(w, CodeApprovalDecided) {
		t.Fatalf("approve after reject: %d %s", w.Code, w.Body.String())
	}
	if hits, _, _, _ := rg.seen.snapshot(); hits != 0 {
		t.Fatal("a rejected approval executed")
	}
	if w := approve(rg, "00000000-0000-4000-8000-000000000009", adminB); w.Code != http.StatusNotFound {
		t.Fatalf("unknown approval: %d", w.Code)
	}
}
