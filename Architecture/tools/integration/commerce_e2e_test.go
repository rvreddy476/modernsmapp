//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestE2E_Commerce_BrowseAndCart is a commerce read smoke: a shopper can browse
// the catalog and read their (empty) cart through the gateway. Guards routing +
// the read path.
//
// The full browse → add-to-cart → order → pay → shipment journey needs seeded
// products + the payment-gateway mock; tracked as a deeper commerce follow-up.
func TestE2E_Commerce_BrowseAndCart(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)

	shopper := NewHTTPClient(urls.APIGateway, uuid.New())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if env := shopper.MustDo(t, ctx, "GET", "/v1/commerce/products", nil); env.Status != 200 {
		t.Errorf("GET /v1/commerce/products: want 200, got %d", env.Status)
	}
	if env := shopper.MustDo(t, ctx, "GET", "/v1/commerce/cart", nil); env.Status >= 500 {
		t.Errorf("GET /v1/commerce/cart: server error %d (want 2xx/4xx)", env.Status)
	}
}
