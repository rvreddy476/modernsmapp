-- 015_message_reports.sql: chat messages become reportable (chat-parity
-- CONTRACT §2.6 / §8).
--
--   message  addressed on the wire as "<conversation_id>:<message_id>".
--            entity_id holds the message id (a UUID already, so the
--            one-active-report-per-reporter index (008) applies per
--            message) and entity_key the canonical "<conversation>:<message>"
--            pair, which the CHECK keeps consistent with entity_id.
--
-- evidence is an optional snapshot of what was reported (text ≤2000 runes,
-- media id, sender, type) with a server-set "source" naming who captured
-- it: "reporter" (public route, user-supplied), "message-service"
-- (internal route) or "chat.MessageRequestReported" (consumer). Messages
-- can be edited or unsent after a report; the snapshot is what the
-- moderator reviews. Bounded so a row stays small.
--
-- source_event_id makes event-driven and service-filed reports idempotent:
-- a redelivered event or a retried internal call finds its row instead of
-- filing a second one, even after the first was resolved (which the
-- active-report index alone would allow).

ALTER TABLE trust.reports ADD COLUMN IF NOT EXISTS evidence JSONB;
ALTER TABLE trust.reports ADD COLUMN IF NOT EXISTS source_event_id TEXT;

ALTER TABLE trust.reports DROP CONSTRAINT IF EXISTS reports_entity_key_check;
ALTER TABLE trust.reports ADD CONSTRAINT reports_entity_key_check CHECK (
    (entity_type = 'hashtag' AND entity_key IS NOT NULL
        AND char_length(entity_key) BETWEEN 1 AND 50
        AND left(entity_key, 1) <> '#')
    OR (entity_type = 'message' AND entity_key IS NOT NULL
        AND entity_key ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
        AND split_part(entity_key, ':', 2) = entity_id::text)
    OR (entity_type NOT IN ('hashtag', 'message') AND entity_key IS NULL)
);

ALTER TABLE trust.reports DROP CONSTRAINT IF EXISTS reports_evidence_check;
ALTER TABLE trust.reports ADD CONSTRAINT reports_evidence_check CHECK (
    evidence IS NULL
    OR (jsonb_typeof(evidence) = 'object' AND octet_length(evidence::text) <= 16384)
);

ALTER TABLE trust.reports DROP CONSTRAINT IF EXISTS reports_source_event_id_check;
ALTER TABLE trust.reports ADD CONSTRAINT reports_source_event_id_check CHECK (
    source_event_id IS NULL OR char_length(source_event_id) BETWEEN 1 AND 200
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_reports_source_event
    ON trust.reports (source_event_id)
    WHERE source_event_id IS NOT NULL;
