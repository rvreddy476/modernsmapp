-- Durable, authoritative idempotency for POST /v1/posts (Slice C, C-LB-3).
--
-- WHY REDIS ALONE CANNOT DO THIS
--
-- The existing `Idempotency` middleware is a Redis response cache. It was never
-- attached to create-post, and attaching it would not be enough:
--
--   * it caps the recorded body at 8 KiB, a limit sized for comments;
--   * it writes its completed-response record AFTER the PostgreSQL post and
--     outbox transaction has committed. A crash, an eviction, or a failed Redis
--     write in that gap leaves no record that the post was created, and the
--     client's retry creates a SECOND post.
--
-- A composer retry is not rare. "Server committed, response lost" is the normal
-- outcome of a phone changing cells mid-publish, and the user sees a failure
-- and presses Post again. Duplicated posts are the most visible possible defect
-- in a social product.
--
-- This table is therefore the authority, and it is written in the SAME
-- transaction as the post row and the PostCreated outbox event. Redis remains
-- useful as a fast concurrency gate; it is no longer the source of truth.
--
-- SHAPE
--
-- The key is semantic, not transport. One actor plus one client-generated key
-- is one creation intent, regardless of how many times it is retried or which
-- connection carries it. `post_id` is the answer that intent produced.
--
-- Deliberately NOT scoped by any post field: unlike comments — which are keyed
-- (actor, post, client_key) because a comment is always about a known post — a
-- create has no parent, so the actor and the client key are the whole identity.
--
-- `fingerprint` binds the intent to its exact payload so a retry carrying
-- edited text is REJECTED (409) rather than silently replaying the older post
-- and making the user think their edit was published.
--
-- Scoping by actor_id also means two different users may independently pick the
-- same client UUID without colliding.
CREATE TABLE IF NOT EXISTS post_create_idempotency (
    actor_id    UUID        NOT NULL,
    client_key  TEXT        NOT NULL,
    fingerprint TEXT        NOT NULL,
    post_id     UUID        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (actor_id, client_key)
);

-- Retention sweep support; a record is only useful while a client may still
-- retry. Nothing deletes these yet — that sweep is a later operational task,
-- and keeping them is the safe direction.
CREATE INDEX IF NOT EXISTS idx_post_create_idempotency_age
    ON post_create_idempotency (created_at);
