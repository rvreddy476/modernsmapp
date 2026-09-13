package postgres

// B4 — the inventory guard: every address writer must seal.
//
// The cutover's whole claim is "no identifying address field is stored in
// plaintext after cutover". That claim is only as good as the list of writers,
// and a list maintained by memory is a list that will be wrong the first time
// somebody adds an endpoint.
//
// So this reads the store's own source and asserts that every statement which
// writes an identifying address column also writes its ciphertext. It is a
// text check, and text checks are usually weak — but the thing it protects
// against is precisely a new INSERT that nobody thought to route through the
// cipher, which no type system here would catch.
//
// It is deliberately narrow: it looks only at the three identifying columns
// that the gated scrub will later clear. A writer that touches those and not
// their `_enc` counterparts is writing PII that the scrub will destroy.

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// identifyingColumns are the ones the gated scrub clears. Anything that writes
// them without ciphertext is writing data that will not survive the cutover.
var identifyingColumns = []string{"contact_name", "phone", "address_line_1"}

// addressTables are the tables those columns live on.
var addressTables = []string{"customer_addresses", "seller_addresses"}

var (
	insertStmt = regexp.MustCompile(`(?is)INSERT\s+INTO\s+(customer_addresses|seller_addresses)\b.*?VALUES`)
	updateStmt = regexp.MustCompile(`(?is)UPDATE\s+(customer_addresses|seller_addresses)\s+SET.*?(?:WHERE|$)`)
)

func storeSources(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = string(b)
	}
	return out
}

// writesIdentifying reports whether a statement assigns any identifying column.
func writesIdentifying(stmt string) bool {
	lower := strings.ToLower(stmt)
	for _, c := range identifyingColumns {
		// `contact_name=` or `contact_name,` in a column list — but not
		// `contact_name_enc`, which is the ciphertext.
		for _, form := range []string{c + "=", c + " =", c + ","} {
			idx := strings.Index(lower, form)
			for idx >= 0 {
				if !strings.HasPrefix(lower[idx:], c+"_enc") {
					return true
				}
				next := strings.Index(lower[idx+1:], form)
				if next < 0 {
					break
				}
				idx = idx + 1 + next
			}
		}
	}
	return false
}

func writesCiphertext(stmt string) bool {
	lower := strings.ToLower(stmt)
	for _, c := range identifyingColumns {
		if strings.Contains(lower, c+"_enc") {
			return true
		}
	}
	return false
}

// THE guard. A writer that stores a name, phone or street without also storing
// its ciphertext is a writer the cutover does not cover.
func TestEveryAddressWriterAlsoWritesCiphertext(t *testing.T) {
	for file, body := range storeSources(t) {
		for _, re := range []*regexp.Regexp{insertStmt, updateStmt} {
			for _, stmt := range re.FindAllString(body, -1) {
				if !writesIdentifying(stmt) {
					continue
				}
				if !writesCiphertext(stmt) {
					t.Fatalf("%s contains a statement that writes identifying address plaintext "+
						"without ciphertext:\n\n%s\n\n"+
						"Every client-reachable address write must seal. After the gated scrub "+
						"clears the plaintext, a row written by this statement has no address "+
						"at all.", file, strings.TrimSpace(stmt))
				}
			}
		}
	}
}

// ─── Seller KYC identifiers (migration 035) ──────────────────────────
//
// Same guard, for the KYC cutover: every statement that writes a bank account
// number or a PAN must also write its ciphertext, because gated/1002 clears the
// plaintext and a writer that skipped the seal leaves a seller with no payout
// account at all.

type kycColumn struct{ plain, enc string }

// kycIdentifying is keyed by table. The column names differ per table, so the
// match is a word match — `\bpan\b` must not fire on `pan_masked`.
var kycIdentifying = map[string]kycColumn{
	"seller_payout_accounts": {plain: "account_number", enc: "account_number_enc"},
	"sellers":                {plain: "pan_number", enc: "pan_enc"},
	"organizations":          {plain: "pan", enc: "pan_enc"},
}

var (
	kycInsertStmt = regexp.MustCompile(`(?is)INSERT\s+INTO\s+(seller_payout_accounts|sellers|organizations)\b.*?VALUES`)
	kycUpdateStmt = regexp.MustCompile(`(?is)UPDATE\s+(seller_payout_accounts|sellers|organizations)\s+SET.*?(?:WHERE|$)`)
)

func mentionsColumn(stmt, col string) bool {
	return regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(col) + `\b`).MatchString(stmt)
}

func TestEveryKYCIdentifierWriterAlsoWritesCiphertext(t *testing.T) {
	for file, body := range storeSources(t) {
		for _, re := range []*regexp.Regexp{kycInsertStmt, kycUpdateStmt} {
			for _, m := range re.FindAllStringSubmatch(body, -1) {
				stmt, table := m[0], strings.ToLower(m[1])
				col := kycIdentifying[table]
				if !mentionsColumn(stmt, col.plain) {
					continue
				}
				if !mentionsColumn(stmt, col.enc) {
					t.Fatalf("%s writes %s.%s without %s:\n\n%s\n\n"+
						"Every writer of a bank account number or PAN must seal it. gated/1002 clears "+
						"the plaintext, and a row written by this statement would be left with nothing.",
						file, table, col.plain, col.enc, strings.TrimSpace(stmt))
				}
			}
		}
	}
}

func TestTheKYCGuardCoversEveryKYCTable(t *testing.T) {
	body := strings.Join([]string{kycInsertStmt.String(), kycUpdateStmt.String()}, " ")
	for tbl := range kycIdentifying {
		if !strings.Contains(body, tbl) {
			t.Fatalf("%s is not covered by the KYC writer-inventory patterns", tbl)
		}
	}
}

// The tables the guard covers must be the tables the backfill covers. A table
// added to one and not the other is a hole with no symptom until the scrub.
func TestTheGuardCoversEveryAddressTable(t *testing.T) {
	body := strings.Join([]string{insertStmt.String(), updateStmt.String()}, " ")
	for _, tbl := range addressTables {
		if !strings.Contains(body, tbl) {
			t.Fatalf("%s is not covered by the writer-inventory patterns", tbl)
		}
	}
}
