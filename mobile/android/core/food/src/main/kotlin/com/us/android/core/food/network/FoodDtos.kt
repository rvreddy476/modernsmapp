package com.us.android.core.food.network

import com.us.android.core.network.ApiMeta
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/*
 * Wire shapes for food-service's onboarding (Wave 1 B1), payout accounts (B2),
 * capabilities and realtime token routes.
 *
 * The response DTOs are pinned field for field by the golden fixtures in
 * food-service/internal/http/testdata/contracts/, copied byte for byte into
 * src/test/resources/contracts/ and decoded STRICTLY (unknown keys fail) by
 * FoodContractFixtureTest. A renamed key on either side fails that test.
 *
 * Dates: `*_at` timestamps are RFC 3339 strings as Go marshals time.Time;
 * `fssai_expires_at` and `specified_premises_declared_at` are plain
 * YYYY-MM-DD dates. Kept as strings: the UI formats, it does not compute.
 *
 * No money appears on these routes. When it does (B3), it is integer paise.
 */

// Requests

@Serializable
data class ComplianceRequest(
    /** RESTAURANT_SPECIFIED_PREMISES, CLOUD_KITCHEN_TAKEAWAY, … (onboarding.go). */
    @SerialName("tax_category") val taxCategory: String,
    @SerialName("legal_name") val legalName: String,
    val pan: String,
    /** Required when the category leaves the restaurant GST-liable; null omits it. */
    val gstin: String? = null,
    /** YYYY-MM-DD; required for the specified-premises category. */
    @SerialName("specified_premises_declared_at") val specifiedPremisesDeclaredAt: String? = null,
)

@Serializable
data class LocationRequest(
    val latitude: Double,
    val longitude: Double,
    @SerialName("address_line1") val addressLine1: String,
    @SerialName("address_line2") val addressLine2: String? = null,
    val city: String,
    val state: String,
    @SerialName("postal_code") val postalCode: String,
    @SerialName("google_place_id") val googlePlaceId: String? = null,
    /** 1–15 km; FOOD_DELIVERY_RADIUS_OUT_OF_RANGE otherwise. */
    @SerialName("delivery_radius_km") val deliveryRadiusKm: Double,
)

@Serializable
data class OperatingHoursRequest(
    val windows: List<OperatingWindowRequest>,
)

@Serializable
data class OperatingWindowRequest(
    /** 0 = Sunday … 6 = Saturday. */
    @SerialName("day_of_week") val dayOfWeek: Int,
    /** HH:MM, or empty on a closed day. */
    @SerialName("opens_at") val opensAt: String,
    @SerialName("closes_at") val closesAt: String,
    @SerialName("is_closed") val isClosed: Boolean,
)

@Serializable
data class AcceptingRequest(
    @SerialName("is_accepting_orders") val isAcceptingOrders: Boolean,
)

@Serializable
data class FssaiRequest(
    @SerialName("licence_number") val licenceNumber: String,
    /** YYYY-MM-DD, after today. */
    @SerialName("expires_at") val expiresAt: String,
    /** The uploaded licence image, from the media pipeline. */
    @SerialName("media_id") val mediaId: String,
)

@Serializable
data class PayoutAccountRequest(
    @SerialName("holder_name") val holderName: String,
    @SerialName("account_number") val accountNumber: String,
    val ifsc: String,
)

// Responses

/** `GET /v1/food/me/capabilities` (handler_capabilities.go). No golden fixture exists yet. */
@Serializable
data class FoodCapabilitiesDto(
    @SerialName("user_id") val userId: String,
    @SerialName("is_customer") val isCustomer: Boolean = false,
    @SerialName("is_restaurant_owner") val isRestaurantOwner: Boolean = false,
    @SerialName("is_delivery_partner") val isDeliveryPartner: Boolean = false,
    @SerialName("is_admin") val isAdmin: Boolean = false,
    @SerialName("is_moderator") val isModerator: Boolean = false,
)

/** `POST /v1/food/realtime/token` (handler_realtime.go). No golden fixture exists yet. */
@Serializable
data class RealtimeTokenDto(
    val token: String,
    val topics: List<String> = emptyList(),
)

@Serializable
data class ComplianceDto(
    @SerialName("restaurant_id") val restaurantId: String,
    @SerialName("tax_category") val taxCategory: String = "",
    @SerialName("legal_name") val legalName: String = "",
    val gstin: String? = null,
    @SerialName("gstin_state_code") val gstinStateCode: String? = null,
    /** Masked only, e.g. `****000Z`. The server never returns the PAN. */
    @SerialName("pan_masked") val panMasked: String = "",
    @SerialName("pan_holder_type") val panHolderType: String = "",
    @SerialName("specified_premises_declared_at") val specifiedPremisesDeclaredAt: String? = null,
    @SerialName("compliance_submitted_at") val complianceSubmittedAt: String? = null,
    /** SUPPLIER or ECO_SECTION_9_5. */
    @SerialName("gst_liability") val gstLiability: String = "",
    @SerialName("gstin_required") val gstinRequired: Boolean = false,
)

