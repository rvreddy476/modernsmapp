//go:build integration

package postgres

// Migration 035 — seller KYC identifiers at rest, against a live PostgreSQL.
//
//	COMMERCE_TEST_DSN=.../commerce_it_test go test -tags=integration ./internal/store/postgres/ -run KYC -v

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/atpost/commerce-service/internal/kyc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func sealedPayout(tag string, plaintext bool) SealedPayoutWrite {
	return SealedPayoutWrite{
		AccountNumberEnc: []byte("enc:" + tag),
		KeyVersion:       1,
		Hash:             "hash:" + tag,
		Last4:            "9012",
		WritePlaintext:   plaintext,
	}
}

func payoutInput(number string) OnboardingPayoutInput {
	ifsc := "HDFC0001234"
	return OnboardingPayoutInput{AccountHolderName: "KYC Test", AccountNumber: number, IFSCCode: &ifsc}
}

type payoutRow struct {
	plain       string
	enc         []byte
	version     *int
	hash, last4 *string
}

func readPayout(t *testing.T, sellerID uuid.UUID) payoutRow {
	t.Helper()
	var r payoutRow
	if err := testPool.QueryRow(context.Background(), `
		SELECT account_number, account_number_enc, pii_key_version, account_number_hash, account_number_last4
		  FROM seller_payout_accounts WHERE seller_id=$1 AND is_primary`, sellerID).
		Scan(&r.plain, &r.enc, &r.version, &r.hash, &r.last4); err != nil {
		t.Fatal(err)
	}
	return r
}

// ─── The store writes ciphertext, and plaintext only in dual mode ─────

func TestKYCPayoutAccountIsStoredSealed(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	dual := newSeller(t)
	if err := store.SaveOnboardingPayout(ctx, dual, payoutInput("123456789012"), sealedPayout("dual", true)); err != nil {
		t.Fatalf("SaveOnboardingPayout (dual): %v", err)
	}
	r := readPayout(t, dual)
	if r.plain != "123456789012" || string(r.enc) != "enc:dual" || r.version == nil || *r.version != 1 ||
		r.hash == nil || *r.hash != "hash:dual" || r.last4 == nil || *r.last4 != "9012" {
		t.Fatalf("dual-mode row = %+v", r)
	}

	cut := newSeller(t)
	if err := store.SaveOnboardingPayout(ctx, cut, payoutInput("123456789012"), sealedPayout("cut", false)); err != nil {
		t.Fatalf("SaveOnboardingPayout (ciphertext): %v", err)
	}
	if r := readPayout(t, cut); r.plain != "" || string(r.enc) != "enc:cut" {
		t.Fatalf("ciphertext-mode row kept plaintext %q (enc %q); after cutover nothing may write it", r.plain, r.enc)
	}

	got, err := store.GetPrimaryPayoutAccount(ctx, cut)
	if err != nil || got == nil || string(got.AccountNumberEnc) != "enc:cut" || got.AccountNumber != "" {
		t.Fatalf("GetPrimaryPayoutAccount = %+v, %v; want the ciphertext back", got, err)
	}
}

func TestKYCPayoutAccountIsRefusedWithoutCiphertext(t *testing.T) {
	store := New(testPool)
	sellerID := newSeller(t)
	if err := store.SaveOnboardingPayout(context.Background(), sellerID, payoutInput("123456789012"),
		SealedPayoutWrite{WritePlaintext: true}); err == nil {
		t.Fatal("a payout account was stored with no ciphertext; gated/1002 would leave the seller unpayable")
	}
}

// After the cutover the plaintext is ”, and readiness must still see the account.
func TestKYCReadinessCountsASealedOnlyPayoutAccount(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	sellerID := newSeller(t)
	if err := store.SaveOnboardingPayout(ctx, sellerID, payoutInput("123456789012"), sealedPayout("ready", false)); err != nil {
		t.Fatal(err)
	}
	r, err := store.SellerReadinessFor(ctx, sellerID)
	if err != nil {
		t.Fatal(err)
	}
	if !r.HasPayoutAccount {
		t.Fatal("a seller whose account is sealed (plaintext '') was told their bank account is missing")
	}
}

