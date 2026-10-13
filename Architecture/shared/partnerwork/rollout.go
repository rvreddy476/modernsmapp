package partnerwork

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
)

const EnabledVariable = "PARTNER_WORK_COORDINATION_ENABLED"

func Enabled(getenv func(string) string) bool {
	return strings.EqualFold(strings.TrimSpace(getenv(EnabledVariable)), "true")
}

// Bootstrap is deliberately opt-in. Moped is the sole migration owner; other
// domains fail startup if their DSN does not reach that authority. Deploy rider
// first, then Food/Home with the same app DB, before enabling Commerce dispatch.
func Bootstrap(ctx context.Context, db *pgxpool.Pool, c Category, getenv func(string) string) error {
	if !Enabled(getenv) {
		return nil
	}
	if c == Moped {
		if err := Migrate(ctx, db); err != nil {
			return fmt.Errorf("partner-work migration: %w", err)
		}
	}
	return InstallDomain(ctx, db, c)
}
