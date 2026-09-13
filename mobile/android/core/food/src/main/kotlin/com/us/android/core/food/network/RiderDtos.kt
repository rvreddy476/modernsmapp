package com.us.android.core.food.network

import com.us.android.core.food.model.Paise
import com.us.android.core.food.model.RupeesAsPaiseSerializer
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/*
 * Wire shapes for the delivery partner's routes (Feast A4, 2026-09-13), from
 * food-service handler.go, handler_rider_kyc.go, handler_delivery_offers.go,
 * handler_delivery_proof.go and store/postgres (models.go, delivery_kyc.go,
 * delivery_offers.go, tracking_payments.go).
 *
 * Pinned STRICTLY by the golden fixtures copied into src/test/resources/contracts
 * where food-service has them (assignment, documents, location, verify-delivery,
 * DigiLocker, KYC status). Profile, offers and earnings have no goldens yet, so
 * those DTOs default every field.
 *
 * MONEY: the assignment's `delivery_fee` / `delivery_partner_payout` and the
 * earnings summary are FLOAT RUPEES on the wire. They decode through
 * [RupeesAsPaiseSerializer] — decimal text to paise, never a Double. The float
 * wire is a backend gap reported with Feast A4, as it was for Kitchen in A3.
 */

// Profile

/**
 * `POST|PATCH /v1/food/delivery/profile`. The server REPLACES every field on
 * update, so callers always send the whole profile. `full_name` and `phone` are
 * required (400 FOOD_DELIVERY_PROFILE_FAILED otherwise).
 */
@Serializable
data class DeliveryPartnerRequest(
    @SerialName("full_name") val fullName: String,
    val phone: String,
    val email: String = "",
    /** Free text server-side; riderkyc classifies BICYCLE / MOTORCYCLE / SCOOTER / EV_SCOOTER …. */
    @SerialName("vehicle_type") val vehicleType: String,
    @SerialName("vehicle_number") val vehicleNumber: String = "",
    val city: String = "",
)

/** postgres.DeliveryPartner. */
@Serializable
data class DeliveryPartnerDto(
    val id: String,
    @SerialName("user_id") val userId: String = "",
    @SerialName("full_name") val fullName: String = "",
    val phone: String = "",
    val email: String = "",
    /** PENDING_REVIEW, APPROVED, ACTIVE, OFFLINE, REJECTED, SUSPENDED. */
    val status: String = "",
    @SerialName("vehicle_type") val vehicleType: String = "",
    @SerialName("vehicle_number") val vehicleNumber: String = "",
    val city: String = "",
    @SerialName("is_online") val isOnline: Boolean = false,
    @SerialName("created_at") val createdAt: String? = null,
)

/** No default on purpose: `false` must reach the wire. */
@Serializable
data class DeliveryAvailabilityRequest(
    @SerialName("is_online") val isOnline: Boolean,
)

// Verification

/** `POST …/kyc/digilocker/start` (kyc_digilocker_start_200*.json). */
@Serializable
data class DigiLockerStartDto(
    /** Open in the browser. In local/dev with the mock it is food-service's own dev authorize route. */
    @SerialName("authorize_url") val authorizeUrl: String,
    /** Echoed back on the return link; the app checks it before posting the callback. */
    val state: String,
    @SerialName("expires_at") val expiresAt: String,
)

@Serializable
data class DigiLockerCallbackRequest(
    val state: String,
    val code: String,
)

/** The rider's verification view (kyc_status_get_200.json, kyc_digilocker_callback_200.json). */
@Serializable
data class DeliveryKycDto(
    @SerialName("partner_id") val partnerId: String,
    val status: String,
    @SerialName("vehicle_type") val vehicleType: String,
    /** False only for a bicycle: then `driving_licence` and `vehicle_rc` never appear in [missing]. */
    @SerialName("driving_documents_required") val drivingDocumentsRequired: Boolean,
    /** riderkyc.MissingSteps, in presentation order. Empty means ready for approval. */
    val missing: List<String>,
    val checks: List<KycCheckDto>,
    val documents: List<DeliveryDocumentDto>,
    @SerialName("has_payout_account") val hasPayoutAccount: Boolean,
)

@Serializable
data class KycCheckDto(
    /** AADHAAR, DRIVING_LICENCE, VEHICLE_RC. */
    val kind: String,
    val provider: String,
    @SerialName("name_on_document_masked") val nameOnDocumentMasked: String,
    /** YYYY-MM-DD; null for Aadhaar. */
    @SerialName("valid_until") val validUntil: String? = null,
    val valid: Boolean,
    @SerialName("verified_at") val verifiedAt: String? = null,
)

/**
 * `POST /v1/food/delivery/documents`. A SELFIE carries no number
 * (422 FOOD_SELFIE_NUMBER_NOT_ALLOWED); a null [documentNumber] is omitted.
 */