// ─── Stale ciphertext: an old writer must not leave the wrong account sealed ──

// oldImagePayoutUpsert is SaveOnboardingPayout as it was BEFORE migration 035 —
// the statement a not-yet-replaced pod runs during the dual-write window.
const oldImagePayoutUpsert = `
	INSERT INTO seller_payout_accounts
	  (id, seller_id, account_holder_name, bank_name, account_number, ifsc_code, upi_id, is_primary, created_at, updated_at)
	VALUES (gen_random_uuid(),$1,$2,$3,$4,$5,$6,TRUE,NOW(),NOW())
	ON CONFLICT (seller_id) WHERE is_primary=TRUE DO UPDATE SET
	  account_holder_name=EXCLUDED.account_holder_name, bank_name=EXCLUDED.bank_name,
	  account_number=EXCLUDED.account_number, ifsc_code=EXCLUDED.ifsc_code,
	  upi_id=EXCLUDED.upi_id, updated_at=NOW()`

func TestKYCAnOldWritersAccountChangeClearsTheStaleCiphertext(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	sellerID := newSeller(t)
	if err := store.SaveOnboardingPayout(ctx, sellerID, payoutInput("123456789012"), sealedPayout("old-account", true)); err != nil {
		t.Fatal(err)
	}

	// The seller moves bank accounts through an OLD pod.
	mustExec(t, oldImagePayoutUpsert, sellerID, "KYC Test", nil, "999988887777", "HDFC0001234", nil)

	r := readPayout(t, sellerID)
	if r.enc != nil {
		t.Fatalf("the old account's ciphertext survived a plaintext change (%q); after the cutover the "+
			"seller would be paid into the account they left", r.enc)
	}
	if r.version != nil || r.hash != nil {
		t.Fatalf("stale key version / hash survived: %+v", r)
	}
	if r.last4 == nil || *r.last4 != "7777" {
		t.Fatalf("last4 = %v, want the new account's 7777", r.last4)
	}

	// Same account, different branch: the hash (and the seal) are stale too.
	if err := store.SaveOnboardingPayout(ctx, sellerID, payoutInput("999988887777"), sealedPayout("resealed", true)); err != nil {
		t.Fatal(err)
	}
	mustExec(t, `UPDATE seller_payout_accounts SET ifsc_code='ICIC0000001' WHERE seller_id=$1`, sellerID)
	if r := readPayout(t, sellerID); r.enc != nil {
		t.Fatal("an IFSC change by an old writer kept a ciphertext and hash bound to the old branch")
	}
}

// The blanking write — the new image in ciphertext mode, and gated/1002 — must
// never clear ciphertext. If it did, the scrub would destroy every account.
func TestKYCBlankingThePlaintextKeepsTheCiphertext(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	sellerID := newSeller(t)
	if err := store.SaveOnboardingPayout(ctx, sellerID, payoutInput("123456789012"), sealedPayout("keep", true)); err != nil {
		t.Fatal(err)
	}
	mustExec(t, `UPDATE seller_payout_accounts SET account_number='' WHERE seller_id=$1`, sellerID)
	if r := readPayout(t, sellerID); string(r.enc) != "enc:keep" || r.version == nil {
		t.Fatalf("blanking the plaintext cleared the ciphertext: %+v", r)
	}
	// And a new-image re-save (fresh ciphertext) is not mistaken for stale.
	if err := store.SaveOnboardingPayout(ctx, sellerID, payoutInput("123456789012"), sealedPayout("fresh", true)); err != nil {
		t.Fatal(err)
	}
	if r := readPayout(t, sellerID); string(r.enc) != "enc:fresh" {
		t.Fatalf("a new-image write lost its own ciphertext to the trigger: %q", r.enc)
	}
}

