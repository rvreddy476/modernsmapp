-- Migration 061: live recordings are not saveable offline by default
-- (founder decision, 2 Oct 2026).
--
-- A live recording becomes an unlisted long video (source = 'live',
-- service/live_vod.go). It was created with allow_download = TRUE, so a
-- viewer holding the link could save it offline without the creator ever
-- having said so. Recordings now start like an ordinary upload does in the
-- composer's default: viewers can't save them offline until the creator
-- switches it on (PATCH /v1/posts/:id, allow_download).
--
-- Two statements, both safe to run again:
--
--   A. Every existing recording is switched off. A recording whose creator
--      has already changed the switch themselves (an owner edit recorded in
--      post_edit_audit with an allow_download change) is left as they set
--      it: that is a choice, not the old default, and it is what keeps a
--      second run from undoing a creator who switched it on afterwards.
--
--   B. The offline copies viewers hold of a recording that does not allow
--      them are revoked with the reason the owner-edit path writes
--      ('not_allowed', store/postgres/post_edit.go). The creator's own
--      copies stay: the creator needs no permission from their own switch.
--      A row already revoked keeps its first revoked_at and reason.

UPDATE posts p
   SET allow_download = FALSE
 WHERE p.source = 'live'
   AND p.allow_download
   AND NOT EXISTS (
       SELECT 1 FROM post_edit_audit a
        WHERE a.post_id = p.id
          AND a.changes ? 'allow_download');

UPDATE post_offline_copies c
   SET revoked_at = NOW(), revoke_reason = 'not_allowed'
  FROM posts p
 WHERE p.id = c.post_id
   AND p.source = 'live'
   AND NOT p.allow_download
   AND c.revoked_at IS NULL
   AND c.user_id <> p.author_id;
