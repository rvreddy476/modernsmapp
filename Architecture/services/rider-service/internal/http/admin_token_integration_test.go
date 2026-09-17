// DB-backed admin-service token tests (admin console Wave 2 — Mopedu): an
// admitted admin write and an admin read of partner data each leave one
// rider_admin_audit_logs row whose actor is the token's signed act claim,
// and the stats route counts what is seeded. Requires TEST_PG_DSN on a
// "_test" database (rider_it_test); skipped when unset.
package http

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/rider-service/database"
	"github.com/atpost/rider-service/internal/service"
	"github.com/atpost/rider-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupAdminTokenIT(t *testing.T) (*adminTokenRig, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping rider-service admin token integration tests")
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
	if err := database.BootstrapSchema(ctx, pool); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	rg := newAdminTokenRig(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	// The real store is the audit sink: rows land in rider_admin_audit_logs.
	New(service.New(store.New(pool), nil, service.Config{}), adminTestInternalKey).
		WithServiceAuth(rg.v).RegisterRoutes(r)
	rg.r = r
	return rg, pool
}

func itExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("seed %q: %v", sql, err)
	}
}

func itScanUUID(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&id); err != nil {
		t.Fatalf("seed %q: %v", sql, err)
	}
	return id
}

func seedITPartner(t *testing.T, pool *pgxpool.Pool, status string) uuid.UUID {
	t.Helper()
	return itScanUUID(t, pool, `
		INSERT INTO rider_partners (user_id, partner_type, full_name, phone, status)
		VALUES ($1, 'individual_driver', 'IT Partner', $2, $3::rider_partner_status)
		RETURNING id`, uuid.New(), "+91"+uuid.NewString()[:10], status)
}

// seedITRide inserts a ride in status with the given creation time and,
// for a cancelled status, a cancellation time of now.
func seedITRide(t *testing.T, pool *pgxpool.Pool, status string, createdAt time.Time) uuid.UUID {
	t.Helper()
	return itScanUUID(t, pool, `
		INSERT INTO rider_rides (customer_user_id, vehicle_type, status, pickup_address, pickup_location,
			drop_address, drop_location, created_at, requested_at, cancelled_at)
		VALUES ($1, 'bike', $2::rider_ride_status, 'IT pickup', ST_SetSRID(ST_MakePoint(77.59, 12.97), 4326)::geography,
			'IT drop', ST_SetSRID(ST_MakePoint(77.60, 12.98), 4326)::geography, $3, $3,
			CASE WHEN $2::text LIKE 'cancelled_%' THEN now() ELSE NULL END)
		RETURNING id`, uuid.New(), status, createdAt)
}

type auditRow struct {
	Actor  uuid.UUID
	Action string
	Path   string
	Status int
}

func auditRowsFor(t *testing.T, pool *pgxpool.Pool, entityID uuid.UUID) []auditRow {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT admin_user_id, action, COALESCE(request_path, ''), COALESCE(response_status, 0)
		FROM rider_admin_audit_logs WHERE entity_id = $1 ORDER BY created_at`, entityID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var a auditRow
		if err := rows.Scan(&a.Actor, &a.Action, &a.Path, &a.Status); err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

// tokenCall sends one admin-service token request with a forged gateway
// identity riding along (no key: this family has none).
func (rg *adminTokenRig) tokenCall(t *testing.T, perm, method, path, body string) (int, []byte) {
	t.Helper()
	hdr := legacyAdmin(uuid.New(), "superadmin")
	delete(hdr, "X-Internal-Service-Key")
	hdr["X-Admin-Id"] = uuid.NewString()
	hdr[ServiceAuthHeader] = "Bearer " + rg.mint(t, rg.admin, AudienceRider, []string{perm}, rg.actor.String())
	w := adminServe(rg.r, method, InternalAdminPrefix+path, body, hdr)
	return w.Code, w.Body.Bytes()
}

func TestAdminTokenIT_WriteAndPartnerReadRecordAct(t *testing.T) {
	rg, pool := setupAdminTokenIT(t)
	ctx := context.Background()
	partnerID := seedITPartner(t, pool, "pending_verification")

	if code, body := rg.tokenCall(t, PermPartnersApprove, http.MethodPost, "/partners/"+partnerID.String()+"/approve", ""); code != http.StatusOK {
		t.Fatalf("approve: status=%d body=%s", code, body)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status::text FROM rider_partners WHERE id = $1`, partnerID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "approved" {
		t.Fatalf("partner status=%q, want approved", status)
	}

	if code, body := rg.tokenCall(t, PermPartnersRead, http.MethodGet, "/partners/"+partnerID.String(), ""); code != http.StatusOK {
		t.Fatalf("partner read: status=%d body=%s", code, body)
	}

	rows := auditRowsFor(t, pool, partnerID)
	if len(rows) != 2 {
		t.Fatalf("audit rows=%+v, want approve then read", rows)
	}
	for i, want := range []string{"partner.approve", "partner.read"} {
		if rows[i].Action != want {
			t.Fatalf("row %d action=%q, want %q", i, rows[i].Action, want)
		}
		if rows[i].Actor != rg.actor {
			t.Fatalf("row %d actor=%s, want act %s", i, rows[i].Actor, rg.actor)
		}
		if !strings.HasPrefix(rows[i].Path, InternalAdminPrefix) || rows[i].Status != http.StatusOK {
			t.Fatalf("row %d path=%q status=%d, want %s… 200", i, rows[i].Path, rows[i].Status, InternalAdminPrefix)
		}
	}
}