@Serializable
data class DeliveryDocumentRequest(
    @SerialName("document_type") val documentType: String,
    @SerialName("document_number") val documentNumber: String? = null,
    @SerialName("media_id") val mediaId: String,
)

/** postgres.DeliveryDocument — masked number only, never the one typed. */
@Serializable
data class DeliveryDocumentDto(
    val id: String,
    @SerialName("delivery_partner_id") val deliveryPartnerId: String,
    @SerialName("document_type") val documentType: String,
    @SerialName("number_masked") val numberMasked: String? = null,
    @SerialName("media_id") val mediaId: String? = null,
    /** PENDING, APPROVED, REJECTED. */
    val status: String,
    @SerialName("rejection_reason") val rejectionReason: String? = null,
    @SerialName("expires_at") val expiresAt: String? = null,
    @SerialName("verified_at") val verifiedAt: String? = null,
    @SerialName("created_at") val createdAt: String,
)

// Location

/**
 * `POST /v1/food/delivery/location`. [heading] is degrees 0–360 and omitted
 * when the fix has none; [accuracyMeters] likewise.
 */
@Serializable
data class DeliveryLocationRequest(
    val latitude: Double,
    val longitude: Double,
    @SerialName("accuracy_meters") val accuracyMeters: Double? = null,
    val heading: Double? = null,
)

/** postgres.DeliveryLocationResult (delivery_location_post_200.json). */
@Serializable
data class DeliveryLocationDto(
    val id: String,
    @SerialName("delivery_partner_id") val deliveryPartnerId: String,
    /** The zero UUID when the rider holds no job. */
    @SerialName("assignment_id") val assignmentId: String,
    @SerialName("assignment_ids") val assignmentIds: List<String> = emptyList(),
    val latitude: Double,
    val longitude: Double,
    @SerialName("accuracy_meters") val accuracyMeters: Double? = null,
    val heading: Double? = null,
    /** Postgres text, not RFC 3339. */
    @SerialName("recorded_at") val recordedAt: String,
)

// Offers

/** `GET …/offers/me` → `{"offers": [...]}`; Go marshals an empty list as null. */
@Serializable
data class DeliveryOffersDto(
    val offers: List<DeliveryOfferDto>? = null,
)

/**
 * postgres.DeliveryOffer. Carries NO restaurant, address or payout — the offer
 * screen can show only distance and time left (a backend gap, reported).
 */
@Serializable
data class DeliveryOfferDto(
    val id: String,
    @SerialName("order_id") val orderId: String = "",
    @SerialName("delivery_partner_id") val deliveryPartnerId: String = "",
    /** pending, accepted, rejected, expired, superseded. */
    val status: String = "",
    @SerialName("distance_km") val distanceKm: Double? = null,
    /** Postgres `timestamptz::text` here; RFC 3339 in the push payload. */
    @SerialName("expires_at") val expiresAt: String = "",
    @SerialName("responded_at") val respondedAt: String? = null,
    @SerialName("reject_reason") val rejectReason: String? = null,
    @SerialName("created_at") val createdAt: String? = null,
)

@Serializable
data class OfferResponseDto(
    @SerialName("offer_id") val offerId: String = "",
    val status: String = "",
)

@Serializable
data class RejectOfferRequest(
    val reason: String? = null,
)

// The job

/** postgres.DeliveryAssignment (delivery_assignment_current_get_200_*.json). */
@Serializable
data class DeliveryAssignmentDto(
    val id: String,
    @SerialName("order_id") val orderId: String,
    @SerialName("order_number") val orderNumber: String,
    @SerialName("restaurant_name") val restaurantName: String,
    @SerialName("restaurant_id") val restaurantId: String,
    @SerialName("delivery_partner_id") val deliveryPartnerId: String? = null,
    /** ASSIGNED, ACCEPTED, ARRIVED_AT_RESTAURANT, PICKED_UP, ARRIVED_AT_CUSTOMER, DELIVERED, … */
    val status: String,
    @SerialName("order_status") val orderStatus: String,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("delivery_fee") val deliveryFee: Paise,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("delivery_partner_payout") val deliveryPartnerPayout: Paise,
    @SerialName("created_at") val createdAt: String,
    /** Present only after the rider accepted and before pickup. Shown to the kitchen. */
    @SerialName("pickup_code") val pickupCode: String? = null,
)

@Serializable
data class DeliveryCodeRequest(
    val code: String,
)

@Serializable
data class VerifyDeliveryDto(
    @SerialName("assignment_id") val assignmentId: String,
    @SerialName("order_id") val orderId: String,
    val status: String,
)

/** `GET …/earnings` (a map server-side). Money in float rupees. */
@Serializable
data class DeliveryEarningsDto(
    @SerialName("deliveries_today") val deliveriesToday: Int = 0,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("earnings_today") val earningsToday: Paise = Paise.ZERO,
    @SerialName("total_deliveries") val totalDeliveries: Int = 0,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("total_earnings") val totalEarnings: Paise = Paise.ZERO,
)
