package http

// The listing editor's refusal goldens, rendered from the error mapper itself
// and without a database, so the ordinary `go test ./...` sweep pins them.
//
// The same files are registered in contracts_fixtures_integration_test.go,
// where the real routes render them against seeded rows; this test is the
// half that runs everywhere. A client copies these bytes (the web's
// __tests__/contracts/seller/), so a changed code, message or detail key
// fails here by name before it reaches one.
//
//	go test ./internal/http/ -run TestListingErrorGoldens -update-listing-errors

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/gin-gonic/gin"
)

var updateListingErrors = flag.Bool("update-listing-errors", false, "rewrite the listing refusal goldens from writeProductWriteError")

func renderProductWriteError(t *testing.T, err error) (int, []byte) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPatch, "/v1/commerce/variants/x", nil)
	writeProductWriteError(c, err)
	var out bytes.Buffer
	if e := json.Indent(&out, w.Body.Bytes(), "", "  "); e != nil {
		t.Fatalf("not JSON: %v\n%s", e, w.Body.String())
	}
	out.WriteByte('\n')
	return w.Code, out.Bytes()
}

func TestListingErrorGoldens(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		// The wrapping the service applies, so the mapper is proven to see
		// through it exactly as it does on the live route.
		{"seller/variant_patch_400_price_above_mrp", fmt.Errorf("%w: selling price 159900 paise, MRP 149900 paise", service.ErrSellingAboveMRP)},
		{"seller/variant_patch_409_sku_locked", service.ErrSKULocked},
		{"seller/product_patch_400_return_policy_invalid", fmt.Errorf("%w: got %q", service.ErrReturnPolicyInvalid, "14_days")},
	} {
		status, got := renderProductWriteError(t, tc.err)
		if want := ctStatusFromName(tc.name); status != want {
			t.Errorf("%s: mapper answered %d, the fixture name says %d\n%s", tc.name, status, want, got)
			continue
		}
		path := filepath.Join(contractsDir, tc.name+".json")
		if *updateListingErrors {
			if err := os.WriteFile(path, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: %v (run with -update-listing-errors)", tc.name, err)
			continue
		}
		if !bytes.Equal(want, got) {
			t.Errorf("%s: golden differs from the mapper.\n--- golden\n%s\n--- mapper\n%s", tc.name, want, got)
		}
	}
}
