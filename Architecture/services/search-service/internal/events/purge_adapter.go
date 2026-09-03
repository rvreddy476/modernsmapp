package events

import (
	"context"
	"fmt"

	"github.com/atpost/search-service/internal/store/postgres"
	"github.com/atpost/search-service/internal/store/search"
	"github.com/google/uuid"
)

// searchEraser is the subset of *search.Store PurgeStore needs, narrowed to
// an interface so unit tests can substitute a fake instead of a live
// OpenSearch cluster.
type searchEraser interface {
	EraseAuthorContent(ctx context.Context, authorID string) error
	DeleteUser(ctx context.Context, userID string) error
	UpdateUserHidden(ctx context.Context, userID string, hidden bool) error
	UpdatePostsAuthorHidden(ctx context.Context, authorID string, hidden bool) error
}

// pgEraser is satisfied by both *postgres.AnalyticsStore and
// *postgres.SearchExtrasStore.
type pgEraser interface {
	PurgeUser(ctx context.Context, userID uuid.UUID) error
}

// PurgeStore composes the OpenSearch store with the optional Postgres
// analytics/extras stores into the purge.Eraser + purge.Hider search-service
// needs for the auth-service account-lifecycle contract (see
// Architecture/shared/events/events.go, "Account control").
//
// OpenSearch is mandatory (the service refuses to start without it).
// analytics and extras are optional — both are nil unless POSTGRES_DSN is
// set — and PurgeUser/SetUserHidden skip them silently when absent, exactly
// like every other optional-store guard in this service.
type PurgeStore struct {
	search    searchEraser
	analytics pgEraser
	extras    pgEraser
}

// NewPurgeStore builds the composite eraser/hider. analytics and extras may
// be nil (POSTGRES_DSN unset).
//
// The nil checks below matter: assigning a nil *postgres.AnalyticsStore
// directly to an interface-typed field would produce a non-nil interface
// wrapping a nil pointer, and PurgeUser's `p.analytics != nil` guard would
// then wrongly evaluate true and call through to a nil receiver.
func NewPurgeStore(s *search.Store, analytics *postgres.AnalyticsStore, extras *postgres.SearchExtrasStore) *PurgeStore {
	p := &PurgeStore{search: s}
	if analytics != nil {
		p.analytics = analytics
	}
	if extras != nil {
		p.extras = extras
	}
	return p
}

// PurgeUser erases every row/doc keyed by userID: the author's posts
// (behind the permanent revision fence — see authorfence.go), the user
// document, and — when wired — the user's saved searches, search history,
// and analytics rows. Idempotent: a second call finds nothing left to
// erase in any of these stores and returns nil, which is required because
// auth-service re-emits user.purge_requested every 24h until it sees our
// ack.
func (p *PurgeStore) PurgeUser(ctx context.Context, userID uuid.UUID) error {
	id := userID.String()

	// M2-P0-7: erase behind a permanent fence rather than delete-by-query
	// — see authorfence.go for why a hard delete is unsafe here.
	if err := p.search.EraseAuthorContent(ctx, id); err != nil {
		return fmt.Errorf("purge: erase author content %s: %w", id, err)
	}
	if err := p.search.DeleteUser(ctx, id); err != nil {
		return fmt.Errorf("purge: delete user doc %s: %w", id, err)
	}
	if p.extras != nil {
		if err := p.extras.PurgeUser(ctx, userID); err != nil {
			return fmt.Errorf("purge: extras (saved searches / history) %s: %w", id, err)
		}
	}
	if p.analytics != nil {
		if err := p.analytics.PurgeUser(ctx, userID); err != nil {
			return fmt.Errorf("purge: analytics (queries / clicks) %s: %w", id, err)
		}
	}
	return nil
}

// SetUserHidden implements unconditional hide/unhide (deactivate,
// scheduled-deletion, reactivate, deletion-cancelled): the user document
// and every post by them get is_hidden flipped, which the query-time
// must_not (blockfilter.go) then keeps out of every search surface for
// every viewer.
func (p *PurgeStore) SetUserHidden(ctx context.Context, userID uuid.UUID, hidden bool, reason string) error {
	id := userID.String()
	if err := p.search.UpdateUserHidden(ctx, id, hidden); err != nil {
		return fmt.Errorf("purge: set user hidden=%v %s (%s): %w", hidden, id, reason, err)
	}
	if err := p.search.UpdatePostsAuthorHidden(ctx, id, hidden); err != nil {
		return fmt.Errorf("purge: set posts author hidden=%v %s (%s): %w", hidden, id, reason, err)
	}
	return nil
}
