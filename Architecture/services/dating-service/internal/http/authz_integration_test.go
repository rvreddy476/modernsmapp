// DB-backed authorization tests for dating-service (Dating plan lane D1):
// the admin 200 paths and the audit actor, the internal routes serving real
// data, and vouch visibility. Requires TEST_PG_DSN on a database whose name
// ends in "_test"; skipped when the DSN is unset.
package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/dating-service/database"
	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/dating-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type authzEnv struct {
	r    *gin.Engine
	st   *store.Store
	pool *pgxpool.Pool
}

func setupAuthzIT(t *testing.T) *authzEnv {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping dating authorization integration tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to run against database %q: name must end in _test", cfg.ConnConfig.Database)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.BootstrapSchema(ctx, pool); err != nil {
		t.Fatalf("bootstrap schema: %v", err)
	}
	st := store.New(pool)
	svc := service.New(st, nil)
	svc.SetMessageClient(&stubMessageClient{})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	New(svc).WithInternalKey(testInternalKey).RegisterRoutes(r)
	return &authzEnv{r: r, st: st, pool: pool}
}

func seedProfile(t *testing.T, st *store.Store, id uuid.UUID) {
	t.Helper()
	intent := "casual"
	if _, err := st.UpsertProfile(context.Background(), id, store.UpsertProfileParams{Intent: &intent}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
}

func auditFor(t *testing.T, st *store.Store, resource string) []*store.AdminAuditEntry {
	t.Helper()
	all, err := st.ListAdminAudit(context.Background(), store.AdminAuditFilter{}, 200, 0)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	var out []*store.AdminAuditEntry
	for _, e := range all {
		if e.TargetResource == resource {
			out = append(out, e)
		}
	}
	return out
}

// assertAdminGate runs the anonymous → 401 and plain user → 403 legs of one
// admin mutation, then the admin leg, and returns the admin response.
func assertAdminGate(t *testing.T, env *authzEnv, method, path, body string, admin uuid.UUID, scope string) {
	t.Helper()
	w := serve(env.r, method, path, body, map[string]string{headerInternalKey: testInternalKey})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous %s: status=%d body=%s, want 401", path, w.Code, w.Body.String())
	}
	w = serve(env.r, method, path, body, gatewayUser(uuid.New(), "feed:read"))
	if w.Code != http.StatusForbidden || errorCode(t, w) != CodeAdminScopeRequired {
		t.Fatalf("plain user %s: status=%d body=%s, want 403 %s", path, w.Code, w.Body.String(), CodeAdminScopeRequired)
	}
	hdr := gatewayUser(admin, scope)
	hdr["X-Admin-Id"] = uuid.NewString() // a forged actor must not be recorded
	w = serve(env.r, method, path, body, hdr)
	if w.Code != http.StatusOK {
		t.Fatalf("admin %s: status=%d body=%s, want 200", path, w.Code, w.Body.String())
	}
}

func TestAuthzIT_ReportAction_AuditActorIsAdmin(t *testing.T) {
	env := setupAuthzIT(t)
	ctx := context.Background()
	report, err := env.st.CreateReport(ctx, uuid.New(), uuid.New(), "spam", "d1 test")
	if err != nil {
		t.Fatalf("seed report: %v", err)
	}
	admin := uuid.New()
	path := "/v1/dating/admin/reports/" + report.ID.String() + "/action"
	assertAdminGate(t, env, http.MethodPost, path, `{"action":"dismiss"}`, admin, "admin")

	rows := auditFor(t, env.st, "report:"+report.ID.String())
	if len(rows) != 1 {
		t.Fatalf("audit rows for report = %d, want exactly 1 (refused requests must not audit)", len(rows))
	}
	if rows[0].ActorAdminID != admin {
		t.Fatalf("audit actor = %s, want the admin's gateway user id %s", rows[0].ActorAdminID, admin)
	}
}

