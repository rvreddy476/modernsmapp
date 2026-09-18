package com.us.android.feature.mopedu.rider.data

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement

/*
 * rider-service's customer routes, on the wire. Decoded with the platform Json
 * (ignoreUnknownKeys), so a field the server adds ahead of this build is
 * ignored rather than fatal; every field the app does not require carries a
 * default, so one the server omits decodes too. The golden fixtures under
 * src/test/resources/contracts pin these names once the pricing lane drops
 * them (MopeduContractFixtureTest).
 */

@Serializable
data class EstimateRequestDto(
    @SerialName("pickup_lat") val pickupLat: Double,
    @SerialName("pickup_lng") val pickupLng: Double,
    @SerialName("pickup_label") val pickupLabel: String = "",
    @SerialName("drop_lat") val dropLat: Double,
    @SerialName("drop_lng") val dropLng: Double,
    @SerialName("drop_label") val dropLabel: String = "",
    @SerialName("city_id") val cityId: String? = null,
    @SerialName("vehicle_type") val vehicleType: String? = null,
    /** Optional: the estimate is re-requested with the code once the coupon validates. */
    @SerialName("coupon_code") val couponCode: String? = null,
)

@Serializable
data class QuoteBreakdownDto(
    @SerialName("base_paise") val basePaise: Long = 0,
    @SerialName("distance_paise") val distancePaise: Long = 0,
    @SerialName("time_paise") val timePaise: Long = 0,
    @SerialName("platform_fee_paise") val platformFeePaise: Long = 0,
    @SerialName("tax_paise") val taxPaise: Long = 0,
    @SerialName("surge_paise") val surgePaise: Long = 0,
    @SerialName("waiting_paise") val waitingPaise: Long = 0,
    @SerialName("discount_paise") val discountPaise: Long = 0,
    @SerialName("outstanding_paise") val outstandingPaise: Long = 0,
)

@Serializable
data class QuoteOptionDto(
    @SerialName("vehicle_type") val vehicleType: String,
    @SerialName("available") val available: Boolean = true,
    @SerialName("pickup_eta_seconds") val pickupEtaSeconds: Int = 0,
    @SerialName("distance_meters") val distanceMeters: Int = 0,
    @SerialName("duration_seconds") val durationSeconds: Int = 0,
    @SerialName("currency") val currency: String = "INR",
    @SerialName("total_paise") val totalPaise: Long = 0,
    @SerialName("breakdown") val breakdown: QuoteBreakdownDto? = null,
    @SerialName("surge_basis_points") val surgeBasisPoints: Long = 0,
    /** "none" | "peak_hours" | "high_demand". */
    @SerialName("surge_reason") val surgeReason: String? = null,
    @SerialName("window_name") val windowName: String? = null,
    @SerialName("coupon_code") val couponCode: String? = null,
    @SerialName("discount_paise") val discountPaise: Long = 0,
    @SerialName("tax_note") val taxNote: String? = null,
)

@Serializable
data class QuoteSnapshotDto(
    @SerialName("quote_id") val quoteId: String,
    @SerialName("estimated_distance_km") val estimatedDistanceKm: Double = 0.0,
    @SerialName("estimated_duration_min") val estimatedDurationMin: Double = 0.0,
    @SerialName("options") val options: List<QuoteOptionDto> = emptyList(),
    @SerialName("expires_at") val expiresAt: String? = null,
    @SerialName("coupon_code") val couponCode: String? = null,
)

/** `GET /v1/rider/coupons/validate`. An invalid coupon is a 422 error envelope, not `valid:false`. */
@Serializable
data class CouponValidationDto(
    @SerialName("valid") val valid: Boolean = false,
    @SerialName("code") val code: String = "",
    @SerialName("description") val description: String = "",
    /** "flat" | "percent" */
    @SerialName("discount_type") val discountType: String = "",
    @SerialName("discount_value_paise") val discountValuePaise: Long? = null,
    @SerialName("percent_bps") val percentBps: Long? = null,
    @SerialName("max_discount_paise") val maxDiscountPaise: Long? = null,
)

@Serializable
data class LocationDto(
    @SerialName("address") val address: String = "",
    @SerialName("lat") val lat: Double,
    @SerialName("lng") val lng: Double,
)

@Serializable
data class CreateRideRequestDto(
    @SerialName("quote_id") val quoteId: String? = null,
    @SerialName("pickup") val pickup: LocationDto,
    @SerialName("drop") val drop: LocationDto,
    @SerialName("vehicle_type") val vehicleType: String,
    @SerialName("city_id") val cityId: String? = null,
    /** cash | upi | card — chosen before booking. There is no wallet. */
    @SerialName("payment_method") val paymentMethod: String,
    @SerialName("idempotency_key") val idempotencyKey: String? = null,
    @SerialName("coupon_code") val couponCode: String? = null,
)

@Serializable
data class CaptainDto(
    @SerialName("id") val id: String = "",
    @SerialName("name") val name: String = "",
    @SerialName("phone") val phone: String = "",
    @SerialName("rating") val rating: Double = 5.0,
    @SerialName("vehicle_model") val vehicleModel: String = "",
    @SerialName("vehicle_number") val vehicleNumber: String = "",
    @SerialName("vehicle_color") val vehicleColor: String = "",
    @SerialName("profile_photo_url") val profilePhotoUrl: String = "",
)

