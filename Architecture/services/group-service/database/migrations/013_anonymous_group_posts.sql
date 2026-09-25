-- Anonymous posting in groups.
--
-- The ROW keeps the real author_id. Bans, rate limits, ownership ("can I
-- delete my own post") and a platform-admin reveal all need it, and a scheme
-- that discards the author cannot do any of them. Only the WIRE is masked, in
-- store.GroupPostV2.MarshalJSON, so a route added later is masked for free
-- rather than remembering to mask itself.

ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS is_anonymous BOOLEAN NOT NULL DEFAULT FALSE;

-- The pseudonym the wire carries instead of author_id.
--
-- PER POST, never per user. A stable per-user alias would let anyone who sees
-- two anonymous posts know they came from the same person, and in a group of
-- a dozen that is usually enough to name them.
ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS anon_alias UUID;

-- An anonymous post must have an alias, and a named post must not.
--
-- This is the load-bearing line. It is the database refusing the exact
-- failure this codebase has produced twice in a week: a field accepted at one
-- layer and dropped at another. If is_anonymous is written and anon_alias is
-- not, the marshaller would have no alias to substitute and would fall back
-- to the real author id. Postgres refuses the row instead.
ALTER TABLE group_posts DROP CONSTRAINT IF EXISTS group_posts_anon_alias_check;
ALTER TABLE group_posts ADD CONSTRAINT group_posts_anon_alias_check
    CHECK ((is_anonymous = FALSE AND anon_alias IS NULL)
        OR (is_anonymous = TRUE  AND anon_alias IS NOT NULL));

-- Anonymity is opt-in per group and off by default. A group that never asked
-- for it must not acquire it because the feature shipped.
ALTER TABLE groups ADD COLUMN IF NOT EXISTS allow_anonymous_posts BOOLEAN NOT NULL DEFAULT FALSE;

-- An author replying under their own name to their own anonymous post
-- de-anonymises themselves in one click. Their comments on it are masked with
-- the POST's alias, server-side, and they are not offered the choice.
-- There is no user-elected anonymous commenting in this release.
ALTER TABLE group_post_comments ADD COLUMN IF NOT EXISTS is_anonymous BOOLEAN NOT NULL DEFAULT FALSE;

-- Reveals are audited. group_admin_audit (012) is append-only by trigger;
-- this index makes "who has been revealed, by whom, and why" answerable
-- without scanning the whole table.
CREATE INDEX IF NOT EXISTS idx_group_admin_audit_reveal
    ON group_admin_audit(created_at DESC)
    WHERE action = 'group_post.author_revealed';