func TestAuthzIT_PhotoModeration_OwnerCannotApprove(t *testing.T) {
	env := setupAuthzIT(t)
	ctx := context.Background()
	owner := uuid.New()
	seedProfile(t, env.st, owner)
	photo, err := env.st.CreatePhoto(ctx, owner, store.CreatePhotoParams{MediaID: uuid.New(), IsPrimary: true})
	if err != nil {
		t.Fatalf("seed photo: %v", err)
	}
	path := "/v1/dating/photos/" + photo.ID.String() + "/moderation"

	// The owner, through the gateway, tries to approve their own photo.
	w := serve(env.r, http.MethodPost, path, `{"status":"approved"}`, gatewayUser(owner, ""))
	if w.Code != http.StatusForbidden {
		t.Fatalf("owner self-approve: status=%d body=%s, want 403", w.Code, w.Body.String())
	}
	var status string
	if err := env.pool.QueryRow(ctx, `SELECT moderation_status FROM dating_photos WHERE id=$1`, photo.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		t.Fatalf("photo status after refused self-approve = %q, want pending", status)
	}

	moderator := uuid.New()
	assertAdminGate(t, env, http.MethodPost, path, `{"status":"approved"}`, moderator, "moderator")
	rows := auditFor(t, env.st, "photo:"+photo.ID.String())
	if len(rows) != 1 || rows[0].ActorAdminID != moderator {
		t.Fatalf("photo audit rows=%d actor=%v, want 1 row by %s", len(rows), actorOf(rows), moderator)
	}
}

func TestAuthzIT_PanicAck_Audited(t *testing.T) {
	env := setupAuthzIT(t)
	ctx := context.Background()
	user := uuid.New()
	if err := env.st.RecordSafetyEvent(ctx, user, "panic", map[string]any{"d1": true}); err != nil {
		t.Fatalf("seed panic: %v", err)
	}
	var panicID uuid.UUID
	if err := env.pool.QueryRow(ctx, `SELECT id FROM dating_safety_events WHERE user_id=$1 AND kind='panic'`, user).Scan(&panicID); err != nil {
		t.Fatal(err)
	}
	admin := uuid.New()
	assertAdminGate(t, env, http.MethodPost, "/v1/dating/admin/safety/panic/"+panicID.String()+"/ack", "", admin, "superadmin")

	rows := auditFor(t, env.st, "safety_event:"+panicID.String())
	if len(rows) != 1 || rows[0].ActorAdminID != admin || rows[0].Action != "panic_acknowledged" || rows[0].TargetUserID != user {
		t.Fatalf("panic audit rows=%d actor=%v, want 1 panic_acknowledged row by %s for %s", len(rows), actorOf(rows), admin, user)
	}
	var ackBy uuid.UUID
	if err := env.pool.QueryRow(ctx, `SELECT acknowledged_by FROM dating_safety_events WHERE id=$1`, panicID).Scan(&ackBy); err != nil {
		t.Fatal(err)
	}
	if ackBy != admin {
		t.Fatalf("acknowledged_by = %s, want %s", ackBy, admin)
	}
}

func TestAuthzIT_AdminReadRoutes(t *testing.T) {
	env := setupAuthzIT(t)
	admin := uuid.New()
	for _, path := range []string{
		"/v1/dating/admin/reports",
		"/v1/dating/admin/safety/panic",
		"/v1/dating/admin/photos/pending",
		"/v1/dating/admin/audit",
		"/v1/dating/admin/risk",
	} {
		assertAdminGate(t, env, http.MethodGet, path, "", admin, "admin")
	}
}

func actorOf(rows []*store.AdminAuditEntry) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ActorAdminID)
	}
	return out
}

