// Customer addresses: the sealed (encrypted) identifying fields and the
// address create, update, delete and set-default writes. Moved out of
// store.go unchanged.

package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ─── Customer Addresses ──────────────────────────────────────

// SealedAddressWrite carries the encrypted identifying fields alongside the
// row they belong to.
//
// B4. The ciphertext and the business row are written in ONE statement, so
// there is no window in which a customer address exists without its
// ciphertext — a window a crash would turn into a permanently plaintext row
// that the backfill would then have to find.
//
// Sealing happens in the service, because it is a KMS network call and the
// store's contract is that it does no I/O beyond the database. The store's
// job is to make the write atomic.
type SealedAddressWrite struct {
	ContactName  []byte
	Phone        []byte
	AddressLine1 []byte
	AddressLine2 []byte
	Landmark     []byte
	KeyVersion   int
	LookupHash   string

	// WritePlaintext is the cutover switch (pii.Mode.WritesPlaintext).
	//
	// True during the dual-write window, so the previous image can still read
	// every row and a rollback is survivable. False after cutover, when the
	// identifying columns are written empty and ciphertext is the only copy.
	WritePlaintext bool
}

// identifying returns the plaintext to store in the identifying columns.
//
// Empty strings rather than NULL: contact_name, phone and address_line_1 are
// NOT NULL, and widening them is a contract change this pass does not make.
// "Nonblank identifying plaintext" is therefore the property the scrub
// verifies, and ” is the scrubbed state.
func (w SealedAddressWrite) identifying(
	name, phone, line1 string, line2, landmark *string,
) (string, string, string, *string, *string) {
	if w.WritePlaintext {
		return name, phone, line1, line2, landmark
	}
	// address_line_2 and landmark are nullable, so their scrubbed state is
	// NULL rather than ''. The three NOT NULL columns take ''.
	return "", "", "", nil, nil
}

// CreateAddress writes a customer address with its identifying fields sealed.
//
// B4: `sealed` is REQUIRED. There is no path here that stores a customer's
// name, phone or street in plaintext only — the previous version of this
// function had no other path, which is why every address in the database is
// currently readable by anyone holding a database credential.
func (s *Store) CreateAddress(ctx context.Context, addr *CustomerAddress, sealed SealedAddressWrite) error {
	if sealed.KeyVersion <= 0 || len(sealed.AddressLine1) == 0 {
		// A write that reached here without ciphertext would be a silent
		// plaintext row. Refusing costs the customer one retry; accepting
		// costs an address that the scrub will later clear and that nothing
		// can then recover.
		return fmt.Errorf("commerce: refusing to store an address without sealed identifying fields")
	}
	addr.ID = uuid.New()
	addr.CreatedAt = time.Now()
	name, phone, line1, line2, landmark := sealed.identifying(
		addr.ContactName, addr.Phone, addr.AddressLine1, addr.AddressLine2, addr.Landmark)

	_, err := s.db.Exec(ctx, `
		INSERT INTO customer_addresses (id,user_id,label,contact_name,phone,address_line_1,
		  address_line_2,landmark,city,state,country,postal_code,address_type,is_default,created_at,
		  contact_name_enc,phone_enc,address_line_1_enc,address_line_2_enc,landmark_enc,
		  pii_key_version,pii_scope,lookup_hash)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,
		        $16,$17,$18,$19,$20,$21,'profile',NULLIF($22,''))`,
		addr.ID, addr.UserID, addr.Label, name, phone, line1,
		line2, landmark, addr.City, addr.State, addr.Country, addr.PostalCode,
		addr.AddressType, addr.IsDefault, addr.CreatedAt,
		sealed.ContactName, sealed.Phone, sealed.AddressLine1, sealed.AddressLine2, sealed.Landmark,
		sealed.KeyVersion, sealed.LookupHash,
	)
	return err
}

// Customer addresses are READ through GetAddressRow / GetAddressRowsByUser
// (accessors_p0.go), which return the ciphertext for the service to open.

