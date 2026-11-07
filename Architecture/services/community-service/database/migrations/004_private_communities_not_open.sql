-- 004: a private community is never open to join (founder, 2026-09-12:
-- communities are private by default and invite-only).
--
-- join_mode defaulted to "open" whatever the type, so a private community
-- created without naming a join mode could be joined directly by anyone who
-- held its id. The join route now reads a private "open" as invite-only
-- (service/access.go, effectiveJoinMode) and the create and save paths no
-- longer store one; this brings the rows in line, so the community card says
-- what the join route does.
--
-- The public set is store.PublicCommunityTypes; a test holds this list to it.
-- Idempotent: a second run matches no row.
UPDATE communities
   SET join_mode = 'invite_only', updated_at = NOW()
 WHERE join_mode = 'open'
   AND community_type NOT IN ('public', 'education', 'professional');
