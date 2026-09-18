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

/** One GST line of the breakdown (`pricing.TaxLineResult`). */
@Serializable
data class TaxLineDto(
    @SerialName("ref") val ref: String = "",
    @SerialName("category") val category: String = "",
    @SerialName("sac") val sac: String = "",
    @SerialName("rate_bps") val rateBps: Long = 0,
    @SerialName("taxable_paise") val taxablePaise: Long = 0,
    @SerialName("tax_paise") val taxPaise: Long = 0,
    @SerialName("gross_paise") val grossPaise: Long = 0,
)

/**
 * `pricing.Breakdown`: the option's `breakdown` and the receipt's
 * `fare_breakdown` (estimate_peak_window_coupon.json, receipt_*.json).
 */
@Serializable
data class QuoteBreakdownDto(
    @SerialName("base_paise") val basePaise: Long = 0,
    @SerialName("distance_paise") val distancePaise: Long = 0,
    @SerialName("time_paise") val timePaise: Long = 0,
    @SerialName("surge_paise") val surgePaise: Long = 0,
    @SerialName("minimum_top_up_paise") val minimumTopUpPaise: Long = 0,
    @SerialName("ride_fare_paise") val rideFarePaise: Long = 0,
    @SerialName("discount_paise") val discountPaise: Long = 0,
    @SerialName("coupon_code") val couponCode: String? = null,
    @SerialName("coupon_id") val couponId: String? = null,
    @SerialName("taxable_ride_fare_paise") val taxableRideFarePaise: Long = 0,
    @SerialName("platform_fee_paise") val platformFeePaise: Long = 0,
    @SerialName("waiting_minutes") val waitingMinutes: Int = 0,
    @SerialName("waiting_charge_paise") val waitingChargePaise: Long = 0,
    @SerialName("outstanding_paise") val outstandingPaise: Long = 0,
    @SerialName("toll_paise") val tollPaise: Long = 0,
    @SerialName("tax_paise") val taxPaise: Long = 0,
    @SerialName("tax_lines") val taxLines: List<TaxLineDto> = emptyList(),
    @SerialName("tax_note") val taxNote: String? = null,
    @SerialName("surge_basis_points") val surgeBasisPoints: Long = 0,
    @SerialName("surge_reason") val surgeReason: String? = null,
    @SerialName("window_name") val windowName: String? = null,
    @SerialName("distance_meters") val distanceMeters: Int = 0,
    @SerialName("duration_seconds") val durationSeconds: Int = 0,
    @SerialName("total_paise") val totalPaise: Long = 0,
    @SerialName("fare_policy_version") val farePolicyVersion: Int = 0,
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
    /** Option level is `surge_bps`; the breakdown repeats it as `surge_basis_points`. */
    @SerialName("surge_bps") val surgeBps: Long = 0,
    /** "none" | "peak_hours" | "high_demand". */
    @SerialName("surge_reason") val surgeReason: String? = null,
    @SerialName("window_name") val windowName: String? = null,
    @SerialName("coupon_code") val couponCode: String? = null,
    @SerialName("discount_paise") val discountPaise: Long = 0,
)

