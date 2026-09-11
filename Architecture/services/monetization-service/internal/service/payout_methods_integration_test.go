//go:build integration

package service

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// 4D — bank account capture
// ---------------------------------------------------------------------------

// A well-formed bank account is validated, stored with only the last four
// digits in the clear, registered with the provider (one contact per
// creator, one fund account), and verified on the provider's validation.
func TestAddBankPayoutMethodVerifiesViaStub(t *testing.T) {
	r := newRailRig(t)
	key := bytes.Repeat([]byte{3}, 32)
	r.svc.WithBankDetailsKey(key)

	creator := uuid.New()
	t.Cleanup(func() {
		_, _ = r.pool.Exec(r.ctx, `DELETE FROM payout_methods WHERE user_id = $1`, creator)
		_, _ = r.pool.Exec(r.ctx, `DELETE FROM creator_payout_accounts WHERE user_id = $1`, creator)
	})

	m, err := r.svc.AddBankPayoutMethod(r.ctx, creator, BankPayoutMethodInput{
		HolderName: "  Asha   Creator ", AccountNumber: " 765432123456789 ", IFSC: "hdfc0000053", IsDefault: true,
	})
	if err != nil {
		t.Fatalf("AddBankPayoutMethod: %v", err)
	}
	if m.MethodType != "bank_account" || m.HolderName != "Asha Creator" || m.IFSC != "HDFC0000053" || m.AccountLast4 != "6789" || !m.IsDefault {
		t.Fatalf("method = %+v", m)
	}
	if !m.IsVerified || m.VerifiedAt == nil || m.RzpFundAccountID == nil || !strings.HasPrefix(*m.RzpFundAccountID, "fa_") {
		t.Fatalf("method not verified via the stub's validation: %+v", m)
	}
	if strings.Contains(m.DetailsEncrypted, "765432123456789") || !strings.HasPrefix(m.DetailsEncrypted, "v1:") {
		t.Fatalf("details_encrypted %q is not ciphertext", m.DetailsEncrypted)
	}

	var (
		storedEnc, storedIFSC, storedLast4, storedHolder, storedFA string
		verified                                                   bool
	)
	if err := r.pool.QueryRow(r.ctx, `
		SELECT details_encrypted, ifsc, account_last4, holder_name, rzp_fund_account_id, is_verified
		FROM payout_methods WHERE id = $1`, m.ID).
		Scan(&storedEnc, &storedIFSC, &storedLast4, &storedHolder, &storedFA, &verified); err != nil {
		t.Fatal(err)
	}
	if storedIFSC != "HDFC0000053" || storedLast4 != "6789" || storedHolder != "Asha Creator" || storedFA == "" || !verified {
		t.Fatalf("stored row: ifsc=%s last4=%s holder=%s fa=%s verified=%v", storedIFSC, storedLast4, storedHolder, storedFA, verified)
	}
	if got, err := r.svc.decryptBankAccountNumber(storedEnc); err != nil || got != "765432123456789" {
		t.Fatalf("decrypt stored number = %q, %v", got, err)
	}
	if n := countRows(r.ctx, t, r.pool, `SELECT count(*) FROM creator_payout_accounts WHERE user_id = $1`, creator); n != 1 {
		t.Fatalf("creator_payout_accounts rows = %d, want 1", n)
	}

	// A second account for the same creator reuses the contact.
	if _, err := r.svc.AddBankPayoutMethod(r.ctx, creator, BankPayoutMethodInput{HolderName: "Asha Creator", AccountNumber: "111122223333", IFSC: "SBIN0001234"}); err != nil {
		t.Fatal(err)
	}
	if n := r.stub.Contacts(); n != 1 {
		t.Fatalf("provider contacts = %d, want 1", n)
	}
	if n := r.stub.FundAccounts(); n != 2 {
		t.Fatalf("provider fund accounts = %d, want 2", n)
	}

	// The verified method passes the withdrawal path's method gate.
	if _, err := r.pool.Exec(r.ctx, `
		INSERT INTO creator_ledger (user_id, balance, lifetime_earnings, pending_payout, currency, is_frozen, created_at, updated_at)
		VALUES ($1, 50000, 50000, 0, 'INR', false, NOW() - INTERVAL '30 days', NOW())`, creator); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(r.ctx, `INSERT INTO creator_tax_profiles (user_id, tax_residency, tds_exempt, verified_at) VALUES ($1, 'IN', false, NOW())`, creator); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = r.pool.Exec(r.ctx, `DELETE FROM payout_requests WHERE user_id = $1`, creator)
		_, _ = r.pool.Exec(r.ctx, `DELETE FROM tds_ledger WHERE creator_id = $1`, creator)
		_, _ = r.pool.Exec(r.ctx, `DELETE FROM ledger_entries WHERE debit_account_id IN (SELECT id FROM accounts WHERE owner_id=$1) OR credit_account_id IN (SELECT id FROM accounts WHERE owner_id=$1)`, creator)
		_, _ = r.pool.Exec(r.ctx, `DELETE FROM accounts WHERE owner_id = $1`, creator)
		_, _ = r.pool.Exec(r.ctx, `DELETE FROM transactions WHERE wallet_id = $1`, creator)
		_, _ = r.pool.Exec(r.ctx, `DELETE FROM creator_tax_profiles WHERE user_id = $1`, creator)
		_, _ = r.pool.Exec(r.ctx, `DELETE FROM creator_ledger WHERE user_id = $1`, creator)
	})
	if _, err := r.svc.RequestPayout(r.ctx, creator, 20_000, m.ID); err != nil {
		t.Fatalf("RequestPayout with the verified bank method: %v", err)
	}
}

