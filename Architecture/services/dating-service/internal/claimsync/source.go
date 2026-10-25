package claimsync

import (
	"context"

	"github.com/atpost/dating-service/internal/store"
)

// holderStore is the part of *store.Store the pass reads.
type holderStore interface {
	LockMediaHolder(ctx context.Context, kind, ref string) (func(), error)
	MediaHolderRows(ctx context.Context, kind, after string, limit int, all bool) ([]store.MediaHolderRow, error)
	MediaHolderRowFor(ctx context.Context, kind, ref string) (store.MediaHolderRow, error)
}

// StoreSource reads the four dating holder columns (store/media_holders.go).
type StoreSource struct {
	st  holderStore
	all bool
}

// NewStoreSource lists holders that name media; with all, also clip slots
// and verifications that name none.
func NewStoreSource(st holderStore, all bool) *StoreSource { return &StoreSource{st: st, all: all} }

// Kinds implements Source.
func (s *StoreSource) Kinds() []string { return store.MediaHolderKinds }

// Page implements Source.
func (s *StoreSource) Page(ctx context.Context, kind, after string, limit int) ([]Row, string, error) {
	rows, err := s.st.MediaHolderRows(ctx, kind, after, limit, s.all)
	if err != nil {
		return nil, "", err
	}
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, Row{Kind: r.Kind, Ref: r.Ref, MediaID: r.MediaID})
	}
	next := ""
	if len(rows) == limit {
		next = rows[len(rows)-1].Ref
	}
	return out, next, nil
}

// Lock implements Holders: the writers' per-holder lock.
func (s *StoreSource) Lock(ctx context.Context, row Row) (func(), error) {
	return s.st.LockMediaHolder(ctx, row.Kind, row.Ref)
}

// Reread implements Holders.
func (s *StoreSource) Reread(ctx context.Context, row Row) (Row, error) {
	r, err := s.st.MediaHolderRowFor(ctx, row.Kind, row.Ref)
	if err != nil {
		return Row{}, err
	}
	return Row{Kind: r.Kind, Ref: r.Ref, MediaID: r.MediaID}, nil
}
