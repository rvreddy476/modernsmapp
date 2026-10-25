package claimsync

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/atpost/monetization-service/internal/mediaclient"
	"github.com/atpost/monetization-service/internal/store/postgres"
)

// holderStore is the part of *postgres.Store the pass reads.
type holderStore interface {
	LockMediaHolder(ctx context.Context, table postgres.MediaHolderTable, id uuid.UUID) (func(), error)
	MediaHolderRows(ctx context.Context, table postgres.MediaHolderTable, after uuid.UUID, limit int, all bool) ([]postgres.MediaHolderRow, error)
	MediaHolderRowFor(ctx context.Context, table postgres.MediaHolderTable, id uuid.UUID) (postgres.MediaHolderRow, error)
}

// kindTables maps each claim kind to the column that holds it (ref = row id).
var kindTables = map[string]postgres.MediaHolderTable{
	mediaclient.KindFundraiserCover: postgres.HolderFundraiserCover,
	mediaclient.KindInvoicePDF:      postgres.HolderInvoicePDF,
}

// StoreSource reads both holder columns.
type StoreSource struct {
	st  holderStore
	all bool
}

// NewStoreSource lists rows that name media; with all, every row.
func NewStoreSource(st holderStore, all bool) *StoreSource { return &StoreSource{st: st, all: all} }

// Kinds implements Source.
func (s *StoreSource) Kinds() []string {
	return []string{mediaclient.KindFundraiserCover, mediaclient.KindInvoicePDF}
}

func table(kind string) (postgres.MediaHolderTable, error) {
	t, ok := kindTables[kind]
	if !ok {
		return "", fmt.Errorf("unknown kind %q", kind)
	}
	return t, nil
}

// Page implements Source.
func (s *StoreSource) Page(ctx context.Context, kind, after string, limit int) ([]Row, string, error) {
	t, err := table(kind)
	if err != nil {
		return nil, "", err
	}
	cursor := uuid.Nil
	if after != "" {
		if cursor, err = uuid.Parse(after); err != nil {
			return nil, "", fmt.Errorf("bad cursor %q: %w", after, err)
		}
	}
	rows, err := s.st.MediaHolderRows(ctx, t, cursor, limit, s.all)
	if err != nil {
		return nil, "", err
	}
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, Row{Kind: kind, Ref: r.ID.String(), MediaID: r.MediaID})
	}
	next := ""
	if len(rows) == limit {
		next = rows[len(rows)-1].ID.String()
	}
	return out, next, nil
}

// Lock implements Holders.
func (s *StoreSource) Lock(ctx context.Context, row Row) (func(), error) {
	t, err := table(row.Kind)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(row.Ref)
	if err != nil {
		return nil, err
	}
	return s.st.LockMediaHolder(ctx, t, id)
}

// Reread implements Holders.
func (s *StoreSource) Reread(ctx context.Context, row Row) (Row, error) {
	t, err := table(row.Kind)
	if err != nil {
		return Row{}, err
	}
	id, err := uuid.Parse(row.Ref)
	if err != nil {
		return Row{}, err
	}
	r, err := s.st.MediaHolderRowFor(ctx, t, id)
	if err != nil {
		return Row{}, err
	}
	return Row{Kind: row.Kind, Ref: r.ID.String(), MediaID: r.MediaID}, nil
}
