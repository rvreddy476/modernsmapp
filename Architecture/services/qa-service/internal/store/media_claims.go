package store

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"

	"github.com/atpost/qa-service/internal/claimsync"
	"github.com/atpost/qa-service/internal/mediaclient"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Media claim support (internal/mediaclaims, internal/claimsync).
//
// The holders: a question's question_media rows (ref = question id) and an
// answer's answer_media rows (ref = answer id). A question is dead once
// deleted; an answer once it, or its question, is deleted. Neither has a
// restore path.

// LockMediaHolder serialises every write that changes which media one row
// holds, for as long as the caller holds it, so the reconcile tool never acts
// on a set a writer is changing. Transaction-scoped, so a crash or a lost
// connection frees it. unlock is idempotent.
func (s *Store) LockMediaHolder(ctx context.Context, ref uuid.UUID) (unlock func(), err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("lock media holder: %w", err)
	}
	if _, err := tx.Exec(ctx, lockMediaHolderSQL, mediaHolderLockKey(ref)); err != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("lock media holder: %w", err)
	}
	done := false
	return func() {
		if !done {
			done = true
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}, nil
}

// lockMediaHolderSQL waits (never try_) for an exclusive, transaction-scoped
// lock. Pinned by TestMediaHolderLockWaitsAndIsTransactionScoped; proven live
// by TestMediaClaimsIT_HolderLockSerialises (CI).
const lockMediaHolderSQL = `SELECT pg_advisory_xact_lock($1)`

// mediaHolderLockKey namespaces the advisory lock so it cannot collide with
// another lock taken on the same id for a different purpose.
func mediaHolderLockKey(ref uuid.UUID) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("qa-media-holder:"))
	_, _ = h.Write(ref[:])
	return int64(h.Sum64())
}

const (
	questionHolderSQL = `
		SELECT q.id, array_agg(m.media_id ORDER BY m.sort_order, m.id), q.deleted_at IS NULL
		FROM questions q JOIN question_media m ON m.question_id = q.id`
	answerHolderSQL = `
		SELECT a.id, array_agg(m.media_id ORDER BY m.sort_order, m.id), (a.deleted_at IS NULL AND q.deleted_at IS NULL)
		FROM answers a JOIN answer_media m ON m.answer_id = a.id JOIN questions q ON q.id = a.question_id`
)

// QuestionMediaClaimRows pages through every question with media, deleted
// ones included, in id order after `after`.
func (s *Store) QuestionMediaClaimRows(ctx context.Context, after uuid.UUID, limit int) ([]claimsync.Row, uuid.UUID, error) {
	return s.holderPage(ctx, mediaclient.KindQuestionMedia,
		questionHolderSQL+` WHERE q.id > $1 GROUP BY q.id ORDER BY q.id LIMIT $2`, after, limit)
}

// AnswerMediaClaimRows pages through every answer with media, deleted ones
// included, in id order after `after`.
func (s *Store) AnswerMediaClaimRows(ctx context.Context, after uuid.UUID, limit int) ([]claimsync.Row, uuid.UUID, error) {
	return s.holderPage(ctx, mediaclient.KindAnswerMedia,
		answerHolderSQL+` WHERE a.id > $1 GROUP BY a.id, q.deleted_at ORDER BY a.id LIMIT $2`, after, limit)
}

func (s *Store) holderPage(ctx context.Context, kind, sql string, after uuid.UUID, limit int) ([]claimsync.Row, uuid.UUID, error) {
	rows, err := s.db.Query(ctx, sql, after, limit)
	if err != nil {
		return nil, after, err
	}
	defer rows.Close()
	var out []claimsync.Row
	last := after
	for rows.Next() {
		r := claimsync.Row{Kind: kind}
		if err := rows.Scan(&r.Ref, &r.MediaIDs, &r.Live); err != nil {
			return nil, after, err
		}
		last = r.Ref
		out = append(out, r)
	}
	return out, last, rows.Err()
}

// MediaClaimRowFor re-reads one holder row, for the reconcile tool, under
// LockMediaHolder. A row that no longer exists, or holds no media, is not
// live and holds nothing.
func (s *Store) MediaClaimRowFor(ctx context.Context, kind string, ref uuid.UUID) (claimsync.Row, error) {
	var sql string
	switch kind {
	case mediaclient.KindQuestionMedia:
		sql = questionHolderSQL + ` WHERE q.id = $1 GROUP BY q.id`
	case mediaclient.KindAnswerMedia:
		sql = answerHolderSQL + ` WHERE a.id = $1 GROUP BY a.id, q.deleted_at`
	default:
		return claimsync.Row{Kind: kind, Ref: ref}, fmt.Errorf("unknown media holder kind %q", kind)
	}
	row := claimsync.Row{Kind: kind}
	err := s.db.QueryRow(ctx, sql, ref).Scan(&row.Ref, &row.MediaIDs, &row.Live)
	if errors.Is(err, pgx.ErrNoRows) {
		return claimsync.Row{Kind: kind, Ref: ref}, nil
	}
	return row, err
}
