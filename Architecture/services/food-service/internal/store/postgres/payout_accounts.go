package postgres

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Payout accounts (Wave 1 B2) for restaurants and delivery partners. The
// account number reaches this file only as a sealed blob, a lookup hash and
// its last four digits. Payouts are OFF: nothing reads account_sealed.

const (
	PayoutOwnerRestaurant      = "RESTAURANT"
	PayoutOwnerDeliveryPartner = "DELIVERY_PARTNER"
)

var (
	last4Pattern = regexp.MustCompile(`^[0-9]{4}$`)

	errPayoutRecordNotSealed = errors.New("payout account record must arrive sealed, with only the last four digits in clear")

	payoutVerificationStatuses = map[string]bool{"NOT_VERIFIED": true, "PENDING": true, "VERIFIED": true, "FAILED": true}
)

// PayoutAccountRecord has no plaintext account-number field by design.
type PayoutAccountRecord struct {
	HolderName         string
	AccountSealed      []byte
	KeyVersion         uint32
	AccountLookup      string
	AccountLast4       string
	IFSC               string
	VerificationStatus string
	VerificationReason string
	VerifiedName       string
	VerifiedAt         *time.Time
}

func (r PayoutAccountRecord) validate() error {
	if len(r.AccountSealed) == 0 || r.KeyVersion == 0 || r.AccountLookup == "" ||
		!last4Pattern.MatchString(r.AccountLast4) || r.IFSC == "" || r.HolderName == "" ||
		!payoutVerificationStatuses[r.VerificationStatus] {
		return errPayoutRecordNotSealed
	}
	return nil
}

// PayoutAccount is the only shape a payout account leaves the store in.
type PayoutAccount struct {
	OwnerType           string    `json:"owner_type"`
	OwnerID             uuid.UUID `json:"owner_id"`
	HolderName          string    `json:"holder_name"`
	AccountNumberMasked string    `json:"account_number_masked"`
	IFSC                string    `json:"ifsc"`
	VerificationStatus  string    `json:"verification_status"`
	VerificationReason  *string   `json:"verification_reason"`
	VerifiedName        *string   `json:"verified_name"`
	VerifiedAt          *string   `json:"verified_at"`
	CreatedAt           string    `json:"created_at"`
	UpdatedAt           string    `json:"updated_at"`
}

const payoutAccountReturning = `owner_type, owner_id, holder_name, account_last4, ifsc,
	verification_status, verification_reason, verified_name, verified_at, created_at, updated_at`

func scanPayoutAccount(row pgx.Row) (*PayoutAccount, error) {
	var p PayoutAccount
	var last4 string
	var verifiedAt *time.Time
	var created, updated time.Time
	if err := row.Scan(&p.OwnerType, &p.OwnerID, &p.HolderName, &last4, &p.IFSC,
		&p.VerificationStatus, &p.VerificationReason, &p.VerifiedName, &verifiedAt, &created, &updated); err != nil {
		return nil, err
	}
	p.AccountNumberMasked = "****" + last4
	p.VerifiedAt, p.CreatedAt, p.UpdatedAt = optionalRFC3339(verifiedAt), rfc3339(created), rfc3339(updated)
	return &p, nil
}

func upsertPayoutAccountTx(ctx context.Context, tx pgx.Tx, ownerType string, ownerID uuid.UUID, r PayoutAccountRecord) (*PayoutAccount, error) {
	return scanPayoutAccount(tx.QueryRow(ctx, `
		INSERT INTO food.payout_accounts (
			owner_type, owner_id, holder_name, account_sealed, key_version, account_lookup, account_last4, ifsc,
			verification_status, verification_reason, verified_name, verified_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (owner_type, owner_id) DO UPDATE SET
			holder_name = EXCLUDED.holder_name,
			account_sealed = EXCLUDED.account_sealed,
			key_version = EXCLUDED.key_version,
			account_lookup = EXCLUDED.account_lookup,
			account_last4 = EXCLUDED.account_last4,
			ifsc = EXCLUDED.ifsc,
			verification_status = EXCLUDED.verification_status,
			verification_reason = EXCLUDED.verification_reason,
			verified_name = EXCLUDED.verified_name,
			verified_at = EXCLUDED.verified_at
		RETURNING `+payoutAccountReturning,
		ownerType, ownerID, r.HolderName, r.AccountSealed, int64(r.KeyVersion), r.AccountLookup, r.AccountLast4, r.IFSC,
		r.VerificationStatus, emptyToNil(r.VerificationReason), emptyToNil(r.VerifiedName), r.VerifiedAt))
}

func (s *Store) UpsertRestaurantPayoutAccount(ctx context.Context, ownerUserID, restaurantID uuid.UUID, r PayoutAccountRecord) (*PayoutAccount, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := lockOwnedRestaurantTx(ctx, tx, ownerUserID, restaurantID); err != nil {
		return nil, err
	}
	out, err := upsertPayoutAccountTx(ctx, tx, PayoutOwnerRestaurant, restaurantID, r)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) GetRestaurantPayoutAccount(ctx context.Context, ownerUserID, restaurantID uuid.UUID) (*PayoutAccount, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := lockOwnedRestaurantTx(ctx, tx, ownerUserID, restaurantID); err != nil {
		return nil, err
	}
	out, err := scanPayoutAccount(tx.QueryRow(ctx, `
		SELECT `+payoutAccountReturning+`
		FROM food.payout_accounts
		WHERE owner_type = 'RESTAURANT' AND owner_id = $1
	`, restaurantID))
	if err != nil {
		return nil, err
	}
	return out, tx.Commit(ctx)
}

// UpsertDeliveryPartnerPayoutAccount writes the CALLER's own account: the
// partner is resolved from the user id, never taken from the request.
func (s *Store) UpsertDeliveryPartnerPayoutAccount(ctx context.Context, userID uuid.UUID, r PayoutAccountRecord) (*PayoutAccount, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var partnerID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM food.delivery_partners WHERE user_id = $1 FOR UPDATE`, userID).Scan(&partnerID); err != nil {
		return nil, err
	}
	out, err := upsertPayoutAccountTx(ctx, tx, PayoutOwnerDeliveryPartner, partnerID, r)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) GetDeliveryPartnerPayoutAccount(ctx context.Context, userID uuid.UUID) (*PayoutAccount, error) {
	return scanPayoutAccount(s.db.QueryRow(ctx, `
		SELECT p.owner_type, p.owner_id, p.holder_name, p.account_last4, p.ifsc,
			p.verification_status, p.verification_reason, p.verified_name, p.verified_at, p.created_at, p.updated_at
		FROM food.payout_accounts p
		JOIN food.delivery_partners dp ON dp.id = p.owner_id
		WHERE p.owner_type = 'DELIVERY_PARTNER' AND dp.user_id = $1
	`, userID))
}
