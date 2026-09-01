// Package piibackfill encrypts the existing address estate, resumably.
//
// B5. Every customer and seller address in the database is currently readable
// by anyone holding a database credential. The cutover cannot simply start
// encrypting new writes and declare victory: the rows already there are the
// ones that matter, and they have to be sealed, VERIFIED, and only then may
// their plaintext be cleared.
//
// # The one invariant
//
// Progress never runs ahead of the data. The ciphertext and the cursor that
// says "this row is done" commit in the SAME transaction. There is no ordering
// in which a crash leaves a row marked complete without its ciphertext — which
// matters because the gated scrub trusts that cursor to decide what is safe to
// clear, and a row wrongly marked complete would have its address destroyed.
//
// # Verify before advancing
//
// Every row is sealed, then immediately DECRYPTED and compared against the
// source before the transaction commits. Sealing that silently produced
// garbage would otherwise be discovered only after the scrub had removed the
// only other copy. The verification costs one KMS-cached AES operation per row
// and removes the entire class of "encrypted, but to nothing".
//
// # Resumption, and why there is no cursor
//
// Each batch selects the next rows that ARE STILL UNSEALED, ordered by primary
// key. The work remaining is a property of the data, not of a bookmark.
//
// The first version of this job kept a `last_id` cursor and selected
// `WHERE id > last_id`. That is a valid completeness argument only when
// primary keys increase monotonically, and these do not: they are
// `gen_random_uuid()`. A row inserted after the cursor passed its position —
// which is every row a live service writes while the backfill runs — sorts
// below the cursor and would never be visited again. The job's own proof
// caught it: twelve addresses sat permanently unsealed behind a cursor that
// claimed to be finished, and the gated scrub's independent data check was the
// only thing standing between them and destruction.
//
// `last_id` is still recorded, because it is genuinely useful for an operator
// watching progress. It is no longer load-bearing for correctness.
//
// Restarting is therefore always safe and always complete: a row that already
// has ciphertext is not selected, so re-running over a finished table is a
// no-op rather than a re-encryption that would change every key version.
package piibackfill

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/commerce-service/internal/pii"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Stats is the observable state of one table's backfill.
type Stats struct {
	Table     string
	Total     int64
	Encrypted int64
	Verified  int64
	Failed    int64
	Remaining int64
	LastID    *uuid.UUID
	Completed bool
}

func (s Stats) String() string {
	last := "<none>"
	if s.LastID != nil {
		last = s.LastID.String()
	}
	return fmt.Sprintf(
		"%s: total=%d encrypted=%d verified=%d failed=%d remaining=%d last_id=%s completed=%t",
		s.Table, s.Total, s.Encrypted, s.Verified, s.Failed, s.Remaining, last, s.Completed)
}

// Job encrypts one or more address tables.
type Job struct {
	pool   *pgxpool.Pool
	cipher *pii.Cipher

	// BatchSize bounds how many rows one transaction touches. Small enough
	// that a KMS stall cannot hold a long transaction open against the table
	// customers are actively writing to.
	BatchSize int
}

// New builds the job. Both dependencies are required: without the cipher there
// is nothing to seal with, and a "backfill" that skipped sealing would advance
// the cursor and licence the scrub to clear plaintext.
func New(pool *pgxpool.Pool, cipher *pii.Cipher) (*Job, error) {
	if pool == nil {
		return nil, errors.New("piibackfill: a database pool is required")
	}
	if cipher == nil {
		return nil, errors.New("piibackfill: a PII cipher is required")
	}
	return &Job{pool: pool, cipher: cipher, BatchSize: 200}, nil
}

// lockKey serialises backfill runs. Two concurrent workers over one ordered
// cursor would each advance it past rows the other was mid-way through.
const lockKey = int64(0x7069696266) // "piibf"

