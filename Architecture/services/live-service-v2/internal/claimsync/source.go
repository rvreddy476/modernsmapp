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
	ImportClaimRows(ctx context.Context, after uuid.UUID, limit int, all bool) ([]postgres.ImportClaimRow, error)
	ImportClaimRowFor(ctx context.Context, streamID uuid.UUID) (postgres.ImportClaimRow, error)
}

// StoreSource reads live_streams covers (kind live_cover) and
// live_recording_imports media (kind live_recording_import); ref = stream id
// for both, and both are locked with the stream's holder lock.
type StoreSource struct {
	st  coverStore
	all bool
}

// NewStoreSource lists streams that name a cover; with all, every stream (a
// stream whose cover was cleared may still hold a stale claim).
func NewStoreSource(st coverStore, all bool) *StoreSource { return &StoreSource{st: st, all: all} }

// Kinds implements Source.
func (s *StoreSource) Kinds() []string {
	return []string{mediaclient.KindLiveCover, mediaclient.KindLiveRecordingImport}
}

// Page implements Source.
func (s *StoreSource) Page(ctx context.Context, kind, after string, limit int) ([]Row, string, error) {
	cursor := uuid.Nil
	if after != "" {
		var err error
		if cursor, err = uuid.Parse(after); err != nil {
			return nil, "", fmt.Errorf("bad cursor %q: %w", after, err)
		}
	}
	var out []Row
	switch kind {
	case mediaclient.KindLiveCover:
		rows, err := s.st.CoverClaimRows(ctx, cursor, limit, s.all)
		if err != nil {
			return nil, "", err
		}
		for _, r := range rows {
			out = append(out, coverRow(r))
		}
	case mediaclient.KindLiveRecordingImport:
		rows, err := s.st.ImportClaimRows(ctx, cursor, limit, s.all)
		if err != nil {
			return nil, "", err
		}
		for _, r := range rows {
			out = append(out, importRow(r))
		}
	default:
		return nil, "", fmt.Errorf("unknown kind %q", kind)
	}
	next := ""
	if len(out) == limit {
		next = out[len(out)-1].Ref
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
	switch row.Kind {
	case mediaclient.KindLiveCover:
		r, err := s.st.CoverClaimRowFor(ctx, id)
		if err != nil {
			return Row{}, err
		}
		return coverRow(r), nil
	case mediaclient.KindLiveRecordingImport:
		r, err := s.st.ImportClaimRowFor(ctx, id)
		if err != nil {
			return Row{}, err
		}
		return importRow(r), nil
	}
	return Row{}, fmt.Errorf("unknown kind %q", row.Kind)
}

func coverRow(r postgres.CoverClaimRow) Row {
	return Row{Kind: mediaclient.KindLiveCover, Ref: r.StreamID.String(), MediaID: r.Cover}
}

func importRow(r postgres.ImportClaimRow) Row {
	return Row{Kind: mediaclient.KindLiveRecordingImport, Ref: r.StreamID.String(), MediaID: r.Held}
}
