//go:build integration

// Copyright hold routes end to end against trust_safety_it_test (plan
// section 6.4): the admin token opens a case and places, reads, releases
// and re-places its hold with the token's act as the audit actor and the
// command's actor, a forged identity beside the token changes nothing,
// and every refusal writes nothing.
package http

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/atpost/trust-safety-service/internal/service"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

type holdBody struct {
	Data service.CopyrightHold `json:"data"`
}

func TestCopyrightIntegration_RoutesEndToEnd(t *testing.T) {
	rg, pool, svc := newIntegrationTokenRig(t)
	svc.SetCopyrightStore(postgres.NewCopyrightStore(pool))
	ctx := context.Background()
	actor, forged := rg.actor, uuid.New()
	tok := rg.mint(t, rg.admin, AudienceTrustSafety, []string{PermCopyrightAct}, actor.String())
	post, author := uuid.New(), uuid.New()
	do := func(method, path, body, requestID string) (int, holdBody) {
		t.Helper()
		w := serveAdmin(rg.r, method, path, body, withForgedIdentity(tok, forged, requestID))
		var b holdBody
		_ = json.Unmarshal(w.Body.Bytes(), &b)
		if w.Code >= 400 {
			t.Logf("%s %s → %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Code, b
	}

	code, created := do(http.MethodPost, copyrightCasesPath, `{"subject_post_id":"`+post.String()+`","subject_author_id":"`+author.String()+`","reason_code":"removal_upheld"}`, "rq-create")
	if code != http.StatusCreated || created.Data.Case == nil || created.Data.Enforcement == nil {
		t.Fatalf("create: %d %+v", code, created)
	}
	c, k := created.Data.Case, created.Data.Enforcement
	if c.State != "hold_active" || c.CaseRevision != 1 || c.SubjectPostID != post || c.SubjectAuthorID != author ||
		k.Status != "pending" || k.ExpectedState != "absent" || k.ActorID != actor || k.ReasonCode != "removal_upheld" {
		t.Fatalf("created: case=%+v cmd=%+v (actor must be the token's %s, not %s)", c, k, actor, forged)
	}
	if got := auditActors(t, pool, c.ID); len(got) != 1 || got[0] != "user:"+actor.String() {
		t.Fatalf("audit actors=%v", got)
	}
	var reqID *string
	if err := pool.QueryRow(ctx, `SELECT request_id FROM trust.admin_audit WHERE target_type = 'copyright_case' AND target_id = $1`, c.ID).Scan(&reqID); err != nil || reqID == nil || *reqID != "rq-create" {
		t.Fatalf("request id: %v %v", reqID, err)
	}

	code, got := do(http.MethodGet, copyrightCasesPath+"/"+c.ID.String(), "", "rq-get")
	if code != http.StatusOK || got.Data.Case.ID != c.ID || got.Data.Enforcement.DecisionID != k.DecisionID {
		t.Fatalf("get: %d %+v", code, got)
	}

	code, rel := do(http.MethodPost, copyrightCasesPath+"/"+c.ID.String()+"/release", `{"reason_code":"claim_withdrawn"}`, "rq-release")
	if code != http.StatusOK || rel.Data.Case.State != "hold_released" || rel.Data.Case.CaseRevision != 2 || rel.Data.Enforcement.ExpectedState != "active" || rel.Data.Enforcement.ActorID != actor {
		t.Fatalf("release: %d %+v", code, rel)
	}
	if code, _ := do(http.MethodPost, copyrightCasesPath+"/"+c.ID.String()+"/release", `{"reason_code":"claim_withdrawn"}`, "rq-release-2"); code != http.StatusConflict {
		t.Fatalf("double release: %d", code)
	}
	code, again := do(http.MethodPost, copyrightCasesPath+"/"+c.ID.String()+"/place", `{"reason_code":"reinstated_on_review"}`, "rq-place")
	if code != http.StatusOK || again.Data.Case.State != "hold_active" || again.Data.Case.CaseRevision != 3 || again.Data.Enforcement.ExpectedState != "released" {
		t.Fatalf("re-place: %d %+v", code, again)
	}
	if code, _ := do(http.MethodPost, copyrightCasesPath+"/"+uuid.NewString()+"/release", `{"reason_code":"claim_withdrawn"}`, "rq-404"); code != http.StatusNotFound {
		t.Fatalf("unknown case: %d", code)
	}
	if code, _ := do(http.MethodGet, copyrightCasesPath+"/"+uuid.NewString(), "", "rq-404"); code != http.StatusNotFound {
		t.Fatalf("unknown case get: %d", code)
	}
	// A pinned id replayed: 409, one case.
	if code, _ := do(http.MethodPost, copyrightCasesPath, `{"case_id":"`+c.ID.String()+`","subject_post_id":"`+uuid.NewString()+`","subject_author_id":"`+uuid.NewString()+`","reason_code":"removal_upheld"}`, "rq-dup"); code != http.StatusConflict {
		t.Fatalf("replayed case id: %d", code)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM trust.restriction_commands WHERE case_id = $1`, c.ID).Scan(&n); err != nil || n != 3 {
		t.Fatalf("commands=%d err=%v", n, err)
	}
	if got := auditActors(t, pool, c.ID); len(got) != 3 {
		t.Fatalf("audit rows=%d", len(got))
	}
	// Legacy path: the internal key plus the admin scope never reaches
	// the token family.
	w := serveAdmin(rg.r, http.MethodPost, copyrightCasesPath, `{"subject_post_id":"`+uuid.NewString()+`","subject_author_id":"`+uuid.NewString()+`","reason_code":"removal_upheld"}`, gatewayAdmin(forged, "admin superadmin"))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("legacy admin: %d", w.Code)
	}
}