// Run backfills every supported table until each reports nothing left.
func (j *Job) Run(ctx context.Context) ([]Stats, error) {
	conn, err := j.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, lockKey).Scan(&got); err != nil {
		return nil, err
	}
	if !got {
		return nil, errors.New("piibackfill: another backfill is already running")
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, lockKey) }()

	var out []Stats
	for _, t := range tables {
		s, err := j.runTable(ctx, t)
		if err != nil {
			return out, fmt.Errorf("piibackfill: %s: %w", t.name, err)
		}
		out = append(out, s)
	}

	// Orders carry a snapshot rather than address columns; separate path,
	// same invariant.
	os, err := j.runOrders(ctx)
	if err != nil {
		return out, fmt.Errorf("piibackfill: orders: %w", err)
	}
	out = append(out, os)
	return out, nil
}

// runTable drives one table to completion.
func (j *Job) runTable(ctx context.Context, t table) (Stats, error) {
	if err := j.ensureProgressRow(ctx, t.name); err != nil {
		return Stats{}, err
	}
	for {
		select {
		case <-ctx.Done():
			// A cancelled run is not a failure: the cursor is durable and the
			// next run resumes exactly where this stopped.
			return j.Stats(ctx, t)
		default:
		}

		done, err := j.batch(ctx, t)
		if err != nil {
			return Stats{}, err
		}
		if done {
			break
		}
	}
	if err := j.markComplete(ctx, t); err != nil {
		return Stats{}, err
	}
	return j.Stats(ctx, t)
}

// batch seals one bounded set of rows and advances the cursor with them.
//
// Returns true when nothing remains.
func (j *Job) batch(ctx context.Context, t table) (bool, error) {
	// Selected OUTSIDE the write transaction: sealing is a KMS call, and
	// holding a transaction open across a network round trip is how a
	// backfill becomes an outage on the table customers are writing to.
	//
	// The predicate is "still unsealed", so a row written while this runs is
	// picked up by a later batch rather than sorted behind a cursor and lost.
	rows, err := j.pool.Query(ctx, t.selectSQL, j.BatchSize)
	if err != nil {
		return false, err
	}
	candidates, err := t.scan(rows)
	if err != nil {
		return false, err
	}
	if len(candidates) == 0 {
		return true, nil
	}

	for _, c := range candidates {
		if err := j.sealOne(ctx, t, c); err != nil {
			// Record the failure durably and stop. The cursor does NOT
			// advance past a row that failed, so a retry re-attempts exactly
			// this row rather than leaving a plaintext gap behind a cursor
			// that claims to have covered it.
			if mErr := j.recordFailure(ctx, t.name, c.id, err); mErr != nil {
				return false, fmt.Errorf("%w (and recording it failed: %v)", err, mErr)
			}
			return false, err
		}
	}
	return false, nil
}

