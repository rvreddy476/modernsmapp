package payments

// Bank offers (Razorpay Offers): the buyer-facing read of payments-service's
// offer registry (its migration 014).
//
//	GET /v1/payments/internal/offers?application=<app>&amount_minor=<paise>
//	scope payments:offers.read, answer {"data":{"items":[…]}}
//
// shared/paymentsclient has no method for this route and is shared with
// food-service, so the call is made here with the same credential rules:
// a service token minted for exactly this operation when commerce holds a
// signing key, the legacy internal key only where the client was built to
// allow it (the dev stack).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/atpost/shared/paymentsclient"
	"github.com/atpost/shared/servicetoken"
)

// OpOffersRead is the service-token operation payments-service requires.
const OpOffersRead = "payments:offers.read"

// Offer is one active, applicable bank offer, public fields only, exactly as
// payments-service's PublicOffer serialises it.
type Offer struct {
	ID               string  `json:"id"`
	Title            string  `json:"title"`
	Description      string  `json:"description"`
	PaymentMethod    string  `json:"payment_method"`
	DiscountType     string  `json:"discount_type"`
	DiscountValue    int64   `json:"discount_value"`
	MaxDiscountMinor *int64  `json:"max_discount_minor"`
	MinAmountMinor   int64   `json:"min_amount_minor"`
	EndsAt           *string `json:"ends_at"`
}

// offersAuth is how the offers read authenticates; it mirrors the shared
// client's choice for the same Client.
type offersAuth struct {
	baseURL     string
	signer      *servicetoken.Signer
	internalKey string
	http        *http.Client
}

// ListOffers asks payments-service for this application's active offers that
// apply to amountMinor (0 = no minimum filter). An empty list is a normal
// answer: payments returns one whenever PAYMENTS_OFFERS_ENABLED is off.
func (c *Client) ListOffers(ctx context.Context, amountMinor int64) ([]Offer, error) {
	if c == nil || c.offers == nil {
		return nil, fmt.Errorf("payments client: offers read not configured")
	}
	q := url.Values{}
	q.Set("application", c.applicationID)
	if amountMinor > 0 {
		q.Set("amount_minor", strconv.FormatInt(amountMinor, 10))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.offers.baseURL+paymentsclient.InternalBase+"/offers?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if c.offers.signer != nil {
		token, err := c.offers.signer.Mint(servicetoken.AudiencePayments, serviceName,
			[]string{OpOffersRead}, []string{servicetoken.RefOrder}, paymentsclient.TokenTTL)
		if err != nil {
			return nil, fmt.Errorf("payments client: mint offers token: %w", err)
		}
		req.Header.Set(paymentsclient.HeaderServiceAuthorization, "Bearer "+token)
	} else {
		req.Header.Set(paymentsclient.HeaderInternalServiceKey, c.offers.internalKey)
	}
	resp, err := c.offers.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPaymentsUnavailable, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("%w: offers read answered %d", ErrPaymentsUnavailable, resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%w: offers read answered %d: %s", ErrRefused, resp.StatusCode,
			strings.TrimSpace(string(raw)))
	}
	var env struct {
		Data struct {
			Items []Offer `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("payments client: offers answer: %w", err)
	}
	if env.Data.Items == nil {
		return []Offer{}, nil
	}
	return env.Data.Items, nil
}
