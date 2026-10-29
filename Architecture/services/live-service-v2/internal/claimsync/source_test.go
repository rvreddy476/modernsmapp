package claimsync

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/mediaclient"
	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// fakeStore is the holder tables the StoreSource reads.
type fakeStore struct {
	covers  map[uuid.UUID]*uuid.UUID
	imports map[uuid.UUID]*uuid.UUID // stream -> the id the import holds now
	locked  []uuid.UUID
}

func (f *fakeStore) LockMediaHolder(_ context.Context, id uuid.UUID) (func(), error) {
	f.locked = append(f.locked, id)
	return func() {}, nil
}

func (f *fakeStore) CoverClaimRows(context.Context, uuid.UUID, int, bool) ([]postgres.CoverClaimRow, error) {
	var out []postgres.CoverClaimRow
	for id, c := range f.covers {
		out = append(out, postgres.CoverClaimRow{StreamID: id, Cover: c})
	}
	return out, nil
}

func (f *fakeStore) CoverClaimRowFor(_ context.Context, id uuid.UUID) (postgres.CoverClaimRow, error) {
	return postgres.CoverClaimRow{StreamID: id, Cover: f.covers[id]}, nil
}

func (f *fakeStore) ImportClaimRows(context.Context, uuid.UUID, int, bool) ([]postgres.ImportClaimRow, error) {
	var out []postgres.ImportClaimRow
	for id, h := range f.imports {
		out = append(out, postgres.ImportClaimRow{StreamID: id, Held: h})
	}
	return out, nil
}

func (f *fakeStore) ImportClaimRowFor(_ context.Context, id uuid.UUID) (postgres.ImportClaimRow, error) {
	return postgres.ImportClaimRow{StreamID: id, Held: f.imports[id]}, nil
}

// One pass covers both kinds under their own kind: a held recording is
// claimed, a settled one (holds nothing now) is released, and a cover on the
// same stream is untouched by either.
func TestStoreSourceReconcilesRecordingImports(t *testing.T) {
	stream, settled := uuid.New(), uuid.New()
	cover, rec, old := uuid.New(), uuid.New(), uuid.New()
	st := &fakeStore{
		covers:  map[uuid.UUID]*uuid.UUID{stream: &cover},
		imports: map[uuid.UUID]*uuid.UUID{stream: &rec, settled: nil},
	}
	c := newFakeClaims()
	c.held[key(mediaclient.KindLiveRecordingImport, settled.String())] = map[uuid.UUID]bool{old: true}
	src := NewStoreSource(st, false)
	if got := src.Kinds(); len(got) != 2 || got[1] != mediaclient.KindLiveRecordingImport {
		t.Fatalf("kinds %v", got)
	}
	rep, err := Run(context.Background(), src, c, true)
	if err != nil || rep.Failed != 0 {
		t.Fatalf("run: %v %s", err, rep)
	}
	if !c.holds(mediaclient.KindLiveRecordingImport, stream.String(), rec) || c.holds(mediaclient.KindLiveRecordingImport, stream.String(), cover) {
		t.Fatalf("import claims %v", c.held)
	}
	if !c.holds(mediaclient.KindLiveCover, stream.String(), cover) || c.holds(mediaclient.KindLiveCover, stream.String(), rec) {
		t.Fatalf("cover claims %v", c.held)
	}
	if len(c.held[key(mediaclient.KindLiveRecordingImport, settled.String())]) != 0 {
		t.Fatal("a settled import's stale claim was not released")
	}
	if len(st.locked) != 3 {
		t.Fatalf("every row is locked before its re-read: %v", st.locked)
	}
}