// UpdateAddress replaces an address, re-sealing its identifying fields.
//
// B4: the ciphertext columns are ALWAYS overwritten, including when the
// cutover is still writing plaintext. An update that refreshed the plaintext
// and left stale ciphertext behind would be the worst of both — the row would
// look encrypted, and what it decrypts to would be the previous occupant's
// address.
func (s *Store) UpdateAddress(ctx context.Context, id, userID uuid.UUID, addr *CustomerAddress, sealed SealedAddressWrite) error {
	if sealed.KeyVersion <= 0 || len(sealed.AddressLine1) == 0 {
		return fmt.Errorf("commerce: refusing to update an address without sealed identifying fields")
	}
	name, phone, line1, line2, landmark := sealed.identifying(
		addr.ContactName, addr.Phone, addr.AddressLine1, addr.AddressLine2, addr.Landmark)

	tag, err := s.db.Exec(ctx, `
		UPDATE customer_addresses SET
			contact_name=$3, phone=$4, address_line_1=$5, address_line_2=$6,
			landmark=$7, city=$8, state=$9, country=$10, postal_code=$11,
			address_type=$12, is_default=$13,
			contact_name_enc=$14, phone_enc=$15, address_line_1_enc=$16,
			address_line_2_enc=$17, landmark_enc=$18,
			pii_key_version=$19, lookup_hash=NULLIF($20,''),
			updated_at=NOW()
		WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`,
		id, userID, name, phone, line1, line2,
		landmark, addr.City, addr.State, addr.Country, addr.PostalCode,
		addr.AddressType, addr.IsDefault,
		sealed.ContactName, sealed.Phone, sealed.AddressLine1,
		sealed.AddressLine2, sealed.Landmark,
		sealed.KeyVersion, sealed.LookupHash,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("address not found")
	}
	return nil
}

// ErrAddressNotFound: no live address with that id belongs to the caller.
var ErrAddressNotFound = errors.New("address not found")

// DeleteAddress removes a saved address from the caller's address book.
//
// L5 (audit 2026-10-30): an address an order (or a return pickup, or an
// organisation) references cannot be removed, because those foreign keys have
// no ON DELETE. It used to fail with a raw FK error. Now the row is removed
// when nothing references it, and otherwise hidden (deleted_at): the address
// book, the default pincode, update, set-default and checkout all skip it,
// while orders keep their snapshot (and a legacy order without one still
// reaches the row). The reported result is the same either way: it is gone
// from the address book.
func (s *Store) DeleteAddress(ctx context.Context, id, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var live bool
	if err := tx.QueryRow(ctx,
		`SELECT TRUE FROM customer_addresses WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL FOR UPDATE`,
		id, userID).Scan(&live); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAddressNotFound
		}
		return err
	}

	// Try the hard delete under a savepoint, so a foreign-key refusal from
	// any referencing table leaves the transaction usable for the soft one.
	sp, err := tx.Begin(ctx)
	if err != nil {
		return err
	}
	_, err = sp.Exec(ctx, `DELETE FROM customer_addresses WHERE id=$1 AND user_id=$2`, id, userID)
	switch {
	case err == nil:
		if err := sp.Commit(ctx); err != nil {
			return err
		}
	case pgCode(err) == sqlStateForeignKeyViolation:
		if err := sp.Rollback(ctx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE customer_addresses SET deleted_at = NOW(), is_default = FALSE, updated_at = NOW()
			  WHERE id=$1 AND user_id=$2`, id, userID); err != nil {
			return err
		}
	default:
		return err
	}
	return tx.Commit(ctx)
}

// SetDefaultAddress atomically clears any existing default and sets the given address as default.
func (s *Store) SetDefaultAddress(ctx context.Context, id, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `UPDATE customer_addresses SET is_default=false WHERE user_id=$1`, userID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE customer_addresses SET is_default=true WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("address not found")
	}
	return tx.Commit(ctx)
}
