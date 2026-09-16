// DB-backed admin-service token tests (admin console Wave 2 — Dating): the
// audit actor is the token's signed act claim, and the stats route counts
// what is seeded. Requires TEST_PG_DSN on a "_test" database; skipped when
// unset.
package http

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/dating-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// setupAdminTokenIT is setupAuthzIT's database with a router that also
// verifies admin-service tokens.
func setupAdminTokenIT(t *testing.T) (*authzEnv, *adminTokenRig) {
	t.Helper()
	env := setupAuthzIT(t)
	rg := newAdminTokenRig(t)
	svc := service.New(env.st, nil)
	svc.SetMessageClient(&stubMessageClient{})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	New(svc).WithInternalKey(testInternalKey).WithServiceAuth(rg.v).RegisterRoutes(r)
	env.r = r
	rg.r = r
	return env, rg
}

func TestAdminTokenIT_ReportAction_AuditActorIsAct(t *testing.T) {
	env, rg := setupAdminTokenIT(t)
	ctx := context.Background()
	report, err := env.st.CreateReport(ctx, uuid.New(), uuid.New(), "spam", "wave2 token test")
	if err != nil {
		t.Fatalf("seed report: %v", err)
	}
	path := InternalAdminPrefix + "/reports/" + report.ID.String() + "/action"

	// Wrong scope: refused, and nothing is audited.
	readTok := rg.mint(t, rg.admin, AudienceDating, []string{PermReportsRead}, rg.actor.String())
	if w := serve(env.r, http.MethodPost, path, `{"action":"dismiss"}`, bearer(readTok)); w.Code != http.StatusForbidden {
		t.Fatalf("reports.read token: status=%d body=%s, want 403", w.Code, w.Body.String())
	}

	// Right scope, with a forged gateway identity riding along: the actor
	// recorded is the token's act, never the header.
	tok := rg.mint(t, rg.admin, AudienceDating, []string{PermReportsAct}, rg.actor.String())
	hdr := gatewayUser(uuid.New(), "superadmin")
	hdr["X-Admin-Id"] = uuid.NewString()
	hdr[ServiceAuthHeader] = "Bearer " + tok
	w := serve(env.r, http.MethodPost, path, `{"action":"dismiss"}`, hdr)
	if w.Code != http.StatusOK {
		t.Fatalf("admin-service token: status=%d body=%s, want 200", w.Code, w.Body.String())
	}
	rows := auditFor(t, env.st, "report:"+report.ID.String())
	if len(rows) != 1 || rows[0].ActorAdminID != rg.actor {
		t.Fatalf("audit rows=%d actors=%v, want exactly 1 row by act %s", len(rows), actorOf(rows), rg.actor)
	}
}

func TestAdminTokenIT_PanicDetail_AuditedWithAct(t *testing.T) {
	env, rg := setupAdminTokenIT(t)
	ctx := context.Background()
	user := uuid.New()
	lat, lng := 12.9716, 77.5946
	out, err := env.st.RecordPanicIncident(ctx, store.RecordPanicParams{UserID: user, Source: store.PanicSourcePanic, Latitude: &lat, Longitude: &lng})
	if err != nil {
		t.Fatalf("seed panic: %v", err)
	}
	path := InternalAdminPrefix + "/safety/panic/" + out.Incident.ID.String()

	// panic.read (the list permission) does not open the GPS detail.
	listTok := rg.mint(t, rg.admin, AudienceDating, []string{PermPanicRead}, rg.actor.String())
	if w := serve(env.r, http.MethodGet, path, "", bearer(listTok)); w.Code != http.StatusForbidden {
		t.Fatalf("panic.read token on detail: status=%d, want 403", w.Code)
	}
	if rows := auditFor(t, env.st, "panic_incident:"+out.Incident.ID.String()); len(rows) != 0 {
		t.Fatalf("a refused detail view wrote %d audit rows", len(rows))
	}

	tok := rg.mint(t, rg.admin, AudienceDating, []string{PermPanicReveal}, rg.actor.String())
	w := serve(env.r, http.MethodGet, path, "", bearer(tok))
	if w.Code != http.StatusOK {
		t.Fatalf("panic.reveal token: status=%d body=%s, want 200", w.Code, w.Body.String())
	}
	rows := auditFor(t, env.st, "panic_incident:"+out.Incident.ID.String())
	if len(rows) != 1 || rows[0].ActorAdminID != rg.actor {
		t.Fatalf("panic detail audit rows=%d actors=%v, want 1 row by act %s", len(rows), actorOf(rows), rg.actor)
	}
}

