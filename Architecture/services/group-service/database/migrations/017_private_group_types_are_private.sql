-- 017: groups whose type promises privacy get the privacy that keeps it.
--
-- A "private" or "hidden" group_type never set privacy_level before G-L1
-- (2026-11-07), so rows created before then kept privacy_level public (or
-- restricted). listableGroupSQL already keeps them out of every listing by
-- their type, but the access gate (service/access.go, groupIsPrivate) and the
-- media access check (store/media_access.go) read privacy_level and
-- visibility only: such a group's page, posts and media still answered a
-- stranger who held its id.
--
-- This sets them exactly as CreateGroup now creates a group of that type
-- (privacyForGroupType): privacy_level private, the legacy visibility private,
-- and join_mode invite_only, the only join mode a private group admits
-- (ValidatePrivacyJoinCombo). Pending join requests are left as they are;
-- the group's admins can still approve or reject them.
--
-- Idempotent: a second run matches no row. Embedded and run on boot by the
-- migration runner (internal/store/bootstrap.go), once per database.
UPDATE groups
   SET privacy_level = 'private',
       visibility    = 'private',
       join_mode     = 'invite_only',
       updated_at    = NOW()
 WHERE group_type IN ('private', 'hidden')
   AND (privacy_level IS DISTINCT FROM 'private'
        OR visibility IS DISTINCT FROM 'private'
        OR join_mode IS DISTINCT FROM 'invite_only');
