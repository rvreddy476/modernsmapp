-- Durable, authoritative idempotency for comment creation.
--
-- Redis alone cannot make this exactly-once. The comment is COMMITTED in
-- PostgreSQL before the middleware records its result, so a crash — or a
-- failed Redis write — between those two points leaves no record that the
-- insert happened, and the client's retry with the same key inserts a second
-- comment. The Redis claim stays as a fast concurrency gate; this table is the
-- authority.
--
-- The key is semantic, not transport: the same actor, the same post and the
-- same client key are one intent. `fingerprint` binds that intent to its exact
-- payload, so a retry carrying edited text is rejected rather than replayed.
CREATE TABLE IF NOT EXISTS comment_idempotency (
    actor_id    UUID        NOT NULL,
    post_id     UUID        NOT NULL,
    client_key  TEXT        NOT NULL,
    fingerprint TEXT        NOT NULL,
    comment_id  UUID        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (actor_id, post_id, client_key)
);

-- Retention sweep support; records are only useful while a client may retry.
CREATE INDEX IF NOT EXISTS idx_comment_idempotency_age
    ON comment_idempotency (created_at);
