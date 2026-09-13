package postgres

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// orderStatusWriterAllowlist names the only places allowed to write
// food.orders.status with raw SQL. Everything else must go through
// transitionOrderTx, which validates the edge + actor, guards on the current
// status, and writes history in the same transaction.
//
// Keys are paths relative to internal/, optionally suffixed "#FuncName" to
// allow a single function rather than a whole file.
var orderStatusWriterAllowlist = map[string]string{
	// The one guarded writer.
	"store/postgres/order_transition.go": "transitionOrderTx is the guarded writer",
	// Payment writers (CreatePaymentIntent COD path, ConfirmPayment) belong to
	// the payments stream, which routes them through the guard next.
	"store/postgres/tracking_payments.go": "payment writers; owned by the payments stream",
	// AdminRefundOrder jumps any status to REFUNDED. Left untouched on purpose:
	// the payments stream re-routes it through REFUND_PENDING.
	"store/postgres/ops.go#AdminRefundOrder": "refund writer; owned by the payments stream",
}

var (
	updateOrdersRe = regexp.MustCompile(`(?is)\bUPDATE\s+food\.orders\b(?:\s+(?:AS\s+)?[a-z_]+)?\s+SET\b(.*)`)
	whereRe        = regexp.MustCompile(`(?is)\bWHERE\b`)
	// status = ... but not payment_status = ...; o.status = counts.
	statusAssignRe = regexp.MustCompile(`(?i)(?:^|[\s,(.])status\s*=`)
)

// TestNoRawOrderStatusUpdates fails when any non-test Go file under internal/
// contains a SQL literal that UPDATEs food.orders and assigns status outside
// the allowlist above.
func TestNoRawOrderStatusUpdates(t *testing.T) {
	root := filepath.Join("..", "..") // internal/
	fset := token.NewFileSet()
	allowedHits := 0
	var offenders []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		check := func(funcName string, n ast.Node) {
			ast.Inspect(n, func(node ast.Node) bool {
				lit, ok := node.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				sql, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				if !writesOrderStatus(sql) {
					return true
				}
				_, fileOK := orderStatusWriterAllowlist[rel]
				_, funcOK := orderStatusWriterAllowlist[rel+"#"+funcName]
				if fileOK || funcOK {
					allowedHits++
					return true
				}
				offenders = append(offenders, fset.Position(lit.Pos()).String()+" in "+funcName)
				return true
			})
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				check(fn.Name.Name, fn)
			} else {
				check("<package-level>", decl)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if allowedHits == 0 {
		t.Fatalf("scanner matched no allowlisted writer at all; the pattern is broken")
	}
	if len(offenders) > 0 {
		t.Fatalf("raw food.orders status UPDATEs outside transitionOrderTx:\n  %s", strings.Join(offenders, "\n  "))
	}
}

func writesOrderStatus(sql string) bool {
	m := updateOrdersRe.FindStringSubmatch(sql)
	if m == nil {
		return false
	}
	set := m[1]
	if loc := whereRe.FindStringIndex(set); loc != nil {
		set = set[:loc[0]]
	}
	return statusAssignRe.MatchString(set)
}

func TestWritesOrderStatusPattern(t *testing.T) {
	cases := map[string]bool{
		`UPDATE food.orders SET status = 'X' WHERE id = $1`:                          true,
		"UPDATE food.orders\n\t\tSET status = $2::food.order_status WHERE id = $1":   true,
		`UPDATE food.orders o SET o.status = 'X'`:                                     true,
		`UPDATE food.orders SET invoice_number = $2 WHERE id = $1`:                    false,
		`UPDATE food.orders SET payment_status = 'PAID' WHERE status = 'CONFIRMED'`:   false,
		`UPDATE food.orders SET delivered_at = NOW(), status = 'DELIVERED'`:           true,
		`UPDATE food.order_items SET status = 'X'`:                                    false,
		`SELECT status FROM food.orders WHERE id = $1`:                                false,
	}
	for sql, want := range cases {
		if got := writesOrderStatus(sql); got != want {
			t.Errorf("writesOrderStatus(%q) = %v, want %v", sql, got, want)
		}
	}
}
