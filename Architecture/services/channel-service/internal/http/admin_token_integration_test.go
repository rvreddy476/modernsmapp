// DB-backed admin-service token tests (admin console Wave 2 — Chat): every
// admin decision writes exactly one channel_admin_audit row whose actor is
// the token's signed act claim (never X-User-Id), and the stats route counts
// what is seeded. Requires TEST_PG_DSN on a "_test" database
// (channel_it_test); skipped when unset.
package http

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/atpost/channel-service/database"
	"github.com/atpost/channel-service/internal/service"
	"github.com/atpost/channel-service/internal/store"
	pgstore "github.com/atpost/channel-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupChannelAdminIT(t *testing.T) (*adminTokenRig, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping channel-service admin token integration tests")
	}
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatal("TEST_PG_DSN must parse and name a database ending in _test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pgstore.BootstrapSchema(ctx, pool, database.SetupSQL); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	// Start every test from empty moderation tables so stats are exact.
	// channel_admin_audit is append-only by trigger; TRUNCATE is not a row
	// trigger event, which is what lets a test database reset it.
	if _, err := pool.Exec(ctx, `TRUNCATE channel_admin_audit, channel_reports, broadcast_channels CASCADE`); err != nil {
		t.Fatalf("reset: %v", err)
	}
	rg := newAdminTokenRig(t)
	svc := service.New(store.New(pool), nil)
	h := New(svc).WithInternalKey(adminTestInternalKey).WithServiceAuth(rg.v)
	r := gin.New()
	r.Use(gin.Recovery())
	h.RegisterRoutes(r)
	rg.r = r
	return rg, pool
}