// sealOne encrypts, verifies and commits a single row with its cursor.
func (j *Job) sealOne(ctx context.Context, t table, c candidate) error {
	// Already sealed by a concurrent writer or an earlier run: advance past
	// it without re-encrypting. Re-sealing would change the key version of a
	// row that is already correct, for no benefit.
	if c.alreadySealed {
		return j.advance(ctx, t.name, c.id, false)
	}

	sealed, err := j.cipher.SealAddress(ctx, t.scope, c.address)
	if err != nil {
		return fmt.Errorf("sealing row %s: %w", c.id, err)
	}

	// VERIFY before committing. A seal that produced garbage would otherwise
	// be discovered only after the scrub removed the only other copy.
	opened, err := j.cipher.OpenAddress(ctx, t.scope, *sealed,
		c.address.City, c.address.State, c.address.PostalCode, c.address.Country)
	if err != nil {
		return fmt.Errorf("verifying row %s: %w", c.id, err)
	}
	if opened.ContactName != c.address.ContactName ||
		opened.Phone != c.address.Phone ||
		opened.AddressLine1 != c.address.AddressLine1 ||
		opened.AddressLine2 != c.address.AddressLine2 ||
		opened.Landmark != c.address.Landmark {
		return fmt.Errorf("row %s: decrypt did not reproduce the source", c.id)
	}

	tx, err := j.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tag, err := tx.Exec(ctx, t.updateSQL, t.updateArgs(c.id, sealed)...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		// The row moved or was deleted under us. Not an error — but the
		// cursor must not claim to have sealed it.
		return fmt.Errorf("row %s: %d rows updated, want 1", c.id, tag.RowsAffected())
	}

	// THE invariant: the cursor advances in the same transaction as the
	// ciphertext. There is no interleaving that marks this row done without
	// its ciphertext being committed.
	if _, err := tx.Exec(ctx, `
		UPDATE pii_backfill_progress
		   SET last_id    = $2,
		       encrypted_rows = encrypted_rows + 1,
		       verified   = verified + 1,
		       updated_at = NOW()
		 WHERE table_name = $1`, t.name, c.id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// advance moves the cursor past a row that needed no work.
func (j *Job) advance(ctx context.Context, name string, id uuid.UUID, counted bool) error {
	delta := 0
	if counted {
		delta = 1
	}
	_, err := j.pool.Exec(ctx, `
		UPDATE pii_backfill_progress
		   SET last_id    = $2,
		       encrypted_rows = encrypted_rows + $3,
		       updated_at = NOW()
		 WHERE table_name = $1`, name, id, delta)
	return err
}

func (j *Job) recordFailure(ctx context.Context, name string, id uuid.UUID, cause error) error {
	// The row id and a KIND, never the plaintext and never the cipher's
	// error detail, which could name the material.
	kind := "seal_or_verify_failed"
	if errors.Is(cause, context.DeadlineExceeded) {
		kind = "kms_timeout"
	}
	slog.Error("piibackfill: row failed", "table", name, "row_id", id, "kind", kind)
	// completed_at is cleared in the SAME statement. A failure means this
	// table is not complete, and `runTable` returns before markComplete when
	// a batch fails — so if the stamp were only cleared there, a table that
	// completed yesterday and fails today would keep claiming success. The
	// gated scrub reads exactly this column to decide whether clearing
	// plaintext is safe.
	_, err := j.pool.Exec(ctx, `
		UPDATE pii_backfill_progress
		   SET failed          = failed + 1,
		       last_error_id   = $2,
		       last_error_at   = NOW(),
		       last_error_kind = $3,
		       completed_at    = NULL,
		       updated_at      = NOW()
		 WHERE table_name = $1`, name, id, kind)
	return err
}

func (j *Job) ensureProgressRow(ctx context.Context, name string) error {
	_, err := j.pool.Exec(ctx, `
		INSERT INTO pii_backfill_progress (table_name) VALUES ($1)
		ON CONFLICT (table_name) DO NOTHING`, name)
	return err
}

// markComplete stamps a table only when nothing remains AND nothing failed.
//
// The re-check is not redundant: rows can be written while the job runs, and
// completion has to mean "nothing is left now", not "nothing was left when the
// last batch was read".
func (j *Job) markComplete(ctx context.Context, t table) error {
	var remaining, failed int64
	if err := j.pool.QueryRow(ctx, t.remainingSQL).Scan(&remaining); err != nil {
		return err
	}
	if err := j.pool.QueryRow(ctx,
		`SELECT failed FROM pii_backfill_progress WHERE table_name=$1`, t.name).Scan(&failed); err != nil {
		return err
	}
	if remaining > 0 || failed > 0 {
		slog.Warn("piibackfill: table not marked complete",
			"table", t.name, "remaining", remaining, "failed", failed)
		// CLEAR a stale stamp. A table that was complete and has since
		// gained unsealed rows — a straggler writer, a restored backup — is
		// not complete any more, and the gated scrub reads this column to
		// decide whether clearing plaintext is safe. Leaving the old stamp
		// would let it proceed on a claim that stopped being true.
		_, err := j.pool.Exec(ctx, `
			UPDATE pii_backfill_progress SET completed_at = NULL, updated_at = NOW()
			 WHERE table_name = $1 AND completed_at IS NOT NULL`, t.name)
		return err
	}
	_, err := j.pool.Exec(ctx, `
		UPDATE pii_backfill_progress
		   SET completed_at = NOW(), updated_at = NOW()
		 WHERE table_name = $1 AND completed_at IS NULL`, t.name)
	return err
}

// Stats reports one table's counters, for an operator or a readiness check.
func (j *Job) Stats(ctx context.Context, t table) (Stats, error) {
	s := Stats{Table: t.name}
	var completed *time.Time
	err := j.pool.QueryRow(ctx, `
		SELECT last_id, encrypted_rows, verified, failed, completed_at
		  FROM pii_backfill_progress WHERE table_name = $1`, t.name).
		Scan(&s.LastID, &s.Encrypted, &s.Verified, &s.Failed, &completed)
	if err != nil {
		return s, err
	}
	s.Completed = completed != nil
	if err := j.pool.QueryRow(ctx, t.totalSQL).Scan(&s.Total); err != nil {
		return s, err
	}
	if err := j.pool.QueryRow(ctx, t.remainingSQL).Scan(&s.Remaining); err != nil {
		return s, err
	}
	return s, nil
}

// AllStats reports every tracked table.
func (j *Job) AllStats(ctx context.Context) ([]Stats, error) {
	var out []Stats
	for _, t := range tables {
		if err := j.ensureProgressRow(ctx, t.name); err != nil {
			return nil, err
		}
		s, err := j.Stats(ctx, t)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// candidate is one row awaiting sealing.
type candidate struct {
	id            uuid.UUID
	address       pii.Address
	alreadySealed bool
}

// table describes how to find, seal and update one table's addresses.
type table struct {
	name  string
	scope pii.Scope

	selectSQL    string
	updateSQL    string
	totalSQL     string
	remainingSQL string

	scan func(pgx.Rows) ([]candidate, error)

	// updateArgs builds the parameter list for updateSQL.
	//
	// Per-table because the two schemas genuinely differ: seller_addresses
	// has no landmark and no lookup hash. Passing a fixed eight parameters
	// and contriving for a statement to ignore two of them would be a lie
	// about the schema that the next person has to decode.
	updateArgs func(id uuid.UUID, s *pii.Sealed) []any
}

func scanAddressRows(rows pgx.Rows) ([]candidate, error) {
	defer rows.Close()
	var out []candidate
	for rows.Next() {
		var (
			c               candidate
			line2, landmark *string
			enc             []byte
		)
		if err := rows.Scan(&c.id, &c.address.ContactName, &c.address.Phone,
			&c.address.AddressLine1, &line2, &landmark,
			&c.address.City, &c.address.State, &c.address.PostalCode, &c.address.Country,
			&enc); err != nil {
			return nil, err
		}
		if line2 != nil {
			c.address.AddressLine2 = *line2
		}
		if landmark != nil {
			c.address.Landmark = *landmark
		}
		c.alreadySealed = len(enc) > 0
		out = append(out, c)
	}
	return out, rows.Err()
}

// tables is the inventory. Adding a table here is the whole change needed to
// bring it into the cutover — and the gated scrub reads the same list, so a
// table added to one and not the other cannot pass.
var tables = []table{
	{
		name:  "customer_addresses",
		scope: pii.ScopeProfile,
		selectSQL: `SELECT id, contact_name, phone, address_line_1, address_line_2, landmark,
		                   city, state, postal_code, country, contact_name_enc
		              FROM customer_addresses
		             WHERE contact_name_enc IS NULL
		             ORDER BY id
		             LIMIT $1`,
		updateSQL: `UPDATE customer_addresses
		               SET contact_name_enc=$2, phone_enc=$3, address_line_1_enc=$4,
		                   address_line_2_enc=$5, landmark_enc=$6,
		                   pii_key_version=$7, lookup_hash=NULLIF($8,''), updated_at=NOW()
		             WHERE id=$1`,
		totalSQL:     `SELECT count(*) FROM customer_addresses`,
		remainingSQL: `SELECT count(*) FROM customer_addresses WHERE contact_name_enc IS NULL`,
		scan:         scanAddressRows,
		updateArgs: func(id uuid.UUID, s *pii.Sealed) []any {
			return []any{id, s.ContactName, s.Phone, s.AddressLine1,
				s.AddressLine2, s.Landmark, s.KeyVersion, s.LookupHash}
		},
	},
	{
		name:  "seller_addresses",
		scope: pii.ScopeProfile,
		selectSQL: `SELECT id, contact_name, phone, address_line_1, address_line_2, NULL::text,
		                   city, state, postal_code, country, contact_name_enc
		              FROM seller_addresses
		             WHERE contact_name_enc IS NULL
		             ORDER BY id
		             LIMIT $1`,
		// seller_addresses has no landmark and no lookup hash: a seller
		// pickup address is never looked up by content the way a customer's
		// is, and the schema reflects that.
		updateSQL: `UPDATE seller_addresses
		               SET contact_name_enc=$2, phone_enc=$3, address_line_1_enc=$4,
		                   address_line_2_enc=$5, pii_key_version=$6
		             WHERE id=$1`,
		totalSQL:     `SELECT count(*) FROM seller_addresses`,
		remainingSQL: `SELECT count(*) FROM seller_addresses WHERE contact_name_enc IS NULL`,
		scan:         scanAddressRows,
		updateArgs: func(id uuid.UUID, s *pii.Sealed) []any {
			return []any{id, s.ContactName, s.Phone, s.AddressLine1,
				s.AddressLine2, s.KeyVersion}
		},
	},
}

// Tables exposes the inventory for the scrub's contract test, so the two
// cannot disagree about what has to be encrypted.
func Tables() []string {
	out := make([]string, 0, len(tables)+1)
	for _, t := range tables {
		out = append(out, t.name)
	}
	return append(out, "orders")
}

// ─── Orders: the delivery-address snapshot ───────────────────────────

// runOrders seals legacy order snapshots.
//
// An order's snapshot is a JSON copy of the address as it was, kept so the
// order can be fulfilled and invoiced independently of later edits. Checkout
// has sealed it into `delivery_address_snapshot_enc` since migration 011, but
// every order placed before that has plaintext only — and the gated scrub
// reduces the plaintext snapshot to its routing fields, so an unsealed one
// would lose the address it exists to preserve.
//
// It is separate from the address tables because the shape genuinely differs:
// there is one opaque JSON blob rather than five columns, and it is sealed
// under ScopeOrderSnapshot so that a future profile-address shred cannot
// destroy an invoice record (review §5-D8).
func (j *Job) runOrders(ctx context.Context) (Stats, error) {
	const name = "orders"
	if err := j.ensureProgressRow(ctx, name); err != nil {
		return Stats{}, err
	}

	for {
		select {
		case <-ctx.Done():
			return j.ordersStats(ctx)
		default:
		}

		rows, err := j.pool.Query(ctx, `
			SELECT id, delivery_address_snapshot, delivery_address_snapshot_enc
			  FROM orders
			 WHERE delivery_address_snapshot IS NOT NULL
			   AND delivery_address_snapshot_enc IS NULL
			 ORDER BY id
			 LIMIT $1`, j.BatchSize)
		if err != nil {
			return Stats{}, err
		}
		type snap struct {
			id     uuid.UUID
			blob   []byte
			sealed []byte
		}
		var batch []snap
		for rows.Next() {
			var s snap
			if err := rows.Scan(&s.id, &s.blob, &s.sealed); err != nil {
				rows.Close()
				return Stats{}, err
			}
			batch = append(batch, s)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return Stats{}, err
		}
		if len(batch) == 0 {
			break
		}

		for _, s := range batch {
			if len(s.sealed) > 0 {
				if err := j.advance(ctx, name, s.id, false); err != nil {
					return Stats{}, err
				}
				continue
			}
			if err := j.sealOrder(ctx, name, s.id, s.blob); err != nil {
				if mErr := j.recordFailure(ctx, name, s.id, err); mErr != nil {
					return Stats{}, fmt.Errorf("%w (and recording it failed: %v)", err, mErr)
				}
				return Stats{}, err
			}
		}
	}

	var remaining, failed int64
	if err := j.pool.QueryRow(ctx, ordersRemainingSQL).Scan(&remaining); err != nil {
		return Stats{}, err
	}
	if err := j.pool.QueryRow(ctx,
		`SELECT failed FROM pii_backfill_progress WHERE table_name=$1`, name).Scan(&failed); err != nil {
		return Stats{}, err
	}
	if remaining == 0 && failed == 0 {
		if _, err := j.pool.Exec(ctx, `
			UPDATE pii_backfill_progress SET completed_at=NOW(), updated_at=NOW()
			 WHERE table_name=$1 AND completed_at IS NULL`, name); err != nil {
			return Stats{}, err
		}
	} else if _, err := j.pool.Exec(ctx, `
				UPDATE pii_backfill_progress SET completed_at=NULL, updated_at=NOW()
				 WHERE table_name=$1 AND completed_at IS NOT NULL`, name); err != nil {
		// Same regression clear as the address path: a stamp that stopped
		// being true must not licence the scrub.
		return Stats{}, err
	}
	return j.ordersStats(ctx)
}

// sealOrder seals one snapshot, verifies it, and advances the cursor with it
// in one transaction — the same invariant the address path keeps.
func (j *Job) sealOrder(ctx context.Context, name string, id uuid.UUID, blob []byte) error {
	sealed, version, err := j.cipher.Seal(ctx, pii.ScopeOrderSnapshot, string(blob))
	if err != nil {
		return fmt.Errorf("sealing order %s: %w", id, err)
	}
	opened, err := j.cipher.Open(ctx, pii.ScopeOrderSnapshot, sealed)
	if err != nil {
		return fmt.Errorf("verifying order %s: %w", id, err)
	}
	if opened != string(blob) {
		return fmt.Errorf("order %s: decrypt did not reproduce the snapshot", id)
	}

	tx, err := j.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tag, err := tx.Exec(ctx, `
		UPDATE orders
		   SET delivery_address_snapshot_enc = $2,
		       snapshot_key_version          = $3,
		       updated_at                    = NOW()
		 WHERE id = $1`, id, sealed, version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("order %s: %d rows updated, want 1", id, tag.RowsAffected())
	}
	if _, err := tx.Exec(ctx, `
		UPDATE pii_backfill_progress
		   SET last_id=$2, encrypted_rows=encrypted_rows+1, verified=verified+1, updated_at=NOW()
		 WHERE table_name=$1`, name, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

const ordersRemainingSQL = `
	SELECT count(*) FROM orders
	 WHERE delivery_address_snapshot IS NOT NULL
	   AND delivery_address_snapshot_enc IS NULL`

func (j *Job) ordersStats(ctx context.Context) (Stats, error) {
	s := Stats{Table: "orders"}
	var completed *time.Time
	if err := j.pool.QueryRow(ctx, `
		SELECT last_id, encrypted_rows, verified, failed, completed_at
		  FROM pii_backfill_progress WHERE table_name='orders'`).
		Scan(&s.LastID, &s.Encrypted, &s.Verified, &s.Failed, &completed); err != nil {
		return s, err
	}
	s.Completed = completed != nil
	if err := j.pool.QueryRow(ctx,
		`SELECT count(*) FROM orders WHERE delivery_address_snapshot IS NOT NULL`).Scan(&s.Total); err != nil {
		return s, err
	}
	if err := j.pool.QueryRow(ctx, ordersRemainingSQL).Scan(&s.Remaining); err != nil {
		return s, err
	}
	return s, nil
}