@Serializable
data class LocationDto(
    @SerialName("restaurant_id") val restaurantId: String,
    val latitude: Double,
    val longitude: Double,
    @SerialName("address_line1") val addressLine1: String = "",
    @SerialName("address_line2") val addressLine2: String? = null,
    val city: String = "",
    val state: String = "",
    @SerialName("postal_code") val postalCode: String = "",
    @SerialName("google_place_id") val googlePlaceId: String? = null,
    @SerialName("delivery_radius_km") val deliveryRadiusKm: Double = 0.0,
    @SerialName("service_area_id") val serviceAreaId: String? = null,
)

@Serializable
data class OperatingHoursDto(
    @SerialName("restaurant_id") val restaurantId: String,
    val timezone: String = "",
    @SerialName("is_open_now") val isOpenNow: Boolean = false,
    val windows: List<OperatingWindowDto> = emptyList(),
)

@Serializable
data class OperatingWindowDto(
    @SerialName("day_of_week") val dayOfWeek: Int,
    @SerialName("opens_at") val opensAt: String = "",
    @SerialName("closes_at") val closesAt: String = "",
    @SerialName("is_closed") val isClosed: Boolean = false,
    /** True when [closesAt] is on the next day (18:00 → 02:00). */
    val overnight: Boolean = false,
)

@Serializable
data class AcceptingDto(
    @SerialName("restaurant_id") val restaurantId: String,
    val status: String = "",
    @SerialName("is_accepting_orders") val isAcceptingOrders: Boolean = false,
)

@Serializable
data class FssaiDto(
    @SerialName("restaurant_id") val restaurantId: String,
    @SerialName("fssai_licence_number") val fssaiLicenceNumber: String = "",
    @SerialName("fssai_expires_at") val fssaiExpiresAt: String? = null,
    val document: RestaurantDocumentDto? = null,
)

/** A restaurant document row; the FSSAI submit and the admin decision both return it. */
@Serializable
data class RestaurantDocumentDto(
    val id: String,
    @SerialName("restaurant_id") val restaurantId: String,
    @SerialName("document_type") val documentType: String = "",
    @SerialName("document_number") val documentNumber: String? = null,
    @SerialName("media_id") val mediaId: String? = null,
    /** PENDING, APPROVED, REJECTED. */
    val status: String = "",
    @SerialName("rejection_reason") val rejectionReason: String? = null,
    @SerialName("expires_at") val expiresAt: String? = null,
    @SerialName("verified_by") val verifiedBy: String? = null,
    @SerialName("verified_at") val verifiedAt: String? = null,
    @SerialName("created_at") val createdAt: String? = null,
)

@Serializable
data class SubmitDto(
    @SerialName("restaurant_id") val restaurantId: String,
    val status: String = "",
    val missing: List<String> = emptyList(),
)

/** Restaurant and delivery-partner payout accounts share one shape; masked only. */
@Serializable
data class PayoutAccountDto(
    /** RESTAURANT or DELIVERY_PARTNER. */
    @SerialName("owner_type") val ownerType: String,
    @SerialName("owner_id") val ownerId: String,
    @SerialName("holder_name") val holderName: String = "",
    @SerialName("account_number_masked") val accountNumberMasked: String = "",
    val ifsc: String = "",
    @SerialName("verification_status") val verificationStatus: String = "",
    @SerialName("verification_reason") val verificationReason: String? = null,
    @SerialName("verified_name") val verifiedName: String? = null,
    @SerialName("verified_at") val verifiedAt: String? = null,
    @SerialName("created_at") val createdAt: String? = null,
    @SerialName("updated_at") val updatedAt: String? = null,
)

// Errors

/**
 * The error half of the platform envelope, with food's `details` typed.
 *
 * `details` carries `field` on a 422 validation failure and `missing` on
 * FOOD_RESTAURANT_NOT_READY (writeOnboardingError in handler_onboarding.go).
 */
@Serializable
data class FoodErrorEnvelopeDto(
    val error: FoodErrorBodyDto? = null,
    val meta: ApiMeta? = null,
)

@Serializable
data class FoodErrorBodyDto(
    val code: String = "",
    val message: String = "",
    val details: FoodErrorDetailsDto? = null,
)

@Serializable
data class FoodErrorDetailsDto(
    val field: String? = null,
    val missing: List<String>? = null,
)
