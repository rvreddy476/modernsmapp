package partnerwork

import (
	"context"
	"embed"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed domains/*.sql
var domains embed.FS

// InstallDomain installs transactional guards and backfills existing accepted
// work. Only rider owns Migrate; other domains must share that database. Missing
// authority is an error, never permission to create independent coordinator state.
func InstallDomain(ctx context.Context, db *pgxpool.Pool, c Category) error {
	if c != Moped && c != FoodDelivery && c != HomeServices {
		return ErrInvalid
	}
	var authority bool
	if err := db.QueryRow(ctx, `SELECT singleton FROM partner_work.authority WHERE singleton`).Scan(&authority); err != nil {
		return fmt.Errorf("partner-work authority missing: configure the shared app database and migrate rider first: %w", err)
	}
	b, err := domains.ReadFile("domains/" + string(c) + ".sql")
	if err != nil {
		return err
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('partner-work-domain-install'))`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, string(b)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
