//go:build integration

// Strike and standing routes end to end against trust_safety_it_test
// (Copyright Match plan T4-2, T5-2, T5-5): the admin token issues and voids
// with the token's act as the audit actor, the standing route answers from
// the same rows, and the legacy issuing route writes nothing.
package http

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/atpost/shared/servicetoken"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type strikeBody struct {
	Data postgres.UserStrike `json:"data"`
}

type voidBody struct {
	Data struct {
		Strike  postgres.UserStrike `json:"strike"`
		Changed bool                `json:"changed"`
	} `json:"data"`
}

type standingBodyWire struct {
	Data struct {
		Standing       string  `json:"standing"`
		PolicyVersion  string  `json:"policy_version"`
		SuspendedUntil *string `json:"suspended_until"`
		ActiveStrikes  []struct {
			ID       string  `json:"id"`
			Severity string  `json:"severity"`
			CaseID   *string `json:"case_id"`
		} `json:"active_strikes"`
	} `json:"data"`
}

func TestStrikesIntegration_IssueVoidAndStandingThroughTheRoutes(t *testing.T) {
	rg, pool, _ := newIntegrationTokenRig(t)
	ctx := context.Background()
	// post-service for the standing route.
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	if err := rg.v.RegisterBase64("post-service", "p1", pub, []string{OpStandingRead}, nil); err != nil {
		t.Fatal(err)
	}
	post, err := servicetoken.NewSignerFromBase64("post-service", "p1", priv)
	if err != nil {
		t.Fatal(err)
	}
	standing := func(user uuid.UUID) standingBodyWire {
		t.Helper()
		tok, err := post.Mint(AudienceTrustSafety, "standing", []string{OpStandingRead}, nil, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		w := serveAdmin(rg.r, http.MethodGet, "/v1/internal/standing/"+user.String(), "", withKey(bearer(tok)))
		if w.Code != http.StatusOK {
			t.Fatalf("standing: status=%d body=%s", w.Code, w.Body.String())
		}
		var b standingBodyWire
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
			t.Fatal(err)
		}
		return b
	}

	user, forged, caseID := uuid.New(), uuid.New(), uuid.New()
	actor := rg.actor.String()
	manage := rg.mint(t, rg.admin, AudienceTrustSafety, []string{PermStrikesManage}, actor)
	issue := func(body string, requestID string) (int, strikeBody) {
		t.Helper()
		w := serveAdmin(rg.r, http.MethodPost, InternalAdminPrefix+"/strikes", body, withForgedIdentity(manage, forged, requestID))
		var b strikeBody
		_ = json.Unmarshal(w.Body.Bytes(), &b)
		return w.Code, b
	}

	if got := standing(user); got.Data.Standing != "ok" || len(got.Data.ActiveStrikes) != 0 || got.Data.SuspendedUntil != nil {
		t.Fatalf("fresh user standing=%+v", got.Data)
	}

	// Two strikes: still ok. The third suspends. A severe one alone suspends.
	key := func(i int) string { return `"idempotency_key":"it:` + user.String() + `:` + string(rune('a'+i)) + `"` }
	var ids []string
	for i := 0; i < 2; i++ {
		code, b := issue(`{"user_id":"`+user.String()+`","reason":"spam","severity":"strike",`+key(i)+`}`, "rq-issue")
		if code != http.StatusCreated || b.Data.ID == uuid.Nil || b.Data.CreatedBy == nil || b.Data.CreatedBy.String() != actor {
			t.Fatalf("issue %d: status=%d body=%+v (created_by must be the token actor, not %s)", i, code, b.Data, forged)
		}
		ids = append(ids, b.Data.ID.String())
	}
	if got := standing(user); got.Data.Standing != "ok" || len(got.Data.ActiveStrikes) != 2 {
		t.Fatalf("two strikes standing=%+v", got.Data)
	}
	// Replay of the second: 200, same id, no new row.
	if code, b := issue(`{"user_id":"`+user.String()+`","reason":"retry","severity":"severe_strike",`+key(1)+`}`, "rq-replay"); code != http.StatusOK || b.Data.ID.String() != ids[1] || b.Data.Severity != "strike" {
		t.Fatalf("replay: status=%d body=%+v", code, b.Data)
	}
	code, third := issue(`{"user_id":"`+user.String()+`","reason":"copyright upheld","severity":"strike","case_id":"`+caseID.String()+`",`+key(2)+`}`, "rq-third")
	if code != http.StatusCreated {
		t.Fatalf("third: status=%d", code)
	}
	got := standing(user)
	if got.Data.Standing != "suspended" || got.Data.SuspendedUntil == nil || len(got.Data.ActiveStrikes) != 3 || got.Data.PolicyVersion != "standing-v1" {
		t.Fatalf("three strikes standing=%+v", got.Data)
	}
	if got.Data.ActiveStrikes[0].ID != third.Data.ID.String() || got.Data.ActiveStrikes[0].CaseID == nil || *got.Data.ActiveStrikes[0].CaseID != caseID.String() {
		t.Fatalf("newest first with its case: %+v", got.Data.ActiveStrikes)
	}
	if until, err := time.Parse(time.RFC3339, *got.Data.SuspendedUntil); err != nil || !until.Equal(third.Data.ExpiresAt.Truncate(time.Second)) {
		t.Fatalf("suspended_until=%s, want the newest strike's expiry %s (err=%v)", *got.Data.SuspendedUntil, third.Data.ExpiresAt, err)
	}

	// Void the third through the route: standing recovers at once (the
	// 60 s in the plan is the caller's cache, not ours).
	voidPath := InternalAdminPrefix + "/strikes/" + user.String() + "/void"
	w := serveAdmin(rg.r, http.MethodPost, voidPath, `{"strike_id":"`+third.Data.ID.String()+`","reason":"claim withdrawn"}`, withForgedIdentity(manage, forged, "rq-void"))
	var vb voidBody
	if err := json.Unmarshal(w.Body.Bytes(), &vb); err != nil || w.Code != http.StatusOK || !vb.Data.Changed || vb.Data.Strike.VoidedAt == nil || vb.Data.Strike.VoidedBy.String() != actor {
		t.Fatalf("void: status=%d body=%s err=%v", w.Code, w.Body.String(), err)
	}
	w = serveAdmin(rg.r, http.MethodPost, voidPath, `{"strike_id":"`+third.Data.ID.String()+`","reason":"again"}`, withForgedIdentity(manage, forged, "rq-void-2"))
	if err := json.Unmarshal(w.Body.Bytes(), &vb); err != nil || w.Code != http.StatusOK || vb.Data.Changed || *vb.Data.Strike.VoidReason != "claim withdrawn" {
		t.Fatalf("void replay: status=%d body=%s", w.Code, w.Body.String())
	}
	if got := standing(user); got.Data.Standing != "ok" || len(got.Data.ActiveStrikes) != 2 {
		t.Fatalf("after void standing=%+v", got.Data)
	}
	// Audit: issued (rq-third) then voided (rq-void); actor is the token's act.
	if got := auditActors(t, pool, third.Data.ID); len(got) != 2 || got[0] != "user:"+actor || got[1] != "user:"+actor {
		t.Fatalf("audit actors=%v, want two rows by %s", got, actor)
	}
	var actions []string
	rows, err := pool.Query(ctx, `SELECT action || '@' || coalesce(request_id, '') FROM trust.admin_audit WHERE target_id=$1 ORDER BY seq`, third.Data.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, s)
	}
	rows.Close()
	if strings.Join(actions, ",") != "strike.issued@rq-third,strike.voided@rq-void" {
		t.Fatalf("audit actions=%v", actions)
	}
	// Outbox: four rows for the user (3 issued + 1 voided), all unpublished.
	var outbox int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM trust.enforcement_outbox WHERE partition_key = $1 AND published_at IS NULL`, user.String()).Scan(&outbox); err != nil || outbox != 4 {
		t.Fatalf("outbox rows=%d err=%v, want 4", outbox, err)
	}

	// Not found: another user's strike, an unknown strike.
	other := uuid.New()
	if w := serveAdmin(rg.r, http.MethodPost, InternalAdminPrefix+"/strikes/"+other.String()+"/void", `{"strike_id":"`+ids[0]+`","reason":"x"}`, bearer(manage)); w.Code != http.StatusNotFound {
		t.Fatalf("void another user's strike: status=%d", w.Code)
	}
	if w := serveAdmin(rg.r, http.MethodPost, voidPath, `{"strike_id":"`+uuid.NewString()+`","reason":"x"}`, bearer(manage)); w.Code != http.StatusNotFound {
		t.Fatalf("void unknown strike: status=%d", w.Code)
	}

	// Severe alone suspends another user.
	severe := uuid.New()
	if code, _ := issue(`{"user_id":"`+severe.String()+`","reason":"x","severity":"severe_strike","idempotency_key":"sev:`+severe.String()+`"}`, "rq-sev"); code != http.StatusCreated {
		t.Fatalf("severe: status=%d", code)
	}
	if got := standing(severe); got.Data.Standing != "suspended" {
		t.Fatalf("severe standing=%+v", got.Data)
	}

	// The legacy issuing route, with the key and forged admin headers,
	// writes nothing (T5-5).
	before := strikeCount(t, pool)
	if w := serveAdmin(rg.r, http.MethodPost, "/v1/strikes", `{"user_id":"`+user.String()+`","reason":"x","severity":"strike","idempotency_key":"legacy"}`, gatewayAdmin(forged, "admin superadmin")); w.Code != http.StatusGone {
		t.Fatalf("legacy issue: status=%d", w.Code)
	}
	if strikeCount(t, pool) != before {
		t.Fatal("the retired route wrote a strike")
	}
	// Legacy and token reads list active strikes only, expires_at always set.
	readTok := rg.mint(t, rg.admin, AudienceTrustSafety, []string{PermStrikesRead}, actor)
	for _, path := range []string{InternalAdminPrefix + "/strikes/" + user.String(), "/v1/strikes/" + user.String()} {
		hdr := bearer(readTok)
		if strings.HasPrefix(path, "/v1/strikes") {
			hdr = gatewayAdmin(uuid.New(), "admin")
		}
		w := serveAdmin(rg.r, http.MethodGet, path, "", hdr)
		var list struct {
			Data struct {
				Items []postgres.UserStrike `json:"items"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || w.Code != http.StatusOK || len(list.Data.Items) != 2 || list.Data.Items[0].ExpiresAt.IsZero() {
			t.Fatalf("%s: status=%d body=%s err=%v", path, w.Code, w.Body.String(), err)
		}
		for _, it := range list.Data.Items {
			if it.ID == third.Data.ID {
				t.Fatalf("%s listed the voided strike", path)
			}
		}
	}
}

func strikeCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM trust.user_strikes`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
