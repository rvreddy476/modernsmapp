package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// busyTx behaves like one pgx connection: a statement issued while a result
// set is still open fails with "conn busy" (CI found an answer in a
// community failing this way).
type busyTx struct {
	open    bool
	topics  []uuid.UUID
	execs   int
	queries int
}

var errConnBusy = errors.New("conn busy")

func (b *busyTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	if b.open {
		return pgconn.CommandTag{}, errConnBusy
	}
	b.execs++
	return pgconn.CommandTag{}, nil
}

func (b *busyTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	if b.open {
		return nil, errConnBusy
	}
	b.queries++
	b.open = true
	return &busyRows{tx: b, ids: b.topics, i: -1}, nil
}

func (b *busyTx) QueryRow(context.Context, string, ...any) pgx.Row { return nil }

type busyRows struct {
	pgx.Rows
	tx  *busyTx
	ids []uuid.UUID
	i   int
}

func (r *busyRows) Next() bool {
	r.i++
	if r.i >= len(r.ids) {
		r.Close()
		return false
	}
	return true
}

func (r *busyRows) Scan(dest ...any) error {
	*(dest[0].(*uuid.UUID)) = r.ids[r.i]
	return nil
}

func (r *busyRows) Close()     { r.tx.open = false }
func (r *busyRows) Err() error { return nil }

// The topic affinity bumps read a question's topics, then write one row per
// topic on the same transaction: the reads must be finished first.
func TestTopicBumpsDoNotWriteWhileReading(t *testing.T) {
	s := &Store{}
	for name, bump := range map[string]func(context.Context, dbtx, uuid.UUID, uuid.UUID) error{
		"answer": s.bumpCommunityTopicAnswerCountTx,
		"view":   s.bumpCommunityTopicViewCountTx,
	} {
		tx := &busyTx{topics: []uuid.UUID{uuid.New(), uuid.New()}}
		if err := bump(context.Background(), tx, uuid.New(), uuid.New()); err != nil {
			t.Fatalf("%s bump: %v", name, err)
		}
		if tx.execs != 2 {
			t.Fatalf("%s bump wrote %d rows, want one per topic", name, tx.execs)
		}
	}
}
