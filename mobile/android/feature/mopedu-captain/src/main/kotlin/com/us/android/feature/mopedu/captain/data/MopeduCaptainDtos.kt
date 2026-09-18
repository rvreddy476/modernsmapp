package com.us.android.feature.mopedu.captain.data

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement

/*
 * rider-service's partner routes, on the wire. Decoded with the platform Json
 * (ignoreUnknownKeys); every field the app does not require carries a default.
 */

@Serializable
data class LocationTelemetryDto(
    @SerialName("lat") val lat: Double,
    @SerialName("lng") val lng: Double,
    @SerialName("speed_mps") val speedMps: Double? = null,
    @SerialName("heading") val heading: Double? = null,
    @SerialName("accuracy_meters") val accuracyMeters: Double? = null,
    @SerialName("sequence") val sequence: Long = 0,
)

@Serializable
data class CaptainOfferDto(
    @SerialName("id") val id: String,
    @SerialName("ride_id") val rideId: String,
    @SerialName("score") val score: Double = 0.0,
    @SerialName("distance_km") val distanceKm: Double = 0.0,
    @SerialName("vehicle_type") val vehicleType: String = "bike",
    @SerialName("payment_method") val paymentMethod: String = "cash",
    @SerialName("pickup_address") val pickupAddress: String = "",
    @SerialName("pickup_lat") val pickupLat: Double = 0.0,
    @SerialName("pickup_lng") val pickupLng: Double = 0.0,
    @SerialName("drop_address") val dropAddress: String = "",
    @SerialName("drop_lat") val dropLat: Double = 0.0,
    @SerialName("drop_lng") val dropLng: Double = 0.0,
    @SerialName("estimated_fare_paise") val estimatedFarePaise: Long = 0,
    @SerialName("expires_at") val expiresAt: String? = null,
)

@Serializable
data class AcceptOfferResponseDto(
    @SerialName("ride_id") val rideId: String,
    @SerialName("partner_id") val partnerId: String = "",
    @SerialName("status") val status: String = "",
    @SerialName("otp_expires_at") val otpExpiresAt: String? = null,
)

@Serializable
data class StartRideRequestDto(@SerialName("otp") val otp: String)

@Serializable
data class CompleteRideRequestDto(
    @SerialName("final_distance_km") val finalDistanceKm: Double,
    @SerialName("final_duration_min") val finalDurationMin: Int,
    @SerialName("idempotency_key") val idempotencyKey: String,
)

@Serializable
data class CaptainEarningsDto(
    @SerialName("today_earnings_paise") val todayEarningsPaise: Long = 0,
    @SerialName("today_rides_count") val todayRidesCount: Int = 0,
    @SerialName("rating") val rating: Double = 5.0,
    @SerialName("total_earnings_paise") val totalEarningsPaise: Long = 0,
    @SerialName("total_rides_count") val totalRidesCount: Int = 0,
)

/**
 * `GET /v1/rider/rides/{id}/payment` (`service.RidePaymentStatus`), read by
 * the captain while waiting for an online payment. Pinned by
 * payment_status_paid.json (CaptainContractFixtureTest).
 */
@Serializable
data class RidePaymentDto(
    @SerialName("method") val method: String = "cash",
    /** cash_pending | cash_confirmed | pending | confirming | paid | failed | refunded | partially_refunded */
    @SerialName("status") val status: String = "",
    @SerialName("amount_paise") val amountPaise: Long = 0,
    @SerialName("refunded_paise") val refundedPaise: Long = 0,
    /** The open payments intent; null for cash and before an intent exists. */
    @SerialName("intent_id") val intentId: String? = null,
    @SerialName("updated_at") val updatedAt: String? = null,
)

// --- Onboarding & Partner Profile DTOs ---

