package postgres

import (
	"context"

	"github.com/atpost/shared/identityroles"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	db *pgxpool.Pool
	// roleIntents is the durable queue that tells identity a user has become
	// or stopped being a `seller`. It lives on the Store rather than on the
	// Service because the seller lifecycle writes own their own transactions
	// here, and the whole point of the queue is to enqueue INSIDE that
	// transaction — see enqueueRoleIntent.
	//
	// nil is a supported state: a deployment without identity configured
	// keeps working and simply does not grant roles. See WithRoleIntents.
	roleIntents *identityroles.Outbox
}

func New(db *pgxpool.Pool) *Store { return &Store{db: db} }

// WithRoleIntents attaches the identity role queue. Fluent, matching the
// Service's WithX convention.
func (s *Store) WithRoleIntents(ob *identityroles.Outbox) *Store {
	s.roleIntents = ob
	return s
}

// enqueueRoleIntent records a role grant/revoke in the SAME transaction as the
// seller row it describes.
//
// # WHY THIS IS NOT AN HTTP CALL
//
// Calling identity here would put a second service's availability on the
// critical path of an approval, and a failed call would leave an approved
// seller who can never see the seller area with nothing recording why. The
// intent commits with the seller row instead — both or neither — and
// shared/identityroles' Worker delivers it within about a second, retrying
// through an identity outage. See the identityroles package doc.
//
// A nil queue is a no-op, not an error: identity being unconfigured must not
// break commerce's approval flow.
func (s *Store) enqueueRoleIntent(ctx context.Context, tx pgx.Tx, op identityroles.Op, userID uuid.UUID, reason string) error {
	if s.roleIntents == nil {
		return nil
	}
	return s.roleIntents.Enqueue(ctx, tx, identityroles.Intent{
		Op:     op,
		UserID: userID.String(),
		Role:   identityroles.RoleSeller,
		Reason: reason,
	})
}

// DB returns the underlying pool for callers that need to open a
// cross-domain transaction (e.g. AcceptRFQQuote spans orders +
// rfq_quotes + rfqs). Most code paths should use the helper methods
// on Store; reach for DB() sparingly.
func (s *Store) DB() *pgxpool.Pool { return s.db }
