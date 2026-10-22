package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/chat-message-service/internal/contentcrypt"
	"github.com/atpost/chat-message-service/internal/fieldbackfill"
	"github.com/atpost/chat-shared/fieldcrypt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Backfill of legacy plaintext in the chat Postgres tables (field
// encryption at rest, 22 Oct 2026). Each table is walked by its key; every
// rewrite is compare-and-set on the values read, so a row a live request
// changed in between (an edit, an unsend, a redaction) is left to that
// request and never overwritten with stale content.

type pgBackfillField struct {
	scope fieldcrypt.Scope
	json  bool
}

type pgBackfillTable struct {
	name string
	// start is the cursor before the first row.
	start string
	// selectSQL: $1 cursor, $2 limit; returns the cursor key as text, then
	// nBind binding columns as text, then one text column per field.
	selectSQL string
	nBind     int
	fields    []pgBackfillField
	row       func(bind []string) ([]byte, error)
	// updateSQL: $1 the row's cursor key, then the new values, then the
	// values read (compare-and-set).
	updateSQL string
}

func uuidBinding(bind []string, build func(ids ...uuid.UUID) []byte) ([]byte, error) {
	ids := make([]uuid.UUID, len(bind))
	for i, b := range bind {
		id, err := uuid.Parse(b)
		if err != nil {
			return nil, fmt.Errorf("binding column %d is not a uuid", i+1)
		}
		ids[i] = id
	}
	return build(ids...), nil
}

