// Package chatdb reads the per-conversation call mute from message-service's
// chat schema (chat_db). call-service owns call_db only; this is a narrow,
// read-only view configured by CHAT_POSTGRES_DSN.
package chatdb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// callsMutedSQL: does the CALLEE have mute_calls on for either the
// conversation the call was started from ($2, may be NULL) or the pair's
// direct conversation (chat.direct_conversation_keys, user_a < user_b)?
// Checking the pair's DM as well means a caller cannot bypass a DM mute by
// naming some other conversation as the call's source.
const callsMutedSQL = `
SELECT EXISTS (
  SELECT 1 FROM chat.conversation_settings cs
  WHERE cs.user_id = $1
    AND cs.mute_calls = true
    AND (cs.conversation_id = $2::uuid
         OR cs.conversation_id IN (
              SELECT k.conversation_id FROM chat.direct_conversation_keys k
              WHERE (k.user_a = $1 AND k.user_b = $3) OR (k.user_a = $3 AND k.user_b = $1)))
)`

// MuteReader answers "has this member muted calls here".
type MuteReader struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

// Open builds a small lazy pool (an unreachable chat_db at boot recovers
// without a restart).
func Open(ctx context.Context, dsn string, log *slog.Logger) (*MuteReader, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse chat dsn: %w", err)
	}
	cfg.MaxConns = 4
	cfg.MaxConnIdleTime = 5 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return New(pool, log), nil
}

func New(pool *pgxpool.Pool, log *slog.Logger) *MuteReader {
	if log == nil {
		log = slog.Default()
	}
	return &MuteReader{pool: pool, log: log}
}

func (m *MuteReader) Close() { m.pool.Close() }

// CallsMuted is defensive: ANY error — including the mute_calls column or
// a table not existing yet (SQLSTATE 42703 / 42P01, before message-service
// migration 011 is applied) — reports NOT muted, so a lookup problem can
// never silently swallow a call.
func (m *MuteReader) CallsMuted(ctx context.Context, calleeID, callerID uuid.UUID, sourceConversationID *uuid.UUID) bool {
	if m == nil || m.pool == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var source any
	if sourceConversationID != nil {
		source = *sourceConversationID
	}
	var muted bool
	if err := m.pool.QueryRow(ctx, callsMutedSQL, calleeID, source, callerID).Scan(&muted); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == "42703" || pgErr.Code == "42P01") {
			m.log.Warn("mute_calls not readable yet (schema); ringing", "code", pgErr.Code)
		} else {
			m.log.Warn("mute_calls lookup failed; ringing", "err", err, "callee", calleeID)
		}
		return false
	}
	return muted
}
