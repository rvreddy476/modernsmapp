-- Migration 004 (account-control purge, internal/purge): hidden_authors is
-- the GLOBAL per-author suppression set consulted by every feed surface's
-- hide filter (internal/service/feed.go, applyHiddenAuthorFilter) —
-- populated on user.deactivated / user.deletion_scheduled and cleared on
-- user.reactivated / user.deletion_cancelled. Row also removed by PurgeUser
-- on user.purge_requested. See Architecture/shared/events/events.go,
-- "Account control", and internal/store/postgres/purge.go.
CREATE TABLE IF NOT EXISTS hidden_authors (
    author_id UUID PRIMARY KEY,
    reason    TEXT,
    hidden_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