var pgBackfillTables = []pgBackfillTable{
	{
		name:  "pg.conversations.last_message_preview",
		start: uuid.Nil.String(),
		selectSQL: `SELECT id::text, id::text, last_message_preview FROM chat.conversations
			WHERE id > $1::uuid ORDER BY id LIMIT $2`,
		nBind:  1,
		fields: []pgBackfillField{{contentcrypt.ScopeConversationPreview, false}},
		row: func(b []string) ([]byte, error) {
			return uuidBinding(b, func(ids ...uuid.UUID) []byte { return contentcrypt.ConversationRow(ids[0]) })
		},
		updateSQL: `UPDATE chat.conversations SET last_message_preview = $2
			WHERE id = $1::uuid AND last_message_preview IS NOT DISTINCT FROM $3`,
	},
	{
		name:  "pg.message_delivery_intents",
		start: uuid.Nil.String(),
		selectSQL: `SELECT message_id::text, conversation_id::text, message_id::text,
			       message_text, reply_to_preview, extra::text
			FROM chat.message_delivery_intents
			WHERE message_id > $1::uuid ORDER BY message_id LIMIT $2`,
		nBind: 2,
		fields: []pgBackfillField{
			{contentcrypt.ScopeIntentText, false},
			{contentcrypt.ScopeIntentReplyPreview, false},
			{contentcrypt.ScopeIntentExtra, true},
		},
		row: func(b []string) ([]byte, error) {
			return uuidBinding(b, func(ids ...uuid.UUID) []byte { return contentcrypt.MessageRow(ids[0], ids[1]) })
		},
		updateSQL: `UPDATE chat.message_delivery_intents
			SET message_text = $2, reply_to_preview = $3, extra = $4::text::jsonb
			WHERE message_id = $1::uuid
			  AND message_text IS NOT DISTINCT FROM $5
			  AND reply_to_preview IS NOT DISTINCT FROM $6
			  AND extra::text IS NOT DISTINCT FROM $7::text`,
	},
	{
		name:  "pg.idempotency_keys.response",
		start: "",
		selectSQL: `SELECT key, key, response::text FROM chat.idempotency_keys
			WHERE key > $1 ORDER BY key LIMIT $2`,
		nBind:  1,
		fields: []pgBackfillField{{contentcrypt.ScopeIdempotencyResponse, true}},
		row:    func(b []string) ([]byte, error) { return contentcrypt.IdempotencyRow(b[0]), nil },
		updateSQL: `UPDATE chat.idempotency_keys SET response = $2::text::jsonb
			WHERE key = $1 AND response::text IS NOT DISTINCT FROM $3::text`,
	},
	{
		// Legacy rows without a dedupe key get "outbox:<id>" in the same
		// write, because the sealed payload is bound to the key.
		name:  "pg.outbox_events.payload",
		start: "0",
		selectSQL: `SELECT id::text, event_type, COALESCE(dedupe_key, 'outbox:' || id::text), payload::text
			FROM chat.outbox_events WHERE id > $1::bigint ORDER BY id LIMIT $2`,
		nBind:  2,
		fields: []pgBackfillField{{contentcrypt.ScopeOutboxPayload, true}},
		row:    func(b []string) ([]byte, error) { return contentcrypt.OutboxRow(b[0], b[1]), nil },
		updateSQL: `UPDATE chat.outbox_events
			SET payload = $2::text::jsonb, dedupe_key = COALESCE(dedupe_key, 'outbox:' || id::text)
			WHERE id = $1::bigint AND payload::text IS NOT DISTINCT FROM $3::text`,
	},
	{
		name:  "pg.message_requests.preview",
		start: "0",
		selectSQL: `SELECT id::text, conversation_id::text, preview FROM chat.message_requests
			WHERE id > $1::bigint ORDER BY id LIMIT $2`,
		nBind:  1,
		fields: []pgBackfillField{{contentcrypt.ScopeRequestPreview, false}},
		row: func(b []string) ([]byte, error) {
			return uuidBinding(b, func(ids ...uuid.UUID) []byte { return contentcrypt.ConversationRow(ids[0]) })
		},
		updateSQL: `UPDATE chat.message_requests SET preview = $2
			WHERE id = $1::bigint AND preview IS NOT DISTINCT FROM $3`,
	},
	{
		name:  "pg.starred_messages.message_preview",
		start: uuid.Nil.String(),
		selectSQL: `SELECT id::text, user_id::text, message_id::text, message_preview FROM chat.starred_messages
			WHERE id > $1::uuid ORDER BY id LIMIT $2`,
		nBind:  2,
		fields: []pgBackfillField{{contentcrypt.ScopeStarredPreview, false}},
		row: func(b []string) ([]byte, error) {
			return uuidBinding(b, func(ids ...uuid.UUID) []byte { return contentcrypt.StarredRow(ids[0], ids[1]) })
		},
		updateSQL: `UPDATE chat.starred_messages SET message_preview = $2
			WHERE id = $1::uuid AND message_preview IS NOT DISTINCT FROM $3`,
	},
	{
		name:  "pg.scheduled_messages.content",
		start: uuid.Nil.String(),
		selectSQL: `SELECT id::text, id::text, content FROM chat.scheduled_messages
			WHERE id > $1::uuid ORDER BY id LIMIT $2`,
		nBind:  1,
		fields: []pgBackfillField{{contentcrypt.ScopeScheduledContent, false}},
		row: func(b []string) ([]byte, error) {
			return uuidBinding(b, func(ids ...uuid.UUID) []byte { return contentcrypt.IDRow(ids[0]) })
		},
		updateSQL: `UPDATE chat.scheduled_messages SET content = $2
			WHERE id = $1::uuid AND content IS NOT DISTINCT FROM $3`,
	},
	{
		name:  "pg.notes.text",
		start: uuid.Nil.String(),
		selectSQL: `SELECT id::text, id::text, user_id::text, text FROM chat.notes
			WHERE id > $1::uuid ORDER BY id LIMIT $2`,
		nBind:  2,
		fields: []pgBackfillField{{contentcrypt.ScopeNoteText, false}},
		row: func(b []string) ([]byte, error) {
			return uuidBinding(b, func(ids ...uuid.UUID) []byte { return contentcrypt.NoteRow(ids[0], ids[1]) })
		},
		updateSQL: `UPDATE chat.notes SET text = $2 WHERE id = $1::uuid AND text IS NOT DISTINCT FROM $3`,
	},
	{
		// Composite key: "<message uuid>|<target_lang>" (a uuid is 36
		// characters, so the language starts at position 38).
		name:  "pg.message_translations.translated_text",
		start: uuid.Nil.String() + "|",
		selectSQL: `SELECT message_id::text || '|' || target_lang, message_id::text, target_lang, translated_text
			FROM chat.message_translations
			WHERE (message_id, target_lang) > (split_part($1, '|', 1)::uuid, substr($1, 38))
			ORDER BY message_id, target_lang LIMIT $2`,
		nBind:  2,
		fields: []pgBackfillField{{contentcrypt.ScopeTranslationText, false}},
		row: func(b []string) ([]byte, error) {
			id, err := uuid.Parse(b[0])
			if err != nil {
				return nil, errors.New("translation message id is not a uuid")
			}
			return contentcrypt.TranslationRow(id, b[1]), nil
		},
		updateSQL: `UPDATE chat.message_translations SET translated_text = $2
			WHERE message_id = split_part($1, '|', 1)::uuid AND target_lang = substr($1, 38)
			  AND translated_text IS NOT DISTINCT FROM $3`,
	},
	{
		name:  "pg.message_threads.last_reply_preview",
		start: uuid.Nil.String(),
		selectSQL: `SELECT id::text, id::text, last_reply_preview FROM chat.message_threads
			WHERE id > $1::uuid ORDER BY id LIMIT $2`,
		nBind:  1,
		fields: []pgBackfillField{{contentcrypt.ScopeThreadPreview, false}},
		row: func(b []string) ([]byte, error) {
			return uuidBinding(b, func(ids ...uuid.UUID) []byte { return contentcrypt.IDRow(ids[0]) })
		},
		updateSQL: `UPDATE chat.message_threads SET last_reply_preview = $2
			WHERE id = $1::uuid AND last_reply_preview IS NOT DISTINCT FROM $3`,
	},
}

