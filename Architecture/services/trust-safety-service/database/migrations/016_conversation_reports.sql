-- 016_conversation_reports.sql: a whole chat conversation becomes reportable
-- (chat-parity fix pass 2: Report in a chat's settings).
--
--   conversation  a group, addressed by the chat conversation UUID. It is
--                 stored like any UUID entity: entity_id = the conversation
--                 id, entity_key NULL, so the one-active-report-per-reporter
--                 index (008) applies per group. (A DM is reported as the
--                 other person, entity_type 'user', and needs nothing here.)
--
-- Evidence for a conversation report (service.ReportEvidence) adds
-- conversation_type and message_ids (≤20 message UUIDs, newest first) next
-- to conversation_id. The CHECKs below keep a conversation row's evidence
-- naming that same conversation and bound message_ids on every row.
--
-- Idempotent: every constraint is dropped and re-added. The entity-key
-- CHECK accepts exactly what 015 accepted (conversation rows already fell
-- under its keyless branch); it now names 'conversation' explicitly so the
-- rule reads in one place.

ALTER TABLE trust.reports DROP CONSTRAINT IF EXISTS reports_entity_key_check;
ALTER TABLE trust.reports ADD CONSTRAINT reports_entity_key_check CHECK (
    (entity_type = 'hashtag' AND entity_key IS NOT NULL
        AND char_length(entity_key) BETWEEN 1 AND 50
        AND left(entity_key, 1) <> '#')
    OR (entity_type = 'message' AND entity_key IS NOT NULL
        AND entity_key ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
        AND split_part(entity_key, ':', 2) = entity_id::text)
    OR (entity_type = 'conversation' AND entity_key IS NULL)
    OR (entity_type NOT IN ('hashtag', 'message', 'conversation') AND entity_key IS NULL)
);

-- A conversation report's evidence, when present, names its own
-- conversation (entity_id).
ALTER TABLE trust.reports DROP CONSTRAINT IF EXISTS reports_conversation_evidence_check;
ALTER TABLE trust.reports ADD CONSTRAINT reports_conversation_evidence_check CHECK (
    entity_type <> 'conversation'
    OR evidence IS NULL
    OR evidence->>'conversation_id' = entity_id::text
);

-- message_ids, when present, is an array of at most 20 entries. CASE, not
-- AND/OR: SQL does not promise evaluation order, and jsonb_array_length
-- raises on a non-array.
ALTER TABLE trust.reports DROP CONSTRAINT IF EXISTS reports_evidence_message_ids_check;
ALTER TABLE trust.reports ADD CONSTRAINT reports_evidence_message_ids_check CHECK (
    CASE
        WHEN evidence IS NULL OR jsonb_typeof(evidence) <> 'object' OR NOT (evidence ? 'message_ids') THEN true
        WHEN jsonb_typeof(evidence->'message_ids') <> 'array' THEN false
        ELSE jsonb_array_length(evidence->'message_ids') <= 20
    END
);
