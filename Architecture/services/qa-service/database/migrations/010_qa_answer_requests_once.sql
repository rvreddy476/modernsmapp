-- One answer request per (question, requester, target) (Q&A backend audit
-- M4, 30 Oct 2026). Every request is a push to its target; without this a
-- requester could loop the route and push the same person hundreds of times.
-- store.CreateAnswerRequest inserts ON CONFLICT DO NOTHING against this
-- index and answers a repeat with 409.
--
-- Earlier duplicates are folded into the oldest row of each triple first.
-- answer_requests is small (one row per explicit ask), so the plain index
-- build's SHARE lock is brief; lock_timeout bounds the wait for it.
SET LOCAL lock_timeout = '5s';

DELETE FROM answer_requests dup
USING answer_requests keep
WHERE dup.question_id = keep.question_id
  AND dup.requester_id = keep.requester_id
  AND dup.requested_user_id = keep.requested_user_id
  AND (dup.created_at, dup.id) > (keep.created_at, keep.id);

CREATE UNIQUE INDEX IF NOT EXISTS uq_answer_requests_once
    ON answer_requests (question_id, requester_id, requested_user_id);
