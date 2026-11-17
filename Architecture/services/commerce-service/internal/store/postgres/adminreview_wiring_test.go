package postgres

// A source check, because the store has no seam to fake a database through:
// every admin decision in this package must go through the guarded UPDATE
// (moveSeller / moveProduct) with its OWN action, and nothing else in the
// package may write a seller's status or a product's approval_status with a
// bare `WHERE id = $1`.
//
// It reads the package's own files. What the statements do against real rows
// is adminreview_integration_test.go.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// storeFuncBody returns the source of `func (s *Store) name(` up to the next
// top-level func.
func storeFuncBody(t *testing.T, src, name string) string {
	t.Helper()
	start := strings.Index(src, "\nfunc (s *Store) "+name+"(")
	if start < 0 {
		t.Fatalf("func (s *Store) %s not found in onboarding.go", name)
	}
	rest := src[start+1:]
	if end := strings.Index(rest, "\nfunc "); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

func TestEveryAdminDecisionRunsTheGuardedUpdateWithItsOwnAction(t *testing.T) {
	raw, err := os.ReadFile("onboarding.go")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(raw), "\r\n", "\n")

	want := map[string]string{
		"ApproveSellerByAdmin":         "moveSeller(ctx, tx, SellerActionApprove, sellerID,",
		"RejectSellerByAdmin":          "moveSeller(ctx, tx, SellerActionReject, sellerID,",
		"RequestSellerChanges":         "moveSeller(ctx, tx, SellerActionRequestChanges, sellerID,",
		"SuspendSellerByAdmin":         "moveSeller(ctx, tx, SellerActionSuspend, sellerID,",
		"UnsuspendSellerByAdmin":       "moveSeller(ctx, tx, SellerActionUnsuspend, sellerID,",
		"approveProduct":               "moveProduct(ctx, tx, ProductActionApprove, productID,",
		"RejectProductByAdmin":         "moveProduct(ctx, tx, ProductActionReject, productID,",
		"RequestProductChangesByAdmin": "moveProduct(ctx, tx, ProductActionRequestChanges, productID,",
	}
	for fn, call := range want {
		body := storeFuncBody(t, src, fn)
		if n := strings.Count(body, call); n != 1 {
			t.Errorf("%s calls %q %d times, want once", fn, call, n)
		}
		if n := strings.Count(body, "moveSeller(") + strings.Count(body, "moveProduct("); n != 1 {
			t.Errorf("%s makes %d guarded moves, want exactly its own", fn, n)
		}
		if strings.Contains(body, "UPDATE sellers") || strings.Contains(body, "UPDATE products") {
			t.Errorf("%s writes sellers or products outside the guarded UPDATE", fn)
		}
	}
	// Both approval entry points are the one guarded write.
	for _, fn := range []string{"ApproveProductByAdmin", "AutoApproveProduct"} {
		if body := storeFuncBody(t, src, fn); !strings.Contains(body, "s.approveProduct(ctx, productID,") {
			t.Errorf("%s does not go through approveProduct", fn)
		}
	}
}

func TestNoOtherStatementWritesAReviewStateUnguarded(t *testing.T) {
	sellerWrite := regexp.MustCompile("UPDATE sellers\\s+SET[^`]*?\\bstatus\\s*=\\s*'(\\w+)'")
	productWrite := regexp.MustCompile("UPDATE products\\s+SET[^`]*?\\bapproval_status\\s*=\\s*'(\\w+)'")

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	sellerSubmits, productSubmits := 0, 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		// Comment lines quote the old statements; only code counts.
		var code []string
		for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "//") {
				code = append(code, line)
			}
		}
		src := strings.Join(code, "\n")
		for _, m := range sellerWrite.FindAllStringSubmatch(src, -1) {
			// The seller's own submit, guarded on status='draft'.
			if m[1] == "submitted" && f == "onboarding.go" {
				sellerSubmits++
				continue
			}
			t.Errorf("%s writes sellers.status=%q in its own UPDATE: an admin decision goes through moveSeller", f, m[1])
		}
		for _, m := range productWrite.FindAllStringSubmatch(src, -1) {
			// The seller's submit and the revalidation bounce of an edit.
			if m[1] == "submitted" && (f == "onboarding.go" || f == "productwrite.go") {
				productSubmits++
				continue
			}
			t.Errorf("%s writes products.approval_status=%q in its own UPDATE: a moderation decision goes through moveProduct", f, m[1])
		}
	}
	if sellerSubmits != 1 || productSubmits != 2 {
		t.Errorf("found %d seller and %d product submit statements, want 1 and 2: "+
			"the scan no longer matches how the package writes these columns", sellerSubmits, productSubmits)
	}
}
