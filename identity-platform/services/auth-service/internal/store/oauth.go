// OAuth accounts: lookup by provider identity, creating users from a
// provider sign-in (plain and extended, with and without a transaction),
// and linking a provider to an existing account. Pure move out of users.go;
// behaviour is unchanged.

package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// --- OAuth ---

// GetUserByLoginProvider finds a user by their login_provider and email combination.
func (s *Store) GetUserByLoginProvider(ctx context.Context, provider, email string) (*User, error) {
	row := s.db.QueryRow(ctx,
		`SELECT `+allUserCols+` FROM auth.users WHERE login_provider = $1 AND email = $2`,
		provider, email,
	)
	return scanUser(row)
}

// CreateUserWithOAuth creates a new user with a social login provider.
//
// A5: prior to the OAuth pre-creation gate this method hard-coded
// email_verified=TRUE on the insert. Providers like GitHub (and Google
// when explicitly asserting `email_verified:false`) hand out account
// rows whose email ownership is NOT proven, so the row would silently
// be created with a verified flag the caller could not contradict.
// We now thread the verified flag through; the OAuth service layer
// decides what to pass based on the provider's claim.
func (s *Store) CreateUserWithOAuth(ctx context.Context, provider, email, name string) (*User, error) {
	return s.CreateUserWithOAuthExtended(ctx, provider, email, name, "", true, false)
}

// CreateUserWithOAuthTx creates a new user with a social login provider inside a transaction.
//
// A5: see CreateUserWithOAuth — kept the legacy signature so callers
// that don't care about the verification gate still compile, but the
// real OAuth flow now goes through CreateUserWithOAuthExtendedTx
// where the email_verified / phone_verified flags are explicit.
func (s *Store) CreateUserWithOAuthTx(ctx context.Context, tx pgx.Tx, provider, email, name string) (*User, error) {
	return s.CreateUserWithOAuthExtendedTx(ctx, tx, provider, email, name, "", true, false)
}

// CreateUserWithOAuthExtended is the A5 variant that accepts an
// optional phone and the explicit email/phone verification flags. The
// OAuth pre-creation flow uses this when an OTP-completed signup has
// proved phone ownership (phoneVerified=true) but the OAuth provider
// hadn't asserted email_verified, so the user starts life with
// email_verified=false and a stashed phone.
func (s *Store) CreateUserWithOAuthExtended(ctx context.Context, provider, email, name, phone string, emailVerified, phoneVerified bool) (*User, error) {
	id := uuid.New()
	now := time.Now()

	var emailPtr, phonePtr *string
	if email != "" {
		emailPtr = &email
	}
	if phone != "" {
		phonePtr = &phone
	}

	res, err := s.db.Exec(ctx, `
		INSERT INTO auth.users (user_id, phone, email, login_provider, email_verified, phone_verified, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
		ON CONFLICT DO NOTHING
	`, id, phonePtr, emailPtr, provider, emailVerified, phoneVerified, now)
	if err != nil {
		return nil, err
	}
	if res.RowsAffected() == 0 {
		return nil, ErrUserExists
	}
	if email != "" {
		return s.GetUserByEmail(ctx, email)
	}
	return s.GetUserByPhone(ctx, phone)
}

// CreateUserWithOAuthExtendedTx is the in-transaction variant of
// CreateUserWithOAuthExtended (see A5 doc above).
func (s *Store) CreateUserWithOAuthExtendedTx(ctx context.Context, tx pgx.Tx, provider, email, name, phone string, emailVerified, phoneVerified bool) (*User, error) {
	id := uuid.New()
	now := time.Now()

	var emailPtr, phonePtr *string
	if email != "" {
		emailPtr = &email
	}
	if phone != "" {
		phonePtr = &phone
	}

	res, err := tx.Exec(ctx, `
		INSERT INTO auth.users (user_id, phone, email, login_provider, email_verified, phone_verified, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
		ON CONFLICT DO NOTHING
	`, id, phonePtr, emailPtr, provider, emailVerified, phoneVerified, now)
	if err != nil {
		return nil, err
	}
	if res.RowsAffected() == 0 {
		return nil, ErrUserExists
	}
	var row pgx.Row
	if email != "" {
		row = tx.QueryRow(ctx, `SELECT `+allUserCols+` FROM auth.users WHERE email = $1`, email)
	} else {
		row = tx.QueryRow(ctx, `SELECT `+allUserCols+` FROM auth.users WHERE phone = $1`, phone)
	}
	return scanUser(row)
}

// LinkOAuthProvider sets the login_provider for an existing user.
func (s *Store) LinkOAuthProvider(ctx context.Context, userID uuid.UUID, provider string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE auth.users
		SET login_provider = $1, updated_at = NOW()
		WHERE user_id = $2
	`, provider, userID)
	return err
}
