-- Edit history (Q&A backend audit L3, 30 Oct 2026).
--
-- An edit used to overwrite the title and body with no record and
-- regenerate the slug, so old links 404'd. The slug is now fixed at
-- creation (store.UpdateQuestion), and every edit that changes something
-- keeps the replaced version here, written in the edit's transaction.
-- editor_id is who made the edit (today always the author). Erasure
-- (store.AnonymizeUser) deletes a user's rows here.
--
-- New tables only: no lock on any existing table beyond the FK reference
-- check (SHARE ROW EXCLUSIVE on questions/answers for the instant the
-- constraint is created on an empty table).
CREATE TABLE IF NOT EXISTS question_revisions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    question_id UUID NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    editor_id   UUID NOT NULL,
    title       TEXT NOT NULL,
    body        TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_question_revisions_question ON question_revisions (question_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_question_revisions_editor ON question_revisions (editor_id);

CREATE TABLE IF NOT EXISTS answer_revisions (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    answer_id  UUID NOT NULL REFERENCES answers(id) ON DELETE CASCADE,
    editor_id  UUID NOT NULL,
    body       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_answer_revisions_answer ON answer_revisions (answer_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_answer_revisions_editor ON answer_revisions (editor_id);