/** `POST /v1/rider/estimate` (`service.FareEstimateResult`). The app prices from [options]; the flat fields are the legacy summary. */
@Serializable
data class QuoteSnapshotDto(
    @SerialName("quote_id") val quoteId: String,
    @SerialName("estimated_distance_km") val estimatedDistanceKm: Double = 0.0,
    @SerialName("estimated_duration_min") val estimatedDurationMin: Double = 0.0,
    @SerialName("fare_estimate_paise") val fareEstimatePaise: Long = 0,
    @SerialName("surge_multiplier") val surgeMultiplier: Double = 1.0,
    @SerialName("surge_bps") val surgeBps: Long = 0,
    @SerialName("surge_reason") val surgeReason: String? = null,
    @SerialName("window_name") val windowName: String? = null,
    @SerialName("discount_paise") val discountPaise: Long = 0,
    @SerialName("coupon_code") val couponCode: String? = null,
    @SerialName("outstanding_paise") val outstandingPaise: Long = 0,
    @SerialName("tax_note") val taxNote: String? = null,
    @SerialName("vehicle_type") val vehicleType: String? = null,
    @SerialName("eta_to_pickup_seconds") val etaToPickupSeconds: Int = 0,
    @SerialName("base_fare_inr") val baseFareInr: Double = 0.0,
    @SerialName("per_km_inr") val perKmInr: Double = 0.0,
    @SerialName("per_minute_inr") val perMinuteInr: Double = 0.0,
    @SerialName("minimum_fare_inr") val minimumFareInr: Double = 0.0,
    @SerialName("fare_estimate_inr") val fareEstimateInr: Double = 0.0,
    @SerialName("options") val options: List<QuoteOptionDto> = emptyList(),
    @SerialName("expires_at") val expiresAt: String? = null,
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

/**
 * One refund. The receipt's `refunds[]` carries the first five keys
 * (`store.ReceiptRefund`); the admin list row (`store.RideRefund`,
 * refund_list_row.json) adds the rest, so one DTO decodes both strictly.
 */
@Serializable
data class RideRefundDto(
    @SerialName("id") val id: String,
    @SerialName("amount_paise") val amountPaise: Long = 0,
    /** pending | refunded | failed */
    @SerialName("status") val status: String = "",
    @SerialName("reason") val reason: String = "",
    @SerialName("created_at") val createdAt: String? = null,
    @SerialName("ride_id") val rideId: String? = null,
    @SerialName("payment_id") val paymentId: String? = null,
    @SerialName("intent_id") val intentId: String? = null,
    @SerialName("requested_by") val requestedBy: String? = null,
    @SerialName("provider_reference") val providerReference: String? = null,
    @SerialName("updated_at") val updatedAt: String? = null,
)

/** `{items: [...]}` of the admin refund list. Not called by this app; pinned so the row shape stays in step with [RideRefundDto]. */
@Serializable
data class RefundListDto(
    @SerialName("items") val items: List<RideRefundDto> = emptyList(),
)

/**
 * `GET /v1/rider/rides/{id}/receipt` (`store.RideReceipt`). `payment` is the
 * ride's latest payment row (null before completion) and is the status the
 * app trusts: the flat `payment_status` is the legacy column and can carry
 * strings such as `pending_cash_confirmation` that are not [RidePaymentDto]
 * statuses. `fare_breakdown` is the same shape as the quote option's breakdown.
 */
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
    @SerialName("surge_bps") val surgeBps: Long = 0,
    @SerialName("surge_reason") val surgeReason: String? = null,
    @SerialName("discount_paise") val discountPaise: Long = 0,
    @SerialName("coupon_code") val couponCode: String? = null,
    @SerialName("waiting_charge_paise") val waitingChargePaise: Long = 0,
    @SerialName("outstanding_paise") val outstandingPaise: Long = 0,
    @SerialName("cancellation_fee_paise") val cancellationFeePaise: Long = 0,
    @SerialName("tax_paise") val taxPaise: Long = 0,
    @SerialName("tax_note") val taxNote: String? = null,
    @SerialName("payment_method") val paymentMethod: String = "cash",
    @SerialName("payment_status") val paymentStatus: String = "",
    @SerialName("payment") val payment: RidePaymentDto? = null,
    @SerialName("refunds") val refunds: List<RideRefundDto> = emptyList(),
    @SerialName("fare_breakdown") val fareBreakdown: QuoteBreakdownDto? = null,
    /** Captain telemetry at completion. Never prices the ride. */
    @SerialName("reported_distance_km") val reportedDistanceKm: Double? = null,
    @SerialName("reported_duration_min") val reportedDurationMin: Double? = null,
    @SerialName("tracked_distance_m") val trackedDistanceM: Int? = null,
    @SerialName("completed_at") val completedAt: String? = null,
    @SerialName("created_at") val createdAt: String? = null,
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

/** `GET /v1/rider/rides/{id}/payment` (`service.RidePaymentStatus`), and the receipt's `payment` block (`store.ReceiptPayment`). */
@Serializable
data class RidePaymentDto(
    @SerialName("method") val method: String = "cash",
    /** cash_pending | cash_confirmed | pending | confirming | paid | failed | refunded | partially_refunded */
    @SerialName("status") val status: String = "",
    @SerialName("amount_paise") val amountPaise: Long = 0,
    @SerialName("refunded_paise") val refundedPaise: Long = 0,
    /** The open payments intent; null for cash and before an intent exists. Absent on the receipt block. */
    @SerialName("intent_id") val intentId: String? = null,
    @SerialName("updated_at") val updatedAt: String? = null,
)

/**
 * One row of `GET /v1/rider/me/outstanding` (`store.CustomerOutstanding`): a
 * plain array, `[]` when nothing is owed. Checked against handler_payments.go
 * on 2026-09-18; no fixture exists for it yet.
 */
@Serializable
data class OutstandingChargeDto(
    @SerialName("id") val id: String,
    @SerialName("customer_user_id") val customerUserId: String? = null,
    @SerialName("ride_id") val rideId: String? = null,
    @SerialName("reason") val reason: String = "",
    @SerialName("amount_paise") val amountPaise: Long = 0,
    /** pending | paid | failed — a charge that has left the list is treated as paid. */
    @SerialName("status") val status: String = "pending",
    @SerialName("settled_by_ride_id") val settledByRideId: String? = null,
    @SerialName("waived_by") val waivedBy: String? = null,
    @SerialName("waive_reason") val waiveReason: String? = null,
    /** The open intent for paying the fee directly, when one exists. */
    @SerialName("intent_id") val intentId: String? = null,
    @SerialName("intent_method") val intentMethod: String? = null,
    @SerialName("settled_intent_id") val settledIntentId: String? = null,
    @SerialName("created_at") val createdAt: String? = null,
    @SerialName("settled_at") val settledAt: String? = null,
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
