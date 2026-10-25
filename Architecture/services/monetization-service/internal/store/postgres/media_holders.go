package postgres

// Media ids this service keeps in its own tables, and the claim protocol
// (media-service internal/store/postgres/media_claims.go).
//
// fundraisers.cover_media_id and invoices.pdf_media_id are plain UUIDs with
// no foreign key into media_assets. media-service lists both as live
// references, and since migration 031 its owner delete is refused only while
// another service holds a CLAIM on the asset. A row written here without a
// claim can therefore have its asset deleted from under it by the uploader.
//
// Nothing in this service writes either column today: no route takes a
// fundraiser cover, and nothing issues invoices (CreateInvoice has no
// caller). So the creates refuse a media id outright (ErrMediaClaimRequired)
// rather than store one unclaimed. A writer added later claims first,
// through internal/mediaclient (kind fundraiser_cover / invoice_pdf, ref =
// the row id), writes, and releases on failure — channel-service's
// internal/service/media_claims.go is the pattern — and only then lifts the
// refusal. Rows written before (seeds, an older build, a manual fix) are
// claimed by cmd/media-claims.

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrMediaClaimRequired refuses storing a media id that was not claimed.
var ErrMediaClaimRequired = errors.New("media ids are stored only after a media-service claim")

// MediaHolderTable names one column that holds media, for the reconcile tool.
type MediaHolderTable string

// The two holder columns.
const (
	HolderFundraiserCover MediaHolderTable = "fundraisers.cover_media_id"
	HolderInvoicePDF      MediaHolderTable = "invoices.pdf_media_id"
)

// holderSQL is each column's page and re-read query. Every row counts as
// live: a paused or completed fundraiser still shows its cover, and an
// invoice is a tax record that must stay retrievable.
var holderSQL = map[MediaHolderTable]struct{ page, one string }{
	HolderFundraiserCover: {
		page: `SELECT id, cover_media_id FROM fundraisers
		       WHERE ($3::boolean OR cover_media_id IS NOT NULL) AND id > $1 ORDER BY id LIMIT $2`,
		one: `SELECT cover_media_id FROM fundraisers WHERE id = $1`,
	},
	HolderInvoicePDF: {
		page: `SELECT id, pdf_media_id FROM invoices
		       WHERE ($3::boolean OR pdf_media_id IS NOT NULL) AND id > $1 ORDER BY id LIMIT $2`,
		one: `SELECT pdf_media_id FROM invoices WHERE id = $1`,
	},
}

// MediaHolderRow is one row and the asset it names (nil: none, or gone).
type MediaHolderRow struct {
	ID      uuid.UUID
	MediaID *uuid.UUID
}

// MediaHolderRows pages through the rows of table that name media (with all,
// every row: one whose media was cleared may still hold a stale claim), in
// id order after `after`.
func (s *Store) MediaHolderRows(ctx context.Context, table MediaHolderTable, after uuid.UUID, limit int, all bool) ([]MediaHolderRow, error) {
	q, ok := holderSQL[table]
	if !ok {
		return nil, fmt.Errorf("unknown media holder %q", table)
	}
	rows, err := s.db.Query(ctx, q.page, after, limit, all)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MediaHolderRow
	for rows.Next() {
		var r MediaHolderRow
		if err := rows.Scan(&r.ID, &r.MediaID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MediaHolderRowFor re-reads one row under LockMediaHolder. A row that no
// longer exists holds nothing.
func (s *Store) MediaHolderRowFor(ctx context.Context, table MediaHolderTable, id uuid.UUID) (MediaHolderRow, error) {
	q, ok := holderSQL[table]
	if !ok {
		return MediaHolderRow{}, fmt.Errorf("unknown media holder %q", table)
	}
	r := MediaHolderRow{ID: id}
	err := s.db.QueryRow(ctx, q.one, id).Scan(&r.MediaID)
	if errors.Is(err, pgx.ErrNoRows) {
		return MediaHolderRow{ID: id}, nil
	}
	return r, err
}

// LockMediaHolder serialises the claim-write-release of one row's media, for
// as long as the caller holds it (the reconcile tool, and any writer added
// later). Transaction-scoped; unlock is idempotent. Callers pass the
// in-process holderGate before taking a connection, so lock holders can
// never exhaust the pool (media_holder_gate.go).
func (s *Store) LockMediaHolder(ctx context.Context, table MediaHolderTable, id uuid.UUID) (unlock func(), err error) {
	return lockMediaHolder(ctx, s.holders, s.db, mediaHolderLockKey(table, id))
}

// lockMediaHolderSQL waits (never try_) for an exclusive, transaction-scoped
// lock. Pinned by TestMediaHolderLockWaitsAndIsTransactionScoped.
const lockMediaHolderSQL = `SELECT pg_advisory_xact_lock($1)`

func mediaHolderLockKey(table MediaHolderTable, id uuid.UUID) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("monetization-media-holder:" + string(table) + ":"))
	_, _ = h.Write(id[:])
	return int64(h.Sum64())
}
