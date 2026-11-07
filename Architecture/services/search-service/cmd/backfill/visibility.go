package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/atpost/search-service/internal/privacyclient"
	"github.com/atpost/search-service/internal/store/search"
)

// Author visibility for the rebuild (audit 2026-11-07, S-M1).
//
// The posts and users rebuilds replace whole documents, so they must carry
// the two account flags or they reset them: is_hidden (deactivated or
// scheduled for deletion) and is_private / author_is_private. The rebuild
// used to set neither, so one run brought every deactivated account and
// every private author's posts back into public search.
//
// Both come from their authorities: the lifecycle state from
// auth.users.account_status on the identity database (the same database the
// users rebuild already reads profile.profiles from), and privacy from the
// identity user-service the live consumer asks. A row whose flags cannot
// be resolved is skipped, never written as public.

// hiddenAccountStatuses are the auth-service states search hides, in step
// with the lifecycle events (purge.Handler): deactivated and
// pending_deletion are hidden, and purged is hidden too in case an erased
// account's row is still listed anywhere.
var hiddenAccountStatuses = []string{"deactivated", "pending_deletion", "purged"}

// visibilitySource resolves an author's flags for the rebuild.
type visibilitySource struct {
	hidden  map[string]bool
	privacy privacyclient.Lookup
}

// resolve returns the author's visibility, or an error when privacy cannot
// be read (the caller skips the row).
func (v *visibilitySource) resolve(ctx context.Context, userID string) (*search.AuthorVisibility, error) {
	if v == nil || v.privacy == nil {
		return nil, fmt.Errorf("no visibility source")
	}
	private, err := v.privacy.IsPrivate(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &search.AuthorVisibility{IsPrivate: private, IsHidden: v.hidden[userID]}, nil
}

// newVisibilitySource loads the hidden set and wires the privacy lookup.
// Either missing is an error: a rebuild that cannot tell is refused rather
// than run.
func newVisibilitySource(ctx context.Context, identityDSN string) (*visibilitySource, error) {
	baseURL := os.Getenv("IDENTITY_USER_SERVICE_URL")
	if baseURL == "" {
		baseURL = os.Getenv("USER_SERVICE_URL")
	}
	privacy := privacyclient.New(baseURL, os.Getenv("INTERNAL_SERVICE_KEY"))
	if !privacy.Configured() {
		return nil, fmt.Errorf("IDENTITY_USER_SERVICE_URL is not set: the rebuild cannot tell private accounts from public ones")
	}
	hidden, err := loadHiddenAccounts(ctx, identityDSN)
	if err != nil {
		return nil, err
	}
	return &visibilitySource{hidden: hidden, privacy: privacy}, nil
}

// loadHiddenAccounts reads the ids of accounts in a hidden lifecycle state.
func loadHiddenAccounts(ctx context.Context, identityDSN string) (map[string]bool, error) {
	pool, err := connect(ctx, identityDSN, "IDENTITY_POSTGRES_DSN (account lifecycle)")
	if err != nil {
		return nil, fmt.Errorf("the rebuild cannot tell deactivated accounts from active ones: %w", err)
	}
	defer pool.Close()
	rows, err := pool.Query(ctx,
		`SELECT user_id::text FROM auth.users WHERE account_status = ANY($1)`, hiddenAccountStatuses)
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
	slog.Info("backfill: hidden accounts loaded from auth.users", "count", len(out))
	return out, nil
}
