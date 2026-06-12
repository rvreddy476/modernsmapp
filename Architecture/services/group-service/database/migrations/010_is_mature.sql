-- 010_is_mature.sql: Mature (18+) flag on spaces — set at creation,
-- shown as an 18+ chip on the space header. Enforcement (age-gating
-- viewers) is a follow-up; this stores the owner's declaration.
ALTER TABLE groups ADD COLUMN IF NOT EXISTS is_mature BOOLEAN NOT NULL DEFAULT FALSE;
