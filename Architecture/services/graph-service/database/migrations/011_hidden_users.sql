-- Account control (auth-service 30-day deletion flow).
--
-- user.deactivated / user.deletion_scheduled mean HIDE, never erase: the
-- account may come back (user.reactivated / user.deletion_cancelled). While a
-- row exists here, permission checks answer as if the pair were blocked and
-- follower/following lists omit the user. The purge path deletes the row
-- along with everything else keyed by the user.
CREATE TABLE IF NOT EXISTS hidden_users (
    user_id   UUID PRIMARY KEY,
    reason    TEXT        NOT NULL DEFAULT '',
    hidden_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