func adminStatsVia(t *testing.T, env *authzEnv, rg *adminTokenRig) store.AdminStats {
	t.Helper()
	tok := rg.mint(t, rg.admin, AudienceDating, []string{PermStatsRead}, rg.actor.String())
	w := serve(env.r, http.MethodGet, InternalAdminPrefix+"/stats", "", bearer(tok))
	if w.Code != http.StatusOK {
		t.Fatalf("stats: status=%d body=%s, want 200", w.Code, w.Body.String())
	}
	var env2 struct {
		Data store.AdminStats `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env2); err != nil {
		t.Fatalf("decode stats: %v body=%s", err, w.Body.String())
	}
	return env2.Data
}

func TestAdminTokenIT_Stats(t *testing.T) {
	env, rg := setupAdminTokenIT(t)
	ctx := context.Background()

	// Token only: the key plus a gateway superadmin gets nothing.
	if w := serve(env.r, http.MethodGet, InternalAdminPrefix+"/stats", "", gatewayUser(uuid.New(), "superadmin")); w.Code != http.StatusUnauthorized {
		t.Fatalf("stats without a token: status=%d, want 401", w.Code)
	}
	if w := serve(env.r, http.MethodGet, InternalAdminPrefix+"/stats", "",
		bearer(rg.mint(t, rg.admin, AudienceDating, []string{PermReportsRead}, rg.actor.String()))); w.Code != http.StatusForbidden {
		t.Fatalf("stats with reports.read: status=%d, want 403", w.Code)
	}

	before := adminStatsVia(t, env, rg)

	// Seed: 2 pending reports + 1 dismissed, 1 open panic + 1 resolved,
	// 3 profiles (1 new today, others back-dated 3 and 10 days; one restricted,
	// one suspended), 2 pending photos, 1 selfie in review, 2 matches (1 today,
	// 1 four days ago).
	p1, p2, p3 := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{p1, p2, p3} {
		seedProfile(t, env.st, id)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := env.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed %q: %v", sql, err)
		}
	}
	exec(`UPDATE dating_profiles SET created_at = now() - interval '3 days', profile_status = 'restricted' WHERE user_id = $1`, p2)
	exec(`UPDATE dating_profiles SET created_at = now() - interval '10 days', profile_status = 'suspended' WHERE user_id = $1`, p3)

	for i := 0; i < 2; i++ {
		if _, err := env.st.CreateReport(ctx, uuid.New(), uuid.New(), "spam", "stats"); err != nil {
			t.Fatalf("seed report: %v", err)
		}
	}
	dismissed, err := env.st.CreateReport(ctx, uuid.New(), uuid.New(), "spam", "stats")
	if err != nil {
		t.Fatalf("seed report: %v", err)
	}
	if err := env.st.SetReportStatus(ctx, dismissed.ID, "dismissed"); err != nil {
		t.Fatalf("dismiss: %v", err)
	}

	if _, err := env.st.RecordPanicIncident(ctx, store.RecordPanicParams{UserID: uuid.New(), Source: store.PanicSourcePanic}); err != nil {
		t.Fatalf("seed panic: %v", err)
	}
	resolved, err := env.st.RecordPanicIncident(ctx, store.RecordPanicParams{UserID: uuid.New(), Source: store.PanicSourcePanic})
	if err != nil {
		t.Fatalf("seed panic: %v", err)
	}
	exec(`UPDATE dating_panic_incidents SET status = 'resolved', acknowledged_at = now(), resolved_at = now() WHERE id = $1`, resolved.Incident.ID)

	for i := 0; i < 2; i++ {
		if _, err := env.st.CreatePhoto(ctx, p1, store.CreatePhotoParams{MediaID: uuid.New(), IsPrimary: i == 0}); err != nil {
			t.Fatalf("seed photo: %v", err)
		}
	}
	exec(`INSERT INTO dating_verifications (user_id, selfie_status, selfie_at) VALUES ($1, 'pending_review', now())
	      ON CONFLICT (user_id) DO UPDATE SET selfie_status = 'pending_review', selfie_at = now()`, p1)

	a, b := uuid.New(), uuid.New()
	if a.String() > b.String() {
		a, b = b, a
	}
	c, d := uuid.New(), uuid.New()
	if c.String() > d.String() {
		c, d = d, c
	}
	exec(`INSERT INTO dating_matches (user_a, user_b) VALUES ($1, $2)`, a, b)
	exec(`INSERT INTO dating_matches (user_a, user_b, matched_at) VALUES ($1, $2, now() - interval '4 days')`, c, d)

	after := adminStatsVia(t, env, rg)

	// The seeded profiles start as drafts; none is active.
	want := map[string][2]int{
		"reports_pending":       {before.ReportsPending, after.ReportsPending - 2},
		"panic_open":            {before.PanicOpen, after.PanicOpen - 1},
		"photos_pending_review": {before.PhotosPendingReview, after.PhotosPendingReview - 2},
		"selfies_in_review":     {before.SelfiesInReview, after.SelfiesInReview - 1},
		"profiles_active":       {before.ProfilesActive, after.ProfilesActive},
		"profiles_restricted":   {before.ProfilesRestricted, after.ProfilesRestricted - 1},
		"profiles_suspended":    {before.ProfilesSuspended, after.ProfilesSuspended - 1},
		"profiles_new_7d":       {before.ProfilesNew7d, after.ProfilesNew7d - 2},
		"matches_7d":            {before.Matches7d, after.Matches7d - 2},
	}
	for name, v := range want {
		if v[0] != v[1] {
			t.Errorf("%s: before=%d, after minus seeded=%d", name, v[0], v[1])
		}
	}
	// "Today" starts at midnight India time, so a row back-dated 3 or 4 days
	// never counts and a row created now always does.
	if after.ProfilesNewToday-before.ProfilesNewToday != 1 {
		t.Errorf("profiles_new_today delta = %d, want 1", after.ProfilesNewToday-before.ProfilesNewToday)
	}
	if after.MatchesToday-before.MatchesToday != 1 {
		t.Errorf("matches_today delta = %d, want 1", after.MatchesToday-before.MatchesToday)
	}
	if after.GeneratedAt.Before(after.DayStartsAt) || after.GeneratedAt.Sub(after.DayStartsAt) > 24*time.Hour {
		t.Errorf("day_starts_at %s is not within the day before generated_at %s", after.DayStartsAt, after.GeneratedAt)
	}
}
