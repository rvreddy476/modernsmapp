package postgres

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/atpost/commerce-service/database"
)

// Migration 039 binds the system actor, action and reason that
// CorrectInvoiceTotals writes; the two must spell them identically or every
// correction is refused by the CHECK.
func TestInvoiceReissueMigrationMatchesTheStore(t *testing.T) {
	body, err := fs.ReadFile(database.Migrations, "migrations/"+InvoiceReissueMigration)
	if err != nil {
		t.Fatalf("migration %s is not embedded: %v", InvoiceReissueMigration, err)
	}
	sql := string(body)
	for _, want := range []string{
		"'" + InvoiceReissueActorID.String() + "'::uuid",
		"'" + AuditActionInvoiceReissue + "'",
		"'" + InvoiceReissueReason + "'",
		"'invoice'",
		// every value 038 allowed is still allowed
		"'cod_remittance_settle'", "'seller_kyc_verify'", "'banner_create'", "'banner_update'",
		"'banner_delete'", "'coupon_create'", "'coupon_update'",
		"'cod_remittance'", "'seller'", "'banner'", "'coupon'",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("migration %s does not contain %s", InvoiceReissueMigration, want)
		}
	}
}

func TestPaiseNumericIsExact(t *testing.T) {
	for p, want := range map[int64]string{
		0: "0.00", 5: "0.05", 100: "1.00", 382650: "3826.50", 244700: "2447.00", -1: "-0.01", -12345: "-123.45",
	} {
		if got := paiseNumeric(p); got != want {
			t.Errorf("paiseNumeric(%d) = %q, want %q", p, got, want)
		}
	}
}
