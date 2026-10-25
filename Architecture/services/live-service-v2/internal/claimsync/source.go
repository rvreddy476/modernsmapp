package claimsync

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/mediaclient"
	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// coverStore is the part of *postgres.Store the pass reads.
type coverStore interface {
	LockMediaHolder(ctx context.Context, streamID uuid.UUID) (func(), error)
	CoverClaimRows(ctx context.Context, after uuid.UUID, limit int, all bool) ([]postgres.CoverClaimRow, error)
	CoverClaimRowFor(ctx context.Context, streamID uuid.UUID) (postgres.CoverClaimRow, error)
}

// StoreSource reads live_streams covers (kind live_cover, ref = stream id).
type StoreSource struct {
	st  coverStore
	all bool
}

// NewStoreSource lists streams that name a cover; with all, every stream (a
// stream whose cover was cleared may still hold a stale claim).
func NewStoreSource(st coverStore, all bool) *StoreSource { return &StoreSource{st: st, all: all} }

// Kinds implements Source.
func (s *StoreSource) Kinds() []string { return []string{mediaclient.KindLiveCover} }

// Page implements Source.
func (s *StoreSource) Page(ctx context.Context, kind, after string, limit int) ([]Row, string, error) {
	if kind != mediaclient.KindLiveCover {
		return nil, "", fmt.Errorf("unknown kind %q", kind)
	}
	cursor := uuid.Nil
	if after != "" {
		var err error
		if cursor, err = uuid.Parse(after); err != nil {
			return nil, "", fmt.Errorf("bad cursor %q: %w", after, err)
		}
	}
	rows, err := s.st.CoverClaimRows(ctx, cursor, limit, s.all)
	if err != nil {
		return nil, "", err
	}
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, coverRow(r))
	}
	next := ""
	if len(rows) == limit {
		next = rows[len(rows)-1].StreamID.String()
	}
	return out, next, nil
}

// Lock implements Holders: the writers' per-stream lock.
func (s *StoreSource) Lock(ctx context.Context, row Row) (func(), error) {
	id, err := uuid.Parse(row.Ref)
	if err != nil {
		return nil, err
	}
	return s.st.LockMediaHolder(ctx, id)
}

// Reread implements Holders.
func (s *StoreSource) Reread(ctx context.Context, row Row) (Row, error) {
	id, err := uuid.Parse(row.Ref)
	if err != nil {
		return Row{}, err
	}
	r, err := s.st.CoverClaimRowFor(ctx, id)
	if err != nil {
		return Row{}, err
	}
	return coverRow(r), nil
}

func coverRow(r postgres.CoverClaimRow) Row {
	return Row{Kind: mediaclient.KindLiveCover, Ref: r.StreamID.String(), MediaID: r.Cover}
}
