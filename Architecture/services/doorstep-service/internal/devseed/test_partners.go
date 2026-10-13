package devseed

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/atpost/doorstep-service/internal/runtimeenv"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestPartnerDatabase is deliberately a separate, loopback-only database.
// Never put fictional approvals, documents or payout accounts in the live pool.
const TestPartnerDatabase = "doorstep_partner_demo_test"

func RequirePartnerSandbox(dsn string, getenv func(string) string) error {
	if !runtimeenv.IsDevelopment(getenv) || getenv("DOORSTEP_TEST_FIXTURES") != "true" {
		return errors.New("test partners require explicit development mode and DOORSTEP_TEST_FIXTURES=true")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return errors.New("invalid sandbox database connection")
	}
	if cfg.ConnConfig.Database != TestPartnerDatabase || (cfg.ConnConfig.Host != "127.0.0.1" && cfg.ConnConfig.Host != "localhost" && cfg.ConnConfig.Host != "::1") {
		return errors.New("test partners require the dedicated loopback doorstep_partner_demo_test database")
	}
	if len(cfg.ConnConfig.Fallbacks) > 0 {
		return errors.New("sandbox connections cannot have fallback hosts")
	}
	return nil
}

// SeedTestPartners adds exactly two clearly labelled fictional AC profiles.
// Both cover the four AC catalogue services. No real identity/bank documents,
// mobile numbers, authentication accounts, bookings or payments are fabricated.
func SeedTestPartners(ctx context.Context, db *pgxpool.Pool, getenv func(string) string) error {
	if err := RequirePartnerSandbox(db.Config().ConnString(), getenv); err != nil {
		return err
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var actual string
	if err = tx.QueryRow(ctx, `SELECT current_database()`).Scan(&actual); err != nil {
		return err
	}
	if actual != TestPartnerDatabase {
		return errors.New("unexpected sandbox database")
	}
	var outsiders int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM doorstep.professionals WHERE id NOT IN ($1,$2)`, ID("test-pro", "arun"), ID("test-pro", "meera")).Scan(&outsiders); err != nil {
		return err
	}
	if outsiders > 0 {
		return errors.New("refusing to mix test partners with an existing partner dataset")
	}
	exec := func(sql string, args ...any) {
		if err != nil {
			return
		}
		_, err = tx.Exec(ctx, sql, args...)
	}
	actor := ID("test-admin", "sandbox")
	exec(`INSERT INTO doorstep.admin_audit_log(actor_user_id,permission,action,entity,entity_id,details) VALUES($1,'doorstep:pros.approve','sandbox.fixture','test-only','fictional-ac-partners','{"simulated":true,"real_verification":false}')`, actor)
	zone := ID("test-zone", "peerzadiguda")
	exec(`INSERT INTO doorstep.zones(id,city_code,name,slug,boundary,travel_buffer_minutes,active)
 VALUES($1,'HYD','TEST · Peerzadiguda','test-peerzadiguda',ST_Multi(ST_Buffer(ST_SetSRID(ST_MakePoint(78.6061,17.3975),4326)::geography,5000)::geometry)::geography,30,true) ON CONFLICT DO NOTHING`, zone)
	for _, name := range []string{"arun", "meera"} {
		pro, user := ID("test-pro", name), ID("test-user", name)
		display := "TEST · " + strings.ToUpper(name[:1]) + name[1:] + " · Fictional AC partner"
		exec(`INSERT INTO doorstep.professionals(id,user_id,status,display_name,city_code,home_point,service_radius_m,approved_at,approved_by,status_reason,area_label)
  VALUES($1,$2,'approved',$3,'HYD',ST_SetSRID(ST_MakePoint(78.6061,17.3975),4326)::geography,10000,now(),$4,'SIMULATED APPROVAL — TEST DATA ONLY','TEST · Peerzadiguda') ON CONFLICT DO NOTHING`, pro, user, display, actor)
		exec(`INSERT INTO doorstep.pro_skills(pro_id,skill_code,status,verified_at,verified_by) VALUES($1,'ac_service','verified',now(),$2) ON CONFLICT DO NOTHING`, pro, actor)
		exec(`INSERT INTO doorstep.pro_zones(pro_id,zone_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, pro, zone)
		for day := 0; day < 7; day++ {
			exec(`INSERT INTO doorstep.pro_weekly_hours(id,pro_id,weekday,start_time,end_time) VALUES($1,$2,$3,'08:00','20:00') ON CONFLICT DO NOTHING`, ID("test-hours", fmt.Sprintf("%s-%d", name, day)), pro, day)
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