@Serializable
data class RideDto(
    @SerialName("id") val id: String,
    @SerialName("customer_user_id") val customerUserId: String? = null,
    @SerialName("partner_id") val partnerId: String? = null,
    @SerialName("vehicle_id") val vehicleId: String? = null,
    @SerialName("quote_id") val quoteId: String? = null,
    @SerialName("revision") val revision: Int = 1,
    @SerialName("vehicle_type") val vehicleType: String = "bike",
    @SerialName("status") val status: String,
    @SerialName("pickup_address") val pickupAddress: String = "",
    @SerialName("pickup_lat") val pickupLat: Double = 0.0,
    @SerialName("pickup_lng") val pickupLng: Double = 0.0,
    @SerialName("drop_address") val dropAddress: String = "",
    @SerialName("drop_lat") val dropLat: Double = 0.0,
    @SerialName("drop_lng") val dropLng: Double = 0.0,
    @SerialName("estimated_fare_paise") val estimatedFarePaise: Long? = null,
    /** Older shape: rupees as a decimal. Read only when `estimated_fare_paise` is absent. */
    @SerialName("estimated_fare") val estimatedFare: Double? = null,
    @SerialName("final_fare_paise") val finalFarePaise: Long? = null,
    @SerialName("payment_method") val paymentMethod: String = "cash",
    @SerialName("otp") val otp: String? = null,
    @SerialName("captain") val captain: CaptainDto? = null,
    @SerialName("cancellation_fee_paise") val cancellationFeePaise: Long? = null,
    @SerialName("cancel_free_until") val cancelFreeUntil: String? = null,
    @SerialName("requested_at") val requestedAt: String? = null,
    @SerialName("created_at") val createdAt: String? = null,
    @SerialName("completed_at") val completedAt: String? = null,
)

@Serializable
data class ActiveRideResponseDto(
    @SerialName("active") val active: Boolean = false,
    @SerialName("ride") val ride: RideDto? = null,
)

@Serializable
data class ReceiptLineDto(
    @SerialName("label") val label: String = "",
    @SerialName("amount_paise") val amountPaise: Long = 0,
)

@Serializable
data class RideReceiptDto(
    @SerialName("ride_id") val rideId: String,
    @SerialName("customer_user_id") val customerUserId: String? = null,
    @SerialName("partner_id") val partnerId: String? = null,
    @SerialName("vehicle_type") val vehicleType: String = "bike",
    @SerialName("status") val status: String = "",
    @SerialName("pickup_address") val pickupAddress: String = "",
    @SerialName("drop_address") val dropAddress: String = "",
    @SerialName("distance_meters") val distanceMeters: Int = 0,
    @SerialName("duration_seconds") val durationSeconds: Int = 0,
    @SerialName("total_paise") val totalPaise: Long = 0,
    @SerialName("payment_method") val paymentMethod: String = "cash",
    @SerialName("payment_status") val paymentStatus: String = "",
    @SerialName("completed_at") val completedAt: String? = null,
    @SerialName("lines") val lines: List<ReceiptLineDto> = emptyList(),
    @SerialName("tax_note") val taxNote: String? = null,
)

@Serializable
data class ShareTokenResponseDto(
    @SerialName("token") val token: String = "",
    @SerialName("url") val url: String = "",
    @SerialName("expires_at") val expiresAt: String? = null,
)

@Serializable
data class RateRideRequestDto(
    @SerialName("rating") val rating: Int,
    @SerialName("comment") val comment: String = "",
    @SerialName("feedback") val feedback: String = "",
)

@Serializable
data class SOSRequestDto(
    @SerialName("lat") val lat: Double? = null,
    @SerialName("lng") val lng: Double? = null,
    @SerialName("reason") val reason: String = "customer_emergency",
)

@Serializable
data class CancelRideRequestDto(
    @SerialName("reason") val reason: String,
)

@Serializable
data class PaymentIntentRequestDto(
    /** upi | card */
    @SerialName("method") val method: String,
)

@Serializable
data class PaymentIntentDto(
    @SerialName("intent_id") val intentId: String = "",
    @SerialName("amount_paise") val amountPaise: Long = 0,
    @SerialName("currency") val currency: String = "INR",
    /** `{provider, order_id, key_id}` plus whatever the registry adds, relayed to the sheet as sent. */
    @SerialName("client_session") val clientSession: Map<String, String> = emptyMap(),
    @SerialName("status") val status: String = "",
)

@Serializable
data class RidePaymentDto(
    @SerialName("method") val method: String = "cash",
    /** cash_pending | cash_confirmed | confirming | paid | failed | refunded | partially_refunded */
    @SerialName("status") val status: String = "",
    @SerialName("amount_paise") val amountPaise: Long = 0,
    @SerialName("refunded_paise") val refundedPaise: Long = 0,
)

@Serializable
data class OutstandingChargeDto(
    @SerialName("id") val id: String,
    @SerialName("ride_id") val rideId: String? = null,
    @SerialName("reason") val reason: String = "",
    @SerialName("amount_paise") val amountPaise: Long = 0,
    /** pending | paid | failed — a charge that has left the list is treated as paid. */
    @SerialName("status") val status: String = "pending",
    @SerialName("created_at") val createdAt: String? = null,
)

/** The error half of the platform envelope, decoded on its own so a failed call needs no `T`. */
@Serializable
data class MopeduErrorEnvelopeDto(
    val error: MopeduErrorBodyDto? = null,
    val meta: JsonElement? = null,
)

@Serializable
data class MopeduErrorBodyDto(
    val code: String = "",
    val message: String = "",
    val details: JsonElement? = null,
)