func TestKYCAnOldWritersPANChangeClearsTheStaleCiphertext(t *testing.T) {
	ctx := context.Background()
	sellerID := newSeller(t)
	mustExec(t, `UPDATE sellers SET pan_number='ABCDE1234F', pan_enc='enc:old', pan_key_version=1,
	                    pan_hash='h', pan_masked='XXXXXX234F' WHERE id=$1`, sellerID)
	mustExec(t, `UPDATE sellers SET pan_number='PQRSX6789Z' WHERE id=$1`, sellerID)

	var enc []byte
	var masked *string
	if err := testPool.QueryRow(ctx, `SELECT pan_enc, pan_masked FROM sellers WHERE id=$1`, sellerID).
		Scan(&enc, &masked); err != nil {
		t.Fatal(err)
	}
	if enc != nil || masked == nil || *masked != "XXXXXX789Z" {
		t.Fatalf("after a plaintext-only PAN change: pan_enc=%q pan_masked=%v; want NULL and XXXXXX789Z", enc, masked)
	}
}

// ─── Aadhaar ─────────────────────────────────────────────────────────

func aadhaarShaped(t *testing.T) string {
	t.Helper()
	for d := '0'; d <= '9'; d++ {
		if v := "23456789012" + string(d); kyc.LooksLikeAadhaar(v) {
			return v
		}
	}
	t.Fatal("no Aadhaar-shaped value found")
	return ""
}

func insertDocument(sellerID uuid.UUID, typ string, number *string) error {
	_, err := testPool.Exec(context.Background(), `
		INSERT INTO seller_documents (seller_id, document_type, document_number, media_id)
		VALUES ($1,$2,$3,$4)`, sellerID, typ, number, uuid.New())
	return err
}

func isKYCCheckViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514"
}

// The database floor under the service's 400: every writer, old images included.
func TestKYCTheDatabaseRefusesARawAadhaarNumber(t *testing.T) {
	number := aadhaarShaped(t)
	s := func(v string) *string { return &v }

	if err := insertDocument(newSeller(t), "aadhaar", s(number)); !isKYCCheckViolation(err) {
		t.Fatalf("an aadhaar document with its number was stored (err = %v)", err)
	}
	if err := insertDocument(newSeller(t), "aadhaar", s("XXXX XXXX 1234")); !isKYCCheckViolation(err) {
		t.Fatalf("the aadhaar type stored a number at all (err = %v); it must store a reference only", err)
	}
	if err := insertDocument(newSeller(t), "other", s(number[:4]+"-"+number[4:8]+"-"+number[8:])); !isKYCCheckViolation(err) {
		t.Fatalf("an Aadhaar number typed into a free-text document was stored (err = %v)", err)
	}
	if err := insertDocument(newSeller(t), "aadhaar", nil); err != nil {
		t.Fatalf("an aadhaar document with only its upload reference was refused: %v", err)
	}
	if err := insertDocument(newSeller(t), "cancelled_cheque", s(number)); err != nil {
		t.Fatalf("a cancelled cheque's 12-digit account number was refused as Aadhaar: %v", err)
	}

	// An update cannot smuggle one in either.
	sellerID := newSeller(t)
	if err := insertDocument(sellerID, "other", s("REF-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(),
		`UPDATE seller_documents SET document_number=$2 WHERE seller_id=$1`, sellerID, number); !isKYCCheckViolation(err) {
		t.Fatalf("an UPDATE stored an Aadhaar number (err = %v)", err)
	}
}

// The SQL twin must agree with kyc.LooksLikeAadhaar on every input, or the
// service and the database disagree about what may be stored.
func TestKYCSQLAndGoAgreeOnWhatLooksLikeAadhaar(t *testing.T) {
	rng := rand.New(rand.NewSource(35))
	digits := func(n int) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			b.WriteByte(byte('0' + rng.Intn(10)))
		}
		return b.String()
	}
	var inputs []string
	for i := 0; i < 3000; i++ {
		v := digits(12)
		inputs = append(inputs, v)
		if i%5 == 0 {
			inputs = append(inputs, v[:4]+" "+v[4:8]+" "+v[8:], v[:4]+"-"+v[4:8]+"-"+v[8:])
		}
	}
	inputs = append(inputs, digits(11), digits(13), "ABCDE1234F", "", "2345 6789 01x2", "\t"+digits(12))

	rows, err := testPool.Query(context.Background(),
		`SELECT v, commerce_looks_like_aadhaar(v) FROM unnest($1::text[]) AS v`, inputs)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	checked, valid := 0, 0
	for rows.Next() {
		var v string
		var sqlSays bool
		if err := rows.Scan(&v, &sqlSays); err != nil {
			t.Fatal(err)
		}
		goSays := kyc.LooksLikeAadhaar(v)
		if sqlSays != goSays {
			t.Fatalf("commerce_looks_like_aadhaar(%q) = %t but kyc.LooksLikeAadhaar = %t", v, sqlSays, goSays)
		}
		checked++
		if goSays {
			valid++
		}
	}
	if checked != len(inputs) || valid < 100 {
		t.Fatalf("checked %d of %d inputs, %d valid; the comparison did not exercise both answers",
			checked, len(inputs), valid)
	}
}