func TestAuthzIT_InternalPreviewAndRisk(t *testing.T) {
	env := setupAuthzIT(t)
	ctx := context.Background()
	subject := uuid.New()
	seedProfile(t, env.st, subject)
	if _, err := env.pool.Exec(ctx, `UPDATE dating_profiles SET first_name='Asha' WHERE user_id=$1`, subject); err != nil {
		t.Fatal(err)
	}
	serviceCall := map[string]string{headerInternalKey: testInternalKey}

	for _, path := range []string{
		"/v1/dating/internal/profile/" + subject.String() + "/preview",
		"/v1/dating/profile/" + subject.String() + "/preview", // legacy, notification-service today
	} {
		w := serve(env.r, http.MethodGet, path, "", serviceCall)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"first_name":"Asha"`) {
			t.Fatalf("service %s: status=%d body=%s, want 200 with first_name", path, w.Code, w.Body.String())
		}
		if w := serve(env.r, http.MethodGet, path, "", nil); w.Code != http.StatusUnauthorized {
			t.Fatalf("no credential %s: status=%d, want 401", path, w.Code)
		}
		w = serve(env.r, http.MethodGet, path, "", gatewayUser(uuid.New(), ""))
		if w.Code != http.StatusForbidden && w.Code != http.StatusGone {
			t.Fatalf("gateway user %s: status=%d body=%s, want refusal", path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "Asha") {
			t.Fatalf("gateway user %s: response leaked the first name: %s", path, w.Body.String())
		}
	}

	risk := "/v1/dating/internal/risk/" + subject.String()
	if w := serve(env.r, http.MethodGet, risk, "", serviceCall); w.Code != http.StatusOK {
		t.Fatalf("service risk: status=%d body=%s, want 200", w.Code, w.Body.String())
	}
	if w := serve(env.r, http.MethodGet, risk, "", gatewayUser(uuid.New(), "admin")); w.Code != http.StatusForbidden {
		t.Fatalf("gateway admin on internal risk: status=%d, want 403", w.Code)
	}
}

func TestAuthzIT_VouchVisibility(t *testing.T) {
	env := setupAuthzIT(t)
	ctx := context.Background()
	vouchee, stranger := uuid.New(), uuid.New()
	pendingBy, acceptedBy, declinedBy := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{vouchee, stranger, pendingBy, acceptedBy, declinedBy} {
		seedProfile(t, env.st, id)
	}
	mk := func(voucher uuid.UUID, decision string) {
		v, err := env.st.CreateVouchRequest(ctx, voucher, vouchee, "friend", nil, "d1")
		if err != nil {
			t.Fatalf("seed vouch: %v", err)
		}
		if decision != "" {
			if err := env.st.DecideVouch(ctx, v.ID, vouchee, decision); err != nil {
				t.Fatalf("decide vouch: %v", err)
			}
		}
	}
	mk(pendingBy, "")
	mk(acceptedBy, "accepted")
	mk(declinedBy, "declined")

	list := func(status string, hdr map[string]string) []store.Vouch {
		t.Helper()
		path := "/v1/dating/vouches/for/" + vouchee.String()
		if status != "" {
			path += "?status=" + url.QueryEscape(status)
		}
		w := serve(env.r, http.MethodGet, path, "", hdr)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: status=%d body=%s", path, w.Code, w.Body.String())
		}
		var env struct {
			Data []store.Vouch `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return env.Data
	}
	onlyAccepted := func(who string, got []store.Vouch) {
		t.Helper()
		if len(got) != 1 || got[0].Status != "accepted" || got[0].VoucherID != acceptedBy {
			t.Fatalf("%s: got %d vouches %+v, want only the accepted one", who, len(got), got)
		}
	}

	for _, status := range []string{"pending", "declined", "", "accepted"} {
		onlyAccepted("stranger status="+status, list(status, gatewayUser(stranger, "")))
		onlyAccepted("anonymous status="+status, list(status, map[string]string{headerInternalKey: testInternalKey}))
	}

	owner := gatewayUser(vouchee, "")
	if got := list("pending", owner); len(got) != 1 || got[0].Status != "pending" || got[0].VoucherID != pendingBy {
		t.Fatalf("owner status=pending: got %+v, want their pending vouch", got)
	}
	if got := list("declined", owner); len(got) != 1 || got[0].VoucherID != declinedBy {
		t.Fatalf("owner status=declined: got %+v, want their declined vouch", got)
	}
	onlyAccepted("owner default", list("", owner))
}
