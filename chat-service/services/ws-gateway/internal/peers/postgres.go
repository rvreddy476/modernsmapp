// Package peers lists a user's direct-message peers for presence fan-out.
//
// It is a READ-ONLY view of message-service's chat schema (chat_db): the
// gateway owns no chat data. Configure with CHAT_POSTGRES_DSN; when it is
// unset the gateway runs without presence fan-out (nothing is disclosed).
package peers

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// dmPeersSQL selects the other member of every ACCEPTED direct conversation
// the user still belongs to, most recently active first. Pending message
// requests are excluded: a stranger whose request was never accepted gets
// no presence pushes. A peer who HID the DM (hidden chats, message-service
// migration 018: conversation_members.locked) gets none either — nothing
// about a hidden chat reaches that person's personal channel.
//
// Each peer appears ONCE: a pair can hold more than one direct conversation
// (an older DM kept alongside a newer one, e.g. after a block severed the
// first), and one row per conversation made every presence transition reach
// that peer once per shared conversation. A peer ranks by its most recent
// shared conversation, and LIMIT counts distinct peers.
const dmPeersSQL = `
SELECT other.user_id
FROM chat.conversation_members me
JOIN chat.conversations c ON c.id = me.conversation_id
JOIN chat.conversation_members other
  ON other.conversation_id = me.conversation_id AND other.user_id <> me.user_id
WHERE me.user_id = $1
  AND c.type = 'direct'
  AND c.is_request = false
  AND me.left_at IS NULL
  AND other.left_at IS NULL
  AND NOT other.locked
GROUP BY other.user_id
ORDER BY MAX(c.updated_at) DESC
LIMIT $2`

// Postgres implements PeerSource over chat_db.
type Postgres struct {
	pool *pgxpool.Pool
}

// Open builds a small read-only pool. Connections are made lazily, so an
// unreachable chat_db at boot recovers without a restart; use Ping to log it.
func Open(ctx context.Context, dsn string) (*Postgres, error) {
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
	return &Postgres{pool: pool}, nil
}

// Ping checks chat_db reachability (startup diagnostics only).
func (p *Postgres) Ping(ctx context.Context) error {
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return p.pool.Ping(pingCtx)
}

func New(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

func (p *Postgres) Close() { p.pool.Close() }

func (p *Postgres) DMPeers(ctx context.Context, userID uuid.UUID, limit int) ([]uuid.UUID, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := p.pool.Query(ctx, dmPeersSQL, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("dm peers: %w", err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