// Format failures never reach the store or the provider; a provider
// refusal (bad IFSC that passes the format check) undoes the row.
func TestAddBankPayoutMethodRefusals(t *testing.T) {
	r := newRailRig(t)
	r.svc.WithBankDetailsKey(bytes.Repeat([]byte{4}, 32))
	creator := uuid.New()
	t.Cleanup(func() {
		_, _ = r.pool.Exec(r.ctx, `DELETE FROM payout_methods WHERE user_id = $1`, creator)
		_, _ = r.pool.Exec(r.ctx, `DELETE FROM creator_payout_accounts WHERE user_id = $1`, creator)
	})
	cases := []struct {
		name string
		in   BankPayoutMethodInput
		want error
	}{
		{"bad ifsc", BankPayoutMethodInput{HolderName: "A B", AccountNumber: "765432123456789", IFSC: "HDFC000053"}, ErrInvalidIFSC},
		{"short account", BankPayoutMethodInput{HolderName: "A B", AccountNumber: "12345678", IFSC: "HDFC0000053"}, ErrInvalidBankAccount},
		{"letters in account", BankPayoutMethodInput{HolderName: "A B", AccountNumber: "12345678X", IFSC: "HDFC0000053"}, ErrInvalidBankAccount},
		{"no holder", BankPayoutMethodInput{HolderName: " ", AccountNumber: "765432123456789", IFSC: "HDFC0000053"}, ErrInvalidHolderName},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.svc.AddBankPayoutMethod(r.ctx, creator, tc.in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	if n := countRows(r.ctx, t, r.pool, `SELECT count(*) FROM payout_methods WHERE user_id = $1`, creator); n != 0 {
		t.Fatalf("refused inputs stored %d rows", n)
	}
	if n := r.stub.Contacts() + r.stub.FundAccounts(); n != 0 {
		t.Fatalf("refused inputs reached the provider: %d objects", n)
	}

	// Without a key, nothing is stored and the caller is told why.
	noKey := enabledPayoutService(r.store)
	if _, err := noKey.AddBankPayoutMethod(r.ctx, creator, BankPayoutMethodInput{HolderName: "A B", AccountNumber: "765432123456789", IFSC: "HDFC0000053"}); !errors.Is(err, ErrBankCaptureNotConfigured) {
		t.Fatalf("without a key: %v, want ErrBankCaptureNotConfigured", err)
	}
	if n := countRows(r.ctx, t, r.pool, `SELECT count(*) FROM payout_methods WHERE user_id = $1`, creator); n != 0 {
		t.Fatalf("keyless capture stored %d rows", n)
	}

	// Rail not configured: recorded, unverified, no provider call.
	offRail := enabledPayoutService(r.store).WithBankDetailsKey(bytes.Repeat([]byte{5}, 32))
	m, err := offRail.AddBankPayoutMethod(r.ctx, creator, BankPayoutMethodInput{HolderName: "A B", AccountNumber: "765432123456789", IFSC: "HDFC0000053"})
	if err != nil {
		t.Fatal(err)
	}
	if m.IsVerified || m.VerifiedAt != nil || m.RzpFundAccountID != nil {
		t.Fatalf("method verified with the rail off: %+v", m)
	}
}
