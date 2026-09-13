package piibackfill

// Seller KYC identifiers (migration 035): bank account numbers and PANs.
//
// The same invariants as the address path, against the KYC cutover's own
// progress table (`pii_kyc_backfill_progress`) so gated/1002 and gated/1000
// never read each other's claims:
//
//   - select still-unsealed rows outside any transaction;
//   - seal, then OPEN and compare to the source before committing;
//   - commit the ciphertext and the progress row in ONE transaction;
//   - record a failure durably and clear completed_at;
//   - stamp completion only after re-counting what is left.
//
// One difference, and it is deliberate. The UPDATE is guarded on the plaintext
// being unchanged since it was read. A payout account an old writer edits
// while the backfill is sealing it must not be sealed with the value it had a
// moment ago — that ciphertext would later pay the seller into the account
// they just left. A row that moved is skipped, not failed: the next batch sees
// its new value.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/atpost/commerce-service/internal/pii"
	sharedkyc "github.com/atpost/shared/kyc"
	"github.com/google/uuid"
)

// kycCandidate is one unsealed identifier, exactly as stored.
type kycCandidate struct {
	id    uuid.UUID
	plain string
	// ifsc is the hash context for a bank account; "" for a PAN.
	ifsc string
}

// kycField describes one table's single sealed identifier.
type kycField struct {
	name   string
	domain string

	selectSQL    string
	updateSQL    string
	totalSQL     string
	remainingSQL string

	// normalize is the canonical value sealed and hashed. It MUST match what
	// the service seals on write, or duplicate detection splits one account
	// into two.
	normalize func(string) string
	// hashParts is the extra lookup-hash context, matching the service.
	hashParts func(kycCandidate) []string
	// display is the clear-text form kept beside the ciphertext.
	display func(string) string

	updateArgs func(c kycCandidate, s *pii.SealedIdentifier, display string) []any
}

func noHashParts(kycCandidate) []string { return nil }

// kycFields is the KYC inventory. gated/1002 checks the same three tables.
var kycFields = []kycField{
	{
		name:   "seller_payout_accounts",
		domain: "bank_account",
		selectSQL: `SELECT id, account_number, COALESCE(ifsc_code, '')
		              FROM seller_payout_accounts
		             WHERE btrim(account_number) <> '' AND account_number_enc IS NULL
		             ORDER BY id
		             LIMIT $1`,
		updateSQL: `UPDATE seller_payout_accounts
		               SET account_number_enc=$2, pii_key_version=$3,
		                   account_number_hash=$4, account_number_last4=$5
		             WHERE id=$1 AND account_number_enc IS NULL
		               AND account_number=$6 AND COALESCE(ifsc_code, '')=$7`,
		totalSQL:     `SELECT count(*) FROM seller_payout_accounts`,
		remainingSQL: `SELECT count(*) FROM seller_payout_accounts WHERE btrim(account_number) <> '' AND account_number_enc IS NULL`,
		normalize:    strings.TrimSpace,
		// An account number is only the same account at the same bank branch.
		hashParts: func(c kycCandidate) []string {
			return []string{strings.ToUpper(strings.TrimSpace(c.ifsc))}
		},
		display: sharedkyc.BankAccountLast4,
		updateArgs: func(c kycCandidate, s *pii.SealedIdentifier, display string) []any {
			return []any{c.id, s.Enc, s.KeyVersion, s.Hash, display, c.plain, c.ifsc}
		},
	},
	{
		name:   "sellers",
		domain: "pan",
		selectSQL: `SELECT id, pan_number, ''
		              FROM sellers
		             WHERE btrim(COALESCE(pan_number, '')) <> '' AND pan_enc IS NULL
		             ORDER BY id
		             LIMIT $1`,
		updateSQL: `UPDATE sellers
		               SET pan_enc=$2, pan_key_version=$3, pan_hash=$4, pan_masked=$5
		             WHERE id=$1 AND pan_enc IS NULL AND pan_number=$6`,
		totalSQL:     `SELECT count(*) FROM sellers`,
		remainingSQL: `SELECT count(*) FROM sellers WHERE btrim(COALESCE(pan_number, '')) <> '' AND pan_enc IS NULL`,
		normalize:    pii.NormalizePAN,
		hashParts:    noHashParts,
		display:      pii.MaskPAN,
		updateArgs: func(c kycCandidate, s *pii.SealedIdentifier, display string) []any {
			return []any{c.id, s.Enc, s.KeyVersion, s.Hash, display, c.plain}
		},
	},
	{
		name:   "organizations",
		domain: "pan",
		selectSQL: `SELECT id, pan, ''
		              FROM organizations
		             WHERE btrim(COALESCE(pan, '')) <> '' AND pan_enc IS NULL
		             ORDER BY id
		             LIMIT $1`,
		updateSQL: `UPDATE organizations
		               SET pan_enc=$2, pan_key_version=$3, pan_hash=$4, pan_masked=$5
		             WHERE id=$1 AND pan_enc IS NULL AND pan=$6`,
		totalSQL:     `SELECT count(*) FROM organizations`,
		remainingSQL: `SELECT count(*) FROM organizations WHERE btrim(COALESCE(pan, '')) <> '' AND pan_enc IS NULL`,
		normalize:    pii.NormalizePAN,
		hashParts:    noHashParts,
		display:      pii.MaskPAN,
		updateArgs: func(c kycCandidate, s *pii.SealedIdentifier, display string) []any {
			return []any{c.id, s.Enc, s.KeyVersion, s.Hash, display, c.plain}
		},
	},
}

