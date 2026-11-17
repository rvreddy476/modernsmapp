package model

import "github.com/google/uuid"

// WatchSessionKey is the primary key of one Scylla
// social_analytics.watch_sessions row: ((content_id), session_id). The
// account-erasure path reads a viewer's keys from PostgreSQL (where the
// viewer id is indexed) and deletes them in Scylla (where it is not).
type WatchSessionKey struct {
	ContentID uuid.UUID
	SessionID string
}
