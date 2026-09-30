package http

// Lane C1: the typed refusals a client branches on, through the real edge
// mapper. The handlers that route into it (AddToCart, CancelOrder, submit)
// are pinned end to end by the golden fixtures.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

func TestLaneC1TypedRefusals(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"second seller in the bag", postgres.ErrMultipleSellers, http.StatusConflict, "MULTIPLE_SELLERS"},
		{"withdrawn listing", postgres.ErrProductUnavailable, http.StatusConflict, "PRODUCT_UNAVAILABLE"},
		{"too few units", &postgres.OutOfStockError{Lines: []postgres.OutOfStockLine{{VariantID: uuid.New(), Requested: 2}}},
			http.StatusConflict, "OUT_OF_STOCK"},
		{"cancel after dispatch", postgres.ErrCancelNotPermitted, http.StatusConflict, "CANCEL_NOT_PERMITTED"},
		{"someone else's order", postgres.ErrNotOrderOwnerP0, http.StatusNotFound, "ORDER_NOT_FOUND"},
		{"retry raced a cancel", fmt.Errorf("%w: order is cancelled", postgres.ErrPaymentNotRetryable),
			http.StatusConflict, "ORDER_NOT_PAYABLE"},
	}
	for _, c := range cases {
		code, body := status(t, c.err)
		var env struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal([]byte(body), &env)
		if code != c.status || env.Error.Code != c.code {
			t.Errorf("%s: %d %s, want %d %s", c.name, code, env.Error.Code, c.status, c.code)
		}
	}
}

func TestIncompleteApplicationCarriesTheMissingCodes(t *testing.T) {
	code, body := status(t, &service.ApplicationIncompleteError{Missing: []string{"email", "payout_account"}})
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Missing []string `json:"missing"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatal(err)
	}
	if code != http.StatusConflict || env.Error.Code != "APPLICATION_INCOMPLETE" {
		t.Fatalf("%d %s, want 409 APPLICATION_INCOMPLETE", code, env.Error.Code)
	}
	if !reflect.DeepEqual(env.Error.Details.Missing, []string{"email", "payout_account"}) {
		t.Fatalf("details.missing = %v\n%s", env.Error.Details.Missing, body)
	}
}
