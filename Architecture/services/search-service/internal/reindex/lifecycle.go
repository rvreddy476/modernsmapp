package reindex

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Account lifecycle for the users reindex (2026-11-15).
//
// profile-service, which the users reindex walks, does not know whether an
// account is deactivated or scheduled for deletion. On a live index that
// did not matter — the keep-hidden rewrite (store/search/bulk.go) keeps a
// stored is_hidden — but the reindex exists for the index that is gone (a
// wiped volume, events lost beyond retention; AutoHealUsersOnStartup runs
// on an EMPTY index), and there it wrote every deactivated account back
// into search as visible.
//
// The authority is auth.users.account_status on the identity database; the
// lifecycle events (user.deactivated / deletion_scheduled / reactivated,
// purge.Handler) keep it current between runs. The backfill already read
// it from there (cmd/backfill/visibility.go); the server's reindex now
// does too, through HiddenAccounts.

// HiddenAccountStatuses are the auth-service states search hides, in step
// with the lifecycle events (purge.Handler): deactivated and
// pending_deletion are hidden, and purged too in case an erased account's
// row is still listed anywhere.
var HiddenAccountStatuses = []string{"deactivated", "pending_deletion", "purged"}

// hiddenAccountsSQL reads the ids of accounts in a hidden state.
const hiddenAccountsSQL = `SELECT user_id::text FROM auth.users WHERE account_status = ANY($1)`

// HiddenAccounts says which accounts search must hide. An error means the
// set is unknown, never empty.
type HiddenAccounts interface {
	HiddenUserIDs(ctx context.Context) (map[string]bool, error)
}

// RowQuerier is the part of a pgx pool or connection the lookup uses.
type RowQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// LoadHiddenAccounts reads the hidden set from the identity database.
func LoadHiddenAccounts(ctx context.Context, q RowQuerier) (map[string]bool, error) {
	rows, err := q.Query(ctx, hiddenAccountsSQL, HiddenAccountStatuses)
	if err != nil {
		return nil, fmt.Errorf("read auth.users.account_status: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan hidden account: %w", err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read auth.users.account_status: %w", err)
	}
	return out, nil
}

// PostgresHiddenAccounts is HiddenAccounts over the identity database.
type PostgresHiddenAccounts struct{ q RowQuerier }

// NewPostgresHiddenAccounts wraps a pool (IDENTITY_POSTGRES_DSN).
func NewPostgresHiddenAccounts(q RowQuerier) *PostgresHiddenAccounts {
	return &PostgresHiddenAccounts{q: q}
}

// HiddenUserIDs implements HiddenAccounts.
func (p *PostgresHiddenAccounts) HiddenUserIDs(ctx context.Context) (map[string]bool, error) {
	return LoadHiddenAccounts(ctx, p.q)
}
