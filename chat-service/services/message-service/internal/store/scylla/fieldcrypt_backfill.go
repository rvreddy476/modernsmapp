package scylla

import (
	"context"
	"encoding/hex"
	"errors"
	"time"

	"github.com/atpost/chat-message-service/internal/contentcrypt"
	"github.com/atpost/chat-message-service/internal/fieldbackfill"
	"github.com/atpost/chat-shared/fieldcrypt"
	"github.com/gocql/gocql"
)

// Backfill of legacy plaintext in Scylla (field encryption at rest, 22 Oct
// 2026). Both tables are walked with driver paging (the resume point is the
// hex page state). Every rewrite is a lightweight transaction conditioned on
// the values read: CQL UPDATE is an upsert, so an unconditional write would
// resurrect an inbox row a disappearing message deleted in between, or put
// the old text back over an unsend or an edit.

// BackfillTargets lists the Scylla tables the backfill walks.
func (s *MessageStore) BackfillTargets() []fieldbackfill.Target {
	return []fieldbackfill.Target{scyllaMessagesTarget{s}, scyllaInboxTarget{s}}
}

func decodePageState(resume string) ([]byte, error) {
	if resume == "" {
		return nil, nil
	}
	b, err := hex.DecodeString(resume)
	if err != nil {
		return nil, errors.New("scylla backfill: resume point is not a page state")
	}
	return b, nil
}

type scyllaMessagesTarget struct{ s *MessageStore }

func (scyllaMessagesTarget) Name() string { return "scylla.messages" }

func (t scyllaMessagesTarget) Step(ctx context.Context, c *fieldcrypt.Cipher, resumeAfter string, limit int) (fieldbackfill.StepResult, error) {
	state, err := decodePageState(resumeAfter)
	if err != nil {
		return fieldbackfill.StepResult{}, err
	}
	iter := t.s.session.Query(`
		SELECT conversation_id, bucket, ts, msg_id, text, reply_to_preview, extra FROM messages
	`).WithContext(ctx).PageSize(limit).PageState(state).Iter()
	next := iter.PageState()
	type row struct {
		conv, msg          gocql.UUID
		bucket             string
		ts                 time.Time
		text, reply, extra *string
	}
	var batch []row
	var r row
	for iter.Scan(&r.conv, &r.bucket, &r.ts, &r.msg, &r.text, &r.reply, &r.extra) {
		batch = append(batch, r)
		r = row{}
	}
	if err := iter.Close(); err != nil {
		return fieldbackfill.StepResult{}, err
	}
	res := fieldbackfill.StepResult{Next: hex.EncodeToString(next), Done: len(next) == 0, Scanned: len(batch)}
	for _, r := range batch {
		binding := contentcrypt.MessageRow(uuidFromGocql(r.conv), uuidFromGocql(r.msg))
		sealed, changed, err := contentcrypt.Reseal(ctx, c, []contentcrypt.Field{
			{Scope: contentcrypt.ScopeMessageText, Row: binding, Value: r.text},
			{Scope: contentcrypt.ScopeMessageReplyPreview, Row: binding, Value: r.reply},
			{Scope: contentcrypt.ScopeMessageExtra, Row: binding, Value: r.extra},
		})
		if err != nil {
			return res, err
		}
		if !changed {
			continue
		}
		applied, err := t.s.session.Query(`
			UPDATE messages SET text = ?, reply_to_preview = ?, extra = ?
			WHERE conversation_id = ? AND bucket = ? AND ts = ? AND msg_id = ?
			IF text = ? AND reply_to_preview = ? AND extra = ?
		`, sealed[0], sealed[1], sealed[2], r.conv, r.bucket, r.ts, r.msg, r.text, r.reply, r.extra).
			WithContext(ctx).MapScanCAS(map[string]interface{}{})
		if err != nil {
			return res, err
		}
		if applied {
			res.Sealed++
		}
	}
	return res, nil
}

type scyllaInboxTarget struct{ s *MessageStore }

func (scyllaInboxTarget) Name() string { return "scylla.conversations_by_user" }

func (t scyllaInboxTarget) Step(ctx context.Context, c *fieldcrypt.Cipher, resumeAfter string, limit int) (fieldbackfill.StepResult, error) {
	state, err := decodePageState(resumeAfter)
	if err != nil {
		return fieldbackfill.StepResult{}, err
	}
	iter := t.s.session.Query(`
		SELECT user_id, bucket, last_ts, conversation_id, last_text FROM conversations_by_user
	`).WithContext(ctx).PageSize(limit).PageState(state).Iter()
	next := iter.PageState()
	type row struct {
		user, conv gocql.UUID
		bucket     string
		ts         time.Time
		text       *string
	}
	var batch []row
	var r row
	for iter.Scan(&r.user, &r.bucket, &r.ts, &r.conv, &r.text) {
		batch = append(batch, r)
		r = row{}
	}
	if err := iter.Close(); err != nil {
		return fieldbackfill.StepResult{}, err
	}
	res := fieldbackfill.StepResult{Next: hex.EncodeToString(next), Done: len(next) == 0, Scanned: len(batch)}
	for _, r := range batch {
		binding := contentcrypt.InboxRow(uuidFromGocql(r.user), uuidFromGocql(r.conv), r.ts)
		sealed, changed, err := contentcrypt.Reseal(ctx, c, []contentcrypt.Field{
			{Scope: contentcrypt.ScopeInboxLastText, Row: binding, Value: r.text},
		})
		if err != nil {
			return res, err
		}
		if !changed {
			continue
		}
		applied, err := t.s.session.Query(`
			UPDATE conversations_by_user SET last_text = ?
			WHERE user_id = ? AND bucket = ? AND last_ts = ? AND conversation_id = ?
			IF last_text = ?
		`, sealed[0], r.user, r.bucket, r.ts, r.conv, r.text).
			WithContext(ctx).MapScanCAS(map[string]interface{}{})
		if err != nil {
			return res, err
		}
		if applied {
			res.Sealed++
		}
	}
	return res, nil
}