// ─── Organizations ───────────────────────────────────────────────────

func TestKYCOrganizationPANIsSealedAndPatchedAsAGroup(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	name := fmt.Sprintf("KYC Org %s", uuid.NewString()[:8])

	org := &Organization{Name: name}
	pan := SealedIdentifierWrite{Supplied: true, Plain: "ABCDE1234F", Enc: []byte("enc:pan"),
		KeyVersion: 1, Hash: "h:pan", Masked: "XXXXXX234F", WritePlaintext: false}
	if err := store.CreateOrganization(ctx, org, uuid.New(), pan); err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	if org.PANMasked == nil || *org.PANMasked != "XXXXXX234F" || org.PAN != nil {
		t.Fatalf("created org carries PAN=%v masked=%v; want only the mask", org.PAN, org.PANMasked)
	}

	read := func() (plain *string, enc []byte, masked *string) {
		t.Helper()
		if err := testPool.QueryRow(ctx, `SELECT pan, pan_enc, pan_masked FROM organizations WHERE id=$1`, org.ID).
			Scan(&plain, &enc, &masked); err != nil {
			t.Fatal(err)
		}
		return
	}
	if p, e, _ := read(); p != nil || string(e) != "enc:pan" {
		t.Fatalf("ciphertext-mode org stored pan=%v enc=%q", p, e)
	}

	// A patch that does not mention the PAN leaves the whole group alone.
	newName := name + " renamed"
	if err := store.UpdateOrganization(ctx, org.ID, &Organization{Name: newName}, SealedIdentifierWrite{}); err != nil {
		t.Fatal(err)
	}
	if _, e, m := read(); string(e) != "enc:pan" || m == nil || *m != "XXXXXX234F" {
		t.Fatalf("an unrelated patch disturbed the PAN: enc=%q masked=%v", e, m)
	}

	// Supplying an empty PAN clears the group, together.
	if err := store.UpdateOrganization(ctx, org.ID, &Organization{}, SealedIdentifierWrite{Supplied: true}); err != nil {
		t.Fatal(err)
	}
	if p, e, m := read(); p != nil || e != nil || m != nil {
		t.Fatalf("clearing the PAN left pan=%v enc=%q masked=%v", p, e, m)
	}

	// And a PAN without ciphertext is refused outright.
	if err := store.UpdateOrganization(ctx, org.ID, &Organization{},
		SealedIdentifierWrite{Supplied: true, Plain: "ABCDE1234F", WritePlaintext: true}); err == nil {
		t.Fatal("a PAN was accepted without its ciphertext")
	}
}

// Full seller reads return the sealed PAN and its mask, and nothing serialises
// the full PAN.
func TestKYCSellerReadsCarryTheSealedPANAndItsMask(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	sellerID := newSeller(t)
	mustExec(t, `UPDATE sellers SET pan_enc='enc:seller-pan', pan_key_version=1, pan_masked='XXXXXX234F' WHERE id=$1`, sellerID)

	sel, err := store.GetSellerByID(ctx, sellerID)
	if err != nil {
		t.Fatal(err)
	}
	if string(sel.PANEnc) != "enc:seller-pan" || sel.PANMasked == nil || *sel.PANMasked != "XXXXXX234F" {
		t.Fatalf("GetSellerByID: PANEnc=%q PANMasked=%v", sel.PANEnc, sel.PANMasked)
	}
}