@Serializable
data class PartnerProfileDto(
    @SerialName("id") val id: String,
    @SerialName("user_id") val userId: String? = null,
    @SerialName("partner_type") val partnerType: String = "individual_driver",
    @SerialName("full_name") val fullName: String = "",
    @SerialName("phone") val phone: String = "",
    @SerialName("email") val email: String? = null,
    @SerialName("status") val status: String = "draft",
    @SerialName("kyc_status") val kycStatus: String = "pending",
    @SerialName("city_id") val cityId: String? = null,
    @SerialName("profile_photo_url") val profilePhotoUrl: String? = null,
    @SerialName("rating") val rating: Double = 5.0,
    @SerialName("rides_completed") val ridesCompleted: Int = 0,
    @SerialName("is_online") val isOnline: Boolean = false,
    /**
     * The onboarding verdict (2026-09-18): DigiLocker-verified documents
     * auto-approve; only manually uploaded ones wait for review. Absent on a
     * server that predates it — see [PartnerProfileDto.toReview].
     */
    @SerialName("review") val review: PartnerReviewDto? = null,
)

/** `review: {state, pending}` on `GET /v1/rider/partners/me`. */
@Serializable
data class PartnerReviewDto(
    /** approved | under_review | incomplete */
    @SerialName("state") val state: String = "incomplete",
    /** What is still missing or waiting, by document type, e.g. `["profile_photo"]` (the selfie). */
    @SerialName("pending") val pending: List<String> = emptyList(),
)

@Serializable
data class CreatePartnerRequestDto(
    @SerialName("partner_type") val partnerType: String = "individual_driver",
    @SerialName("full_name") val fullName: String,
    @SerialName("phone") val phone: String,
    @SerialName("email") val email: String? = null,
    @SerialName("city_id") val cityId: String? = null,
)

@Serializable
data class UpdatePartnerRequestDto(
    @SerialName("full_name") val fullName: String? = null,
    @SerialName("email") val email: String? = null,
    @SerialName("profile_photo_url") val profilePhotoUrl: String? = null,
    @SerialName("city_id") val cityId: String? = null,
)

// --- Document KYC DTOs ---

@Serializable
data class PartnerDocumentDto(
    @SerialName("id") val id: String,
    @SerialName("partner_id") val partnerId: String = "",
    @SerialName("document_type") val documentType: String,
    @SerialName("document_number") val documentNumber: String? = null,
    @SerialName("file_url") val fileUrl: String = "",
    @SerialName("status") val status: String = "submitted",
    @SerialName("rejection_reason") val rejectionReason: String? = null,
    @SerialName("expires_at") val expiresAt: String? = null,
)

/**
 * `POST /v1/rider/partners/me/documents`. The photo goes up through
 * :core:media first; `media_id` is the confirmed asset, which is what the
 * server's automatic selfie check compares with the DigiLocker licence photo
 * (service.SubmitKYCDocumentRequest.MediaID). `file_url` is required non-empty
 * by the server and carries the same asset as `media://{id}` — the app holds
 * no other locator for it; media-service serves the bytes by id.
 */
@Serializable
data class SubmitDocumentRequestDto(
    @SerialName("document_type") val documentType: String,
    @SerialName("document_number") val documentNumber: String? = null,
    @SerialName("file_url") val fileUrl: String,
    @SerialName("media_id") val mediaId: String? = null,
    @SerialName("expires_at") val expiresAt: String? = null,
) {
    companion object {
        /** The request for an uploaded photo: the media id in both fields, as the server reads each. */
        fun forMedia(documentType: String, documentNumber: String?, mediaId: String): SubmitDocumentRequestDto =
            SubmitDocumentRequestDto(documentType, documentNumber, fileUrl = "media://$mediaId", mediaId = mediaId)
    }
}

// --- DigiLocker Aadhaar DTOs ---

@Serializable
data class AadhaarStartResponseDto(
    @SerialName("digilocker_url") val digiLockerUrl: String,
    @SerialName("request_id") val requestId: String,
)

@Serializable
data class AadhaarCallbackRequestDto(
    @SerialName("request_id") val requestId: String,
    @SerialName("assertion_token") val assertionToken: String,
)

// --- Vehicle DTOs ---

@Serializable
data class VehicleDto(
    @SerialName("id") val id: String,
    @SerialName("partner_id") val partnerId: String = "",
    @SerialName("vehicle_type") val vehicleType: String = "bike",
    @SerialName("registration_number") val registrationNumber: String = "",
    @SerialName("brand") val brand: String? = null,
    @SerialName("model") val model: String? = null,
    @SerialName("color") val color: String? = null,
    @SerialName("manufacture_year") val manufactureYear: Int? = null,
    @SerialName("year") val year: Int? = null,
    @SerialName("status") val status: String = "pending_review",
)