func seedChannel(t *testing.T, pool *pgxpool.Pool, status string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	handle := "it" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO broadcast_channels (owner_id, handle, name, status) VALUES ($1, $2, 'IT channel', $3) RETURNING id`,
		uuid.New(), handle, status).Scan(&id); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	return id
}

func seedChannelReport(t *testing.T, pool *pgxpool.Pool, channelID uuid.UUID, status, reviewedAgo string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	q := `INSERT INTO channel_reports (channel_id, reporter_id, reason, status) VALUES ($1, $2, 'spam', $3) RETURNING id`
	args := []any{channelID, uuid.New(), status}
	if reviewedAgo != "" {
		q = `INSERT INTO channel_reports (channel_id, reporter_id, reason, status, reviewer_id, reviewed_at)
			VALUES ($1, $2, 'spam', $3, $4, NOW() - $5::interval) RETURNING id`
		args = append(args, uuid.New(), reviewedAgo)
	}
	if err := pool.QueryRow(context.Background(), q, args...).Scan(&id); err != nil {
		t.Fatalf("seed report: %v", err)
	}
	return id
}

type channelAuditRow struct {
	Actor  uuid.UUID
	Action string
	Reason string
}

func channelAuditRows(t *testing.T, pool *pgxpool.Pool, targetID uuid.UUID) []channelAuditRow {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT actor_id, action, reason FROM channel_admin_audit WHERE target_id = $1 ORDER BY created_at`, targetID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []channelAuditRow
	for rows.Next() {
		var a channelAuditRow
		if err := rows.Scan(&a.Actor, &a.Action, &a.Reason); err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

func TestChannelAdminIT_DecisionsAuditTheRealActor(t *testing.T) {
	rg, pool := setupChannelAdminIT(t)
	ctx := context.Background()
	channelID := seedChannel(t, pool, "active")
	upheld := seedChannelReport(t, pool, channelID, "open", "")
	dismissed := seedChannelReport(t, pool, channelID, "open", "")

	send := func(method, path, perm, body string) int {
		hdr := forgedEdge(uuid.New()) // forged X-User-Id must not become the actor
		hdr[ServiceAuthHeader] = "Bearer " + rg.mint(t, rg.admin, AudienceChat, []string{perm}, rg.actor.String())
		w := adminServe(rg.r, method, InternalAdminPrefix+path, body, hdr)
		return w.Code
	}

	cases := []struct {
		name, path, perm, body string
		target                 uuid.UUID
		action                 string
	}{
		{"uphold", "/channel-reports/" + upheld.String() + "/decision", PermReportsAct, `{"decision":"uphold","reason":"coordinated spam"}`, upheld, store.AuditReportUpheld},
		{"dismiss", "/channel-reports/" + dismissed.String() + "/decision", PermReportsAct, `{"decision":"dismiss","reason":"not a violation"}`, dismissed, store.AuditReportDismissed},
		{"suspend", "/channels/" + channelID.String() + "/suspend", PermChannelsModerate, `{"reason":"coordinated spam"}`, channelID, store.AuditChannelSuspended},
	}
	for _, tc := range cases {
		if code := send(http.MethodPost, tc.path, tc.perm, tc.body); code != http.StatusOK {
			t.Fatalf("%s: status=%d, want 200", tc.name, code)
		}
		got := channelAuditRows(t, pool, tc.target)
		if len(got) != 1 || got[0].Actor != rg.actor || got[0].Action != tc.action || got[0].Reason == "" {
			t.Fatalf("%s: audit rows=%+v, want one %s by %s with a reason", tc.name, got, tc.action, rg.actor)
		}
	}

	var reviewer uuid.UUID
	var status string
	if err := pool.QueryRow(ctx, `SELECT reviewer_id, status FROM channel_reports WHERE id = $1`, upheld).Scan(&reviewer, &status); err != nil {
		t.Fatal(err)
	}
	if reviewer != rg.actor || status != "reviewed" {
		t.Fatalf("upheld report reviewer=%s status=%s, want %s reviewed", reviewer, status, rg.actor)
	}

	// Refused writes change nothing and audit nothing: a second decision on
	// a decided report, suspending a suspended channel, a missing reason.
	for _, tc := range []struct {
		path, perm, body string
		want             int
	}{
		{"/channel-reports/" + upheld.String() + "/decision", PermReportsAct, `{"decision":"dismiss","reason":"second opinion"}`, http.StatusConflict},
		{"/channels/" + channelID.String() + "/suspend", PermChannelsModerate, `{"reason":"again"}`, http.StatusConflict},
		{"/channels/" + channelID.String() + "/unsuspend", PermChannelsModerate, `{"reason":""}`, http.StatusUnprocessableEntity},
		{"/channel-reports/" + uuid.NewString() + "/decision", PermReportsAct, `{"decision":"uphold","reason":"x"}`, http.StatusNotFound},
	} {
		if code := send(http.MethodPost, tc.path, tc.perm, tc.body); code != tc.want {
			t.Fatalf("%s: status=%d, want %d", tc.path, code, tc.want)
		}
	}
	if n := len(channelAuditRows(t, pool, upheld)); n != 1 {
		t.Fatalf("upheld report audit rows=%d after refused re-decision, want 1", n)
	}
	if n := len(channelAuditRows(t, pool, channelID)); n != 1 {
		t.Fatalf("channel audit rows=%d after refused writes, want 1", n)
	}

	if code := send(http.MethodPost, "/channels/"+channelID.String()+"/unsuspend", PermChannelsModerate, `{"reason":"appeal granted"}`); code != http.StatusOK {
		t.Fatalf("unsuspend: status=%d, want 200", code)
	}
	got := channelAuditRows(t, pool, channelID)
	if len(got) != 2 || got[1].Actor != rg.actor || got[1].Action != store.AuditChannelRestored {
		t.Fatalf("after unsuspend audit rows=%+v, want suspended then unsuspended by %s", got, rg.actor)
	}

	// The trail is append-only.
	if _, err := pool.Exec(ctx, `UPDATE channel_admin_audit SET reason = 'rewritten' WHERE target_id = $1`, channelID); err == nil {
		t.Fatal("channel_admin_audit accepted an UPDATE")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM channel_admin_audit WHERE target_id = $1`, channelID); err == nil {
		t.Fatal("channel_admin_audit accepted a DELETE")
	}

	// A refused token writes nothing.
	hdr := forgedEdge(uuid.New())
	hdr[ServiceAuthHeader] = "Bearer " + rg.mint(t, rg.admin, AudienceChat, []string{PermReportsRead}, rg.actor.String())
	other := seedChannelReport(t, pool, channelID, "open", "")
	if w := adminServe(rg.r, http.MethodPost, InternalAdminPrefix+"/channel-reports/"+other.String()+"/decision", adminBody, hdr); w.Code != http.StatusForbidden {
		t.Fatalf("read-only token deciding: status=%d, want 403", w.Code)
	}
	if n := len(channelAuditRows(t, pool, other)); n != 0 {
		t.Fatalf("refused decision wrote %d audit rows", n)
	}
}

func TestChannelAdminIT_ReadsAndStats(t *testing.T) {
	rg, pool := setupChannelAdminIT(t)
	active := seedChannel(t, pool, "active")
	seedChannel(t, pool, "suspended")
	seedChannel(t, pool, "suspended")
	seedChannel(t, pool, "archived")
	open1 := seedChannelReport(t, pool, active, "open", "")
	seedChannelReport(t, pool, active, "open", "")
	seedChannelReport(t, pool, active, "open", "")
	seedChannelReport(t, pool, active, "reviewed", "2 days")
	seedChannelReport(t, pool, active, "dismissed", "3 days")
	seedChannelReport(t, pool, active, "dismissed", "6 days")
	seedChannelReport(t, pool, active, "reviewed", "10 days") // outside the window

	get := func(path, perm string) map[string]json.RawMessage {
		w := adminServe(rg.r, http.MethodGet, InternalAdminPrefix+path, "", bearer(rg.mint(t, rg.admin, AudienceChat, []string{perm}, rg.actor.String())))
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: status=%d body=%s", path, w.Code, w.Body.String())
		}
		var env map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		return env
	}

	var stats store.AdminStats
	if err := json.Unmarshal(get("/stats", PermStatsRead)["data"], &stats); err != nil {
		t.Fatal(err)
	}
	if stats.OpenReports != 3 || stats.ReportsDecided7d != 3 || stats.ReportsUpheld7d != 1 ||
		stats.ReportsDismissed7d != 2 || stats.SuspendedChannels != 2 || stats.OldestOpenReportAt == nil {
		t.Fatalf("stats=%+v, want open 3, decided 3 (upheld 1, dismissed 2), suspended 2", stats)
	}

	var queue []store.ReportListItem
	if err := json.Unmarshal(get("/channel-reports", PermReportsRead)["data"], &queue); err != nil {
		t.Fatal(err)
	}
	if len(queue) != 3 {
		t.Fatalf("open queue len=%d, want 3", len(queue))
	}
	var one store.ReportListItem
	if err := json.Unmarshal(get("/channel-reports/"+open1.String(), PermReportsRead)["data"], &one); err != nil {
		t.Fatal(err)
	}
	if one.ID != open1 || one.ChannelID != active || one.ChannelHandle == "" {
		t.Fatalf("report detail=%+v, want id %s on channel %s", one, open1, active)
	}
}