// KYCTables names the KYC inventory, for operators and contract tests.
func KYCTables() []string {
	out := make([]string, 0, len(kycFields))
	for _, f := range kycFields {
		out = append(out, f.name)
	}
	return out
}

// errVerifyMismatch means a ciphertext opened, but not to the value sealed.
var errVerifyMismatch = errors.New("decrypt did not reproduce the source")

// verifySealed opens enc and requires it to equal want exactly.
func verifySealed(ctx context.Context, cipher *pii.Cipher, enc []byte, want string) error {
	opened, err := cipher.OpenIdentifier(ctx, pii.ScopeKYC, enc)
	if err != nil {
		return fmt.Errorf("verifying: %w", err)
	}
	if opened != want {
		return errVerifyMismatch
	}
	return nil
}

// errRowMoved means the row changed between being read and being updated.
var errRowMoved = errors.New("piibackfill: row changed under the backfill")

// RunKYC backfills every seller-KYC identifier until each table reports nothing
// left.
func (j *Job) RunKYC(ctx context.Context) ([]Stats, error) {
	release, err := j.acquireLock(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	var out []Stats
	for _, f := range kycFields {
		s, err := j.runKYCField(ctx, f)
		if err != nil {
			return out, fmt.Errorf("piibackfill: kyc %s: %w", f.name, err)
		}
		out = append(out, s)
	}
	return out, nil
}

// KYCStats reports every KYC table's counters without doing any work.
func (j *Job) KYCStats(ctx context.Context) ([]Stats, error) {
	var out []Stats
	for _, f := range kycFields {
		if err := j.ensureProgressRow(ctx, kycProgress, f.name); err != nil {
			return nil, err
		}
		s, err := j.statsIn(ctx, kycProgress, f.name, f.totalSQL, f.remainingSQL)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func (j *Job) runKYCField(ctx context.Context, f kycField) (Stats, error) {
	if err := j.ensureProgressRow(ctx, kycProgress, f.name); err != nil {
		return Stats{}, err
	}
	for {
		select {
		case <-ctx.Done():
			return j.statsIn(ctx, kycProgress, f.name, f.totalSQL, f.remainingSQL)
		default:
		}
		done, err := j.kycBatch(ctx, f)
		if err != nil {
			return Stats{}, err
		}
		if done {
			break
		}
	}
	if err := j.markCompleteIn(ctx, kycProgress, f.name, f.remainingSQL); err != nil {
		return Stats{}, err
	}
	return j.statsIn(ctx, kycProgress, f.name, f.totalSQL, f.remainingSQL)
}

// kycBatch seals one bounded set of rows. Returns true when nothing remains.
func (j *Job) kycBatch(ctx context.Context, f kycField) (bool, error) {
	rows, err := j.pool.Query(ctx, f.selectSQL, j.BatchSize)
	if err != nil {
		return false, err
	}
	var candidates []kycCandidate
	for rows.Next() {
		var c kycCandidate
		if err := rows.Scan(&c.id, &c.plain, &c.ifsc); err != nil {
			rows.Close()
			return false, err
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	if len(candidates) == 0 {
		return true, nil
	}

	sealed := 0
	for _, c := range candidates {
		err := j.sealKYCOne(ctx, f, c)
		if errors.Is(err, errRowMoved) {
			continue
		}
		if err != nil {
			// Durable, and the cursor does not advance past it.
			if mErr := j.recordFailure(ctx, kycProgress, f.name, c.id, err); mErr != nil {
				return false, fmt.Errorf("%w (and recording it failed: %v)", err, mErr)
			}
			return false, err
		}
		sealed++
	}
	if sealed == 0 {
		// Every row in the batch moved under us. Stop rather than spin; a
		// re-run picks up the settled values.
		return false, errors.New("piibackfill: every row in a KYC batch changed while it was being sealed; re-run")
	}
	return false, nil
}

// sealKYCOne encrypts, verifies and commits one identifier with its progress.
func (j *Job) sealKYCOne(ctx context.Context, f kycField, c kycCandidate) error {
	value := f.normalize(c.plain)
	if value == "" {
		return fmt.Errorf("row %s: the stored value normalises to nothing", c.id)
	}
	sealed, err := j.cipher.SealIdentifier(ctx, pii.ScopeKYC, f.domain, value, f.hashParts(c)...)
	if err != nil {
		return fmt.Errorf("sealing row %s: %w", c.id, err)
	}

	// VERIFY before committing. gated/1002 clears the plaintext, and a seal
	// that produced garbage would otherwise be found only when a seller could
	// not be paid.
	if err := verifySealed(ctx, j.cipher, sealed.Enc, value); err != nil {
		return fmt.Errorf("row %s: %w", c.id, err)
	}

	tx, err := j.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tag, err := tx.Exec(ctx, f.updateSQL, f.updateArgs(c, sealed, f.display(value))...)
	if err != nil {
		return err
	}
	switch tag.RowsAffected() {
	case 1:
	case 0:
		return errRowMoved
	default:
		return fmt.Errorf("row %s: %d rows updated, want 1", c.id, tag.RowsAffected())
	}

	// THE invariant: progress advances in the same transaction as the
	// ciphertext it describes.
	if _, err := tx.Exec(ctx, fmt.Sprintf(`
		UPDATE %s
		   SET last_id        = $2,
		       encrypted_rows = encrypted_rows + 1,
		       verified       = verified + 1,
		       updated_at     = NOW()
		 WHERE table_name = $1`, kycProgress), f.name, c.id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