@Serializable
data class CreateVehicleRequestDto(
    @SerialName("vehicle_type") val vehicleType: String,
    @SerialName("registration_number") val registrationNumber: String,
    @SerialName("brand") val brand: String? = null,
    @SerialName("model") val model: String? = null,
    @SerialName("color") val color: String? = null,
    @SerialName("manufacture_year") val manufactureYear: Int? = null,
    @SerialName("seat_count") val seatCount: Int? = null,
    @SerialName("fuel_type") val fuelType: String? = null,
    @SerialName("is_ev") val isEv: Boolean = false,
)

// --- Subscription DTOs ---

/**
 * `GET /v1/rider/subscriptions/plans`. The price is `price_amount` in RUPEES
 * (legacy); `price_paise` is read too when a newer server sends it. The trial
 * (`trial_7d`) has price 0.
 */
@Serializable
data class SubscriptionPlanDto(
    @SerialName("id") val id: String = "",
    @SerialName("code") val code: String = "",
    @SerialName("name") val name: String = "",
    @SerialName("vehicle_type") val vehicleType: String = "",
    @SerialName("billing_cycle") val billingCycle: String = "",
    /** Rupees, legacy. */
    @SerialName("price_amount") val priceAmount: Double = 0.0,
    @SerialName("price_paise") val pricePaise: Long = 0,
    @SerialName("billing_period_days") val billingPeriodDays: Int = 0,
    @SerialName("daily_lead_cap") val dailyLeadCap: Int? = null,
    @SerialName("priority_score") val priorityScore: Int = 10,
    @SerialName("description") val description: String = "",
)

/**
 * `POST /v1/rider/subscriptions/checkout` (2026-09-18): the plan is paid on
 * the device through payments-service. The old payment-proof route answers
 * 410 and is gone from this app.
 */
@Serializable
data class SubscriptionCheckoutRequestDto(
    @SerialName("plan_code") val planCode: String,
    /** "upi" | "card". Sent for the trial too; the server ignores it there. */
    @SerialName("method") val method: String,
)

/**
 * The checkout answer. For the trial `status` is `active` and there is no
 * `client_session`: it is granted instantly, once ever. For a paid plan the
 * session opens the sheet and `subscription_id` is the payment's reference.
 */
@Serializable
data class SubscriptionCheckoutResponseDto(
    @SerialName("subscription_id") val subscriptionId: String,
    @SerialName("intent_id") val intentId: String? = null,
    @SerialName("amount_paise") val amountPaise: Long = 0,
    @SerialName("currency") val currency: String = "INR",
    /** `{provider, order_id, key_id}`, relayed to the sheet as sent. Absent for the trial. */
    @SerialName("client_session") val clientSession: Map<String, String>? = null,
    /** active | pending */
    @SerialName("status") val status: String = "",
)

/** `GET /v1/rider/subscriptions/me/payment`: the ONLY source of "paid" for a plan. */
@Serializable
data class SubscriptionPaymentDto(
    /** pending | confirming | paid | failed */
    @SerialName("status") val status: String = "",
    @SerialName("intent_id") val intentId: String? = null,
    @SerialName("expires_at") val expiresAt: String? = null,
)

@Serializable
data class PartnerSubscriptionDto(
    @SerialName("id") val id: String,
    @SerialName("partner_id") val partnerId: String = "",
    @SerialName("plan_id") val planId: String = "",
    @SerialName("plan_code") val planCode: String = "",
    @SerialName("plan_name") val planName: String = "",
    @SerialName("status") val status: String = "",
    @SerialName("leads_used") val leadsUsed: Int = 0,
    @SerialName("daily_lead_cap") val dailyLeadCap: Int? = null,
    @SerialName("starts_at") val startsAt: String = "",
    @SerialName("expires_at") val expiresAt: String = "",
)

/** The error half of the platform envelope, decoded on its own so a failed call needs no `T`. */
@Serializable
data class CaptainErrorEnvelopeDto(
    val error: CaptainErrorBodyDto? = null,
    val meta: JsonElement? = null,
)

@Serializable
data class CaptainErrorBodyDto(
    val code: String = "",
    val message: String = "",
    val details: JsonElement? = null,
)
