package scylla

import (
	"context"
	"fmt"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

// ── Account control: purge (Scylla side) ────────────────────────────────────
//
// Scylla has no cross-partition transactions, so every statement here is
// idempotent and the caller runs this pass BEFORE the Postgres transaction:
// a crash mid-way is repaired by the redelivery re-running the whole purge.

// monthBuckets lists YYYYMM buckets from `from` (inclusive) to now.
func monthBuckets(from time.Time) []string {
	now := time.Now().UTC()
	if from.IsZero() || from.After(now) {
		from = now
	}
	t := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	var out []string
	for !t.After(end) {
		out = append(out, t.Format("200601"))
		t = t.AddDate(0, 1, 0)
	}
	return out
}

// RedactUserMessages blanks every message the user authored in the
// conversation (text='', media_id=null, is_deleted=true), keeping the row so
// other members' timelines stay consistent, and removes the user's
// reactions. Scans each month bucket from `since`.
func (s *MessageStore) RedactUserMessages(ctx context.Context, convID, userID uuid.UUID, since time.Time) error {
	cid := gocql.UUID(convID)
	uid := gocql.UUID(userID)
	for _, bucket := range monthBuckets(since) {
		iter := s.session.Query(`
			SELECT ts, msg_id, sender_id FROM messages WHERE conversation_id = ? AND bucket = ?`,
			cid, bucket).WithContext(ctx).Iter()
		var ts time.Time
		var msgID, sender gocql.UUID
		type key struct {
			ts time.Time
			id gocql.UUID
		}
		var mine []key
		for iter.Scan(&ts, &msgID, &sender) {
			if sender == uid {
				mine = append(mine, key{ts, msgID})
			}
		}
		if err := iter.Close(); err != nil {
			return fmt.Errorf("scan messages %s/%s: %w", convID, bucket, err)
		}
		for _, k := range mine {
			if err := s.session.Query(`
				UPDATE messages SET text = '', media_id = null, is_deleted = true
				WHERE conversation_id = ? AND bucket = ? AND ts = ? AND msg_id = ?`,
				cid, bucket, k.ts, k.id).WithContext(ctx).Exec(); err != nil {
				return fmt.Errorf("redact message %s: %w", k.id, err)
			}
		}

		// Reactions by the user in this partition.
		riter := s.session.Query(`
			SELECT msg_ts, msg_id, emoji, user_id FROM message_reactions WHERE conversation_id = ? AND bucket = ?`,
			cid, bucket).WithContext(ctx).Iter()
		type rkey struct {
			ts    time.Time
			id    gocql.UUID
			emoji string
		}
		var mineR []rkey
		var emoji string
		var ruser gocql.UUID
		for riter.Scan(&ts, &msgID, &emoji, &ruser) {
			if ruser == uid {
				mineR = append(mineR, rkey{ts, msgID, emoji})
			}
		}
		if err := riter.Close(); err != nil {
			return fmt.Errorf("scan reactions %s/%s: %w", convID, bucket, err)
		}
		for _, k := range mineR {
			if err := s.session.Query(`
				DELETE FROM message_reactions
				WHERE conversation_id = ? AND bucket = ? AND msg_ts = ? AND msg_id = ? AND emoji = ? AND user_id = ?`,
				cid, bucket, k.ts, k.id, k.emoji, uid).WithContext(ctx).Exec(); err != nil {
				return fmt.Errorf("delete reaction: %w", err)
			}
		}
	}
	return nil
}

// DeleteUserInbox drops every conversations_by_user partition for the user
// from `since` to now (one DELETE per month bucket).
func (s *MessageStore) DeleteUserInbox(ctx context.Context, userID uuid.UUID, since time.Time) error {
	uid := gocql.UUID(userID)
	for _, bucket := range monthBuckets(since) {
		if err := s.session.Query(`DELETE FROM conversations_by_user WHERE user_id = ? AND bucket = ?`,
			uid, bucket).WithContext(ctx).Exec(); err != nil {
			return fmt.Errorf("delete inbox %s: %w", bucket, err)
		}
	}
	return nil
}
