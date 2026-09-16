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
)

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
