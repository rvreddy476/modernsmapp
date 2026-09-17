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

	// Content apps (Wave 2 — Social, Tube, Q&A, Chat). post-service serves
	// both Social (posts, reels, comments, reports, creators) and Tube (videos,
	// channels, series); business pages live in the app user-service, whose
	// admin family verifies audience "social". The three chat services all
	// verify audience "chat" but live at their own URLs and prefixes.
	PostAudience    = "post"
	PostAdminPrefix = "/v1/posts/internal/admin"

	QAAudience    = "qa"
	QAAdminPrefix = "/v1/qa/internal/admin"

	UserPagesAudience    = "social"
	UserPagesAdminPrefix = "/v1/users/internal/admin"

	ChatAudience         = "chat"
	ChannelAdminPrefix   = "/v1/broadcast-channels/internal/admin"
	GroupAdminPrefix     = "/v1/groups/internal/admin"
	CommunityAdminPrefix = "/v1/communities/internal/admin"

	// Mopedu (Wave 2): rider-service's token-only admin family
	// (rider-service/internal/http/admin_token.go), audience "rider".
	RiderAudience    = "rider"
	RiderAdminPrefix = "/v1/rider/internal/admin"
)

// NewRiderClient builds the client for rider-service (Mopedu).
func NewRiderClient(baseURL string, signer *servicetoken.Signer) *ProductClient {
	return newProductClient(baseURL, RiderAdminPrefix, RiderAudience, signer)
}

// NewPostClient builds the client for post-service (Social and Tube).
func NewPostClient(baseURL string, signer *servicetoken.Signer) *ProductClient {
	return newProductClient(baseURL, PostAdminPrefix, PostAudience, signer)
}

// NewQAClient builds the client for qa-service.
func NewQAClient(baseURL string, signer *servicetoken.Signer) *ProductClient {
	return newProductClient(baseURL, QAAdminPrefix, QAAudience, signer)
}

// NewUserPagesClient builds the client for user-service's business pages
// family (audience "social", alongside its private-profile callers).
func NewUserPagesClient(baseURL string, signer *servicetoken.Signer) *ProductClient {
	return newProductClient(baseURL, UserPagesAdminPrefix, UserPagesAudience, signer)
}

// NewChannelClient builds the client for channel-service (broadcast channels).
func NewChannelClient(baseURL string, signer *servicetoken.Signer) *ProductClient {
	return newProductClient(baseURL, ChannelAdminPrefix, ChatAudience, signer)
}

// NewGroupClient builds the client for group-service.
func NewGroupClient(baseURL string, signer *servicetoken.Signer) *ProductClient {
	return newProductClient(baseURL, GroupAdminPrefix, ChatAudience, signer)
}

// NewCommunityClient builds the client for community-service.
func NewCommunityClient(baseURL string, signer *servicetoken.Signer) *ProductClient {
	return newProductClient(baseURL, CommunityAdminPrefix, ChatAudience, signer)
}

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
