-- 005: platform suspension of a community (admin console, Chat).
--
-- communities.status has admitted 'suspended' since setup.sql and nothing
-- wrote it. admin-service now suspends and unsuspends through the token
-- family (POST /v1/communities/internal/admin/communities/{id}/suspend and
-- .../unsuspend, permission chat:communities.moderate). These columns say who
-- did it, when and why; status stays the one switch every read path and
-- search-service already obey.
--
-- While suspended: out of Discover and search (community.updated carries
-- status 'suspended'), no joins, no join-request approvals, no posts and no
-- other community writes; its members (and its owner) can still read it and
-- see the suspension on the card. Everyone else gets the missing answer.
--
-- Unsuspending clears all three columns. Each action appends one
-- community_admin_audit row (003) with the reason code and the note.
--
-- No foreign key on suspended_by: it is the admin's identity user id, which
-- lives in auth, not here. Idempotent.

ALTER TABLE communities ADD COLUMN IF NOT EXISTS suspended_at           TIMESTAMPTZ;
ALTER TABLE communities ADD COLUMN IF NOT EXISTS suspended_by           UUID;
ALTER TABLE communities ADD COLUMN IF NOT EXISTS suspension_reason_code TEXT;

-- The admin console's "suspended communities" list, newest first.
CREATE INDEX IF NOT EXISTS idx_communities_suspended
    ON communities(suspended_at DESC) WHERE status = 'suspended';
