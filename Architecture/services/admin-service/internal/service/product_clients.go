package service

import "github.com/atpost/shared/servicetoken"

// Product admin families reached with admin-service tokens. Each product
// verifies audience, issuer admin-service, the one scope, and act.
const (
	FoodAudience    = "food"
	FoodAdminPrefix = "/v1/food/internal/admin"

	CommerceAudience    = "commerce"
	CommerceAdminPrefix = "/v1/commerce/internal/admin"

	TrustSafetyAudience    = "trust_safety"
	TrustSafetyAdminPrefix = "/v1/internal/admin/trust"

	MonetizationAudience    = "monetization"
	MonetizationAdminPrefix = "/v1/monetization/internal/admin"

	// Payments registers admin-service through its existing SERVICE_CALLERS
	// (OPS = its admin permissions, no REFTYPES).
	PaymentsAudience    = "payments"
	PaymentsAdminPrefix = "/v1/payments/internal/admin"
)

// NewMonetizationClient builds the client for monetization-service.
func NewMonetizationClient(baseURL string, signer *servicetoken.Signer) *ProductClient {
	return newProductClient(baseURL, MonetizationAdminPrefix, MonetizationAudience, signer)
}

// NewPaymentsClient builds the client for payments-service.
func NewPaymentsClient(baseURL string, signer *servicetoken.Signer) *ProductClient {
	return newProductClient(baseURL, PaymentsAdminPrefix, PaymentsAudience, signer)
}

// NewFoodClient builds the client for food-service (Feast).
func NewFoodClient(baseURL string, signer *servicetoken.Signer) *ProductClient {
	return newProductClient(baseURL, FoodAdminPrefix, FoodAudience, signer)
}

// NewCommerceClient builds the client for commerce-service (MStore). It
// replaced the internal-key client that forwarded X-User-Id.
func NewCommerceClient(baseURL string, signer *servicetoken.Signer) *ProductClient {
	return newProductClient(baseURL, CommerceAdminPrefix, CommerceAudience, signer)
}

// NewTrustSafetyClient builds the client for trust-safety-service.
func NewTrustSafetyClient(baseURL string, signer *servicetoken.Signer) *ProductClient {
	return newProductClient(baseURL, TrustSafetyAdminPrefix, TrustSafetyAudience, signer)
}