// pgBackfillTarget adapts one table to fieldbackfill.Target.
type pgBackfillTarget struct {
	db pgDB
	t  pgBackfillTable
}

// BackfillTargets lists the Postgres tables the backfill walks.
func (s *ConversationStore) BackfillTargets() []fieldbackfill.Target {
	out := make([]fieldbackfill.Target, 0, len(pgBackfillTables))
	for _, t := range pgBackfillTables {
		out = append(out, pgBackfillTarget{db: s.db, t: t})
	}
	return out
}

func (p pgBackfillTarget) Name() string { return p.t.name }

func (p pgBackfillTarget) Step(ctx context.Context, c *fieldcrypt.Cipher, resumeAfter string, limit int) (fieldbackfill.StepResult, error) {
	if resumeAfter == "" {
		resumeAfter = p.t.start
	}
	type rowRead struct {
		key    string
		bind   []string
		values []*string
	}
	rows, err := p.db.Query(ctx, p.t.selectSQL, resumeAfter, limit)
	if err != nil {
		return fieldbackfill.StepResult{}, err
	}
	var batch []rowRead
	for rows.Next() {
		r := rowRead{bind: make([]string, p.t.nBind), values: make([]*string, len(p.t.fields))}
		dest := make([]any, 0, 1+p.t.nBind+len(p.t.fields))
		dest = append(dest, &r.key)
		for i := range r.bind {
			dest = append(dest, &r.bind[i])
		}
		for i := range r.values {
			dest = append(dest, &r.values[i])
		}
		if err := rows.Scan(dest...); err != nil {
			rows.Close()
			return fieldbackfill.StepResult{}, err
		}
		batch = append(batch, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fieldbackfill.StepResult{}, err
	}

	res := fieldbackfill.StepResult{Next: resumeAfter, Scanned: len(batch), Done: len(batch) < limit}
	for _, r := range batch {
		binding, err := p.t.row(r.bind)
		if err != nil {
			return res, err
		}
		fields := make([]contentcrypt.Field, len(p.t.fields))
		for i, f := range p.t.fields {
			fields[i] = contentcrypt.Field{Scope: f.scope, Row: binding, Value: r.values[i], JSON: f.json}
		}
		sealed, changed, err := contentcrypt.Reseal(ctx, c, fields)
		if err != nil {
			return res, err
		}
		if changed {
			args := make([]any, 0, 1+2*len(fields))
			args = append(args, r.key)
			for _, v := range sealed {
				args = append(args, v)
			}
			for _, v := range r.values {
				args = append(args, v)
			}
			tag, err := p.db.Exec(ctx, p.t.updateSQL, args...)
			if err != nil {
				return res, err
			}
			if tag.RowsAffected() > 0 {
				res.Sealed++
			}
		}
		res.Next = r.key
	}
	return res, nil
}

// LoadProgress implements fieldbackfill.Progress.
func (s *ConversationStore) LoadProgress(ctx context.Context, target string) (string, bool, error) {
	var resume string
	var done bool
	err := s.db.QueryRow(ctx, `SELECT resume_after, done FROM chat.field_backfill_progress WHERE target = $1`, target).Scan(&resume, &done)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return resume, done, err
}

// SaveProgress implements fieldbackfill.Progress.
func (s *ConversationStore) SaveProgress(ctx context.Context, target, resumeAfter string, done bool, scanned, sealed int) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO chat.field_backfill_progress (target, resume_after, done, scanned, sealed, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (target) DO UPDATE SET
			resume_after = EXCLUDED.resume_after, done = EXCLUDED.done,
			scanned = chat.field_backfill_progress.scanned + EXCLUDED.scanned,
			sealed = chat.field_backfill_progress.sealed + EXCLUDED.sealed,
			updated_at = NOW()
	`, target, resumeAfter, done, scanned, sealed)
	return err
}

// ResetProgress clears every resume point (a -restart run).
func (s *ConversationStore) ResetProgress(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `DELETE FROM chat.field_backfill_progress`)
	return err
}