func TestAdminTokenIT_StatsCountSeededData(t *testing.T) {
	rg, pool := setupAdminTokenIT(t)
	read := func() store.AdminStats {
		t.Helper()
		code, body := rg.tokenCall(t, PermStatsRead, http.MethodGet, "/stats", "")
		if code != http.StatusOK {
			t.Fatalf("stats: status=%d body=%s", code, body)
		}
		// api.JSON wraps the payload: {"data": {...}}.
		var out struct {
			Data store.AdminStats `json:"data"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("stats body %s: %v", body, err)
		}
		return out.Data
	}
	before := read()

	pending := seedITPartner(t, pool, "pending_verification")
	approved := seedITPartner(t, pool, "approved")
	itExec(t, pool, `INSERT INTO rider_partner_documents (partner_id, document_type, file_url, status)
		VALUES ($1, 'driving_license', 'https://it/dl', 'pending')`, pending)
	itExec(t, pool, `INSERT INTO rider_vehicles (partner_id, vehicle_type, registration_number, status)
		VALUES ($1, 'bike', $2, 'pending')`, approved, "IT"+strings.ToUpper(uuid.NewString()[:8]))
	planID := itScanUUID(t, pool, `SELECT id FROM rider_subscription_plans ORDER BY created_at LIMIT 1`)
	itExec(t, pool, `INSERT INTO rider_subscription_payments (partner_id, plan_id, amount, payment_method, status)
		VALUES ($1, $2, 499.00, 'upi', 'submitted')`, approved, planID)
	itExec(t, pool, `INSERT INTO rider_subscription_payments (partner_id, plan_id, amount, payment_method, status, verified_at)
		VALUES ($1, $2, 1234.56, 'upi', 'verified', now())`, approved, planID)
	live := seedITRide(t, pool, "in_progress", time.Now())
	cancelled := seedITRide(t, pool, "cancelled_by_customer", time.Now())
	seedITRide(t, pool, "completed", time.Now().Add(-3*24*time.Hour))
	seedITRide(t, pool, "completed", time.Now().Add(-30*24*time.Hour))
	itExec(t, pool, `INSERT INTO rider_complaints (ride_id, customer_id, category, status) VALUES ($1, $2, 'it', 'open')`, cancelled, uuid.New())
	itExec(t, pool, `INSERT INTO rider_safety_incidents (ride_id, kind, status) VALUES ($1, 'sos', 'acknowledged')`, live)

	after := read()
	deltas := []struct {
		name          string
		before, after int
		want          int
	}{
		{"partners_pending_review", before.PartnersPendingReview, after.PartnersPendingReview, 1},
		{"documents_pending", before.DocumentsPending, after.DocumentsPending, 1},
		{"vehicles_pending", before.VehiclesPending, after.VehiclesPending, 1},
		{"payments_awaiting_verification", before.PaymentsAwaitingVerification, after.PaymentsAwaitingVerification, 1},
		{"rides_today", before.RidesToday, after.RidesToday, 2},
		{"rides_last_7_days", before.RidesLast7Days, after.RidesLast7Days, 3},
		{"live_rides_now", before.LiveRidesNow, after.LiveRidesNow, 1},
		{"cancellations_today", before.CancellationsToday, after.CancellationsToday, 1},
		{"open_complaints", before.OpenComplaints, after.OpenComplaints, 1},
		{"open_safety_incidents", before.OpenSafetyIncidents, after.OpenSafetyIncidents, 1},
	}
	for _, d := range deltas {
		if got := d.after - d.before; got != d.want {
			t.Errorf("%s: delta=%d (before %d, after %d), want %d", d.name, got, d.before, d.after, d.want)
		}
	}
	if got := after.RevenueTodayPaise - before.RevenueTodayPaise; got != 123456 {
		t.Errorf("revenue_today_paise: delta=%d, want 123456", got)
	}
	if after.DayStartsAt.IsZero() || after.GeneratedAt.Before(after.DayStartsAt) {
		t.Errorf("bounds: day_starts_at=%s generated_at=%s", after.DayStartsAt, after.GeneratedAt)
	}
}
