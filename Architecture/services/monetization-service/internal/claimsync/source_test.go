package claimsync

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/atpost/monetization-service/internal/mediaclient"
	"github.com/atpost/monetization-service/internal/store/postgres"
)

type memHolders struct {
	rows   map[postgres.MediaHolderTable][]postgres.MediaHolderRow
	locked []postgres.MediaHolderTable
}

func (m *memHolders) LockMediaHolder(_ context.Context, t postgres.MediaHolderTable, _ uuid.UUID) (func(), error) {
	m.locked = append(m.locked, t)
	return func() {}, nil
}

func (m *memHolders) MediaHolderRows(_ context.Context, t postgres.MediaHolderTable, after uuid.UUID, limit int, all bool) ([]postgres.MediaHolderRow, error) {
	var out []postgres.MediaHolderRow
	for _, r := range m.rows[t] {
		if r.ID.String() > after.String() && (all || r.MediaID != nil) && len(out) < limit {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *memHolders) MediaHolderRowFor(_ context.Context, t postgres.MediaHolderTable, id uuid.UUID) (postgres.MediaHolderRow, error) {
	for _, r := range m.rows[t] {
		if r.ID == id {
			return r, nil
		}
	}
	return postgres.MediaHolderRow{ID: id}, nil
}

// Each kind reads its own column, and the claim names the row.
func TestStoreSourceClaimsBothColumnsUnderTheirKinds(t *testing.T) {
	fund, inv := uuid.MustParse("00000000-0000-4000-8000-000000000001"), uuid.MustParse("00000000-0000-4000-8000-000000000002")
	cover, pdf := uuid.New(), uuid.New()
	h := &memHolders{rows: map[postgres.MediaHolderTable][]postgres.MediaHolderRow{
		postgres.HolderFundraiserCover: {{ID: fund, MediaID: &cover}},
		postgres.HolderInvoicePDF:      {{ID: inv, MediaID: &pdf}},
	}}
	c := newFakeClaims()
	rep, err := Run(context.Background(), NewStoreSource(h, false), c, true)
	if err != nil || rep.Claimed != 2 {
		t.Fatalf("report %s, %v", rep, err)
	}
	if !c.holds(mediaclient.KindFundraiserCover, fund.String(), cover) || !c.holds(mediaclient.KindInvoicePDF, inv.String(), pdf) {
		t.Fatalf("held %v", c.held)
	}
	if c.holds(mediaclient.KindInvoicePDF, fund.String(), cover) {
		t.Fatal("a fundraiser cover was claimed as an invoice PDF")
	}
	if len(h.locked) != 2 || h.locked[0] != postgres.HolderFundraiserCover || h.locked[1] != postgres.HolderInvoicePDF {
		t.Fatalf("locked %v: each row is locked under its own column", h.locked)
	}
}
