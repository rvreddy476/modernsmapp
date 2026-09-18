package com.us.android.core.mobility.model

import java.util.Locale

/*
 * Pure Kotlin/JVM domain models for Mopedu (ride-hailing).
 *
 * No Android dependencies, no serialization annotations; every amount is an
 * integer number of paise. DTOs and their wire names live in each feature's
 * data layer and map onto these.
 */

data class GeoPoint(
    val lat: Double,
    val lng: Double,
    val address: String = "",
    val label: String = "",
    val placeId: String = "",
)

/** Money as integer paise. `₹150` for a whole rupee amount, `₹150.50` otherwise. */
data class MoneyPaise(val paise: Long) {
    val rupees: Double get() = paise / PAISE_PER_RUPEE_D

    val isZero: Boolean get() = paise == 0L

    val formattedINR: String
        get() = if (paise % PAISE_PER_RUPEE == 0L) {
            "₹${paise / PAISE_PER_RUPEE}"
        } else {
            String.format(Locale.ENGLISH, "₹%.2f", rupees)
        }

    operator fun plus(other: MoneyPaise) = MoneyPaise(paise + other.paise)
    operator fun minus(other: MoneyPaise) = MoneyPaise(paise - other.paise)

    companion object {
        val ZERO = MoneyPaise(0L)
        private const val PAISE_PER_RUPEE = 100L
        private const val PAISE_PER_RUPEE_D = 100.0
    }
}

enum class VehicleType(val code: String, val displayName: String) {
    BIKE("bike", "Mopedu Bike"),
    AUTO("auto", "Mopedu Auto"),
    MINI_CAB("mini_cab", "Mini Cab"),
    SEDAN("sedan", "Sedan"),
    SUV("suv", "SUV"),
    PREMIUM("premium", "Premium Cab"),
    EV_BIKE("ev_bike", "Electric Bike"),
    EV_CAR("ev_car", "Electric Car"),
    ;

    companion object {
        fun fromCode(code: String): VehicleType =
            entries.firstOrNull { it.code.equals(code, ignoreCase = true) } ?: BIKE
    }
}

enum class RideStatus(val code: String) {
    REQUESTED("requested"),
    SEARCHING_PARTNER("searching_partner"),
    PARTNER_ASSIGNED("partner_assigned"),
    PARTNER_ARRIVING("partner_arriving"),
    ARRIVED("arrived"),
    OTP_VERIFIED("otp_verified"),
    IN_PROGRESS("in_progress"),
    COMPLETED("completed"),
    CANCELLED_BY_CUSTOMER("cancelled_by_customer"),
    CANCELLED_BY_PARTNER("cancelled_by_partner"),
    CANCELLED_BY_ADMIN("cancelled_by_admin"),
    EXPIRED("expired"),
    FAILED("failed"),
    SCHEDULED("scheduled"),
    ;

    val isTerminal: Boolean
        get() = this in setOf(COMPLETED, CANCELLED_BY_CUSTOMER, CANCELLED_BY_PARTNER, CANCELLED_BY_ADMIN, EXPIRED, FAILED)

    val isCancelled: Boolean
        get() = this in setOf(CANCELLED_BY_CUSTOMER, CANCELLED_BY_PARTNER, CANCELLED_BY_ADMIN)

    val isActive: Boolean get() = !isTerminal && this != SCHEDULED

    companion object {
        fun fromCode(code: String): RideStatus =
            entries.firstOrNull { it.code.equals(code, ignoreCase = true) } ?: REQUESTED
    }
}

/** How the customer pays. Chosen BEFORE booking; there is no wallet. */
enum class PaymentMethod(val code: String, val displayName: String) {
    CASH("cash", "Cash"),
    UPI("upi", "UPI"),
    CARD("card", "Card"),
    ;

    /** Cash is settled by the captain's confirmation; UPI and card go through the payment sheet. */
    val isOnline: Boolean get() = this != CASH

    companion object {
        fun fromCode(code: String?): PaymentMethod =
            entries.firstOrNull { it.code.equals(code, ignoreCase = true) } ?: CASH
    }
}

/**
 * Why the fare is above the window's base. `NONE` renders no chip; the other
 * two are the only reasons the server names today, and an unknown reason is
 * shown as generic demand pricing rather than hidden.
 */
enum class SurgeReason(val code: String, val chipLabel: String?) {
    NONE("none", null),
    PEAK_HOURS("peak_hours", "Peak hours"),
    HIGH_DEMAND("high_demand", "High demand"),
    OTHER("", "Demand pricing"),
    ;

    companion object {
        fun fromCode(code: String?): SurgeReason = when {
            code.isNullOrBlank() -> NONE
            else -> entries.firstOrNull { it != OTHER && it.code.equals(code, ignoreCase = true) } ?: OTHER
        }
    }
}

/**
 * The server's fare breakdown for one option, every line in paise. A line the
 * server did not send is zero. `outstanding` is a previous cancellation fee
 * folded into this fare; `discount` is the coupon's reduction.
 */
data class QuoteBreakdown(
    val basePaise: Long = 0,
    val distancePaise: Long = 0,
    val timePaise: Long = 0,
    val platformFeePaise: Long = 0,
    val taxPaise: Long = 0,
    val surgePaise: Long = 0,
    val waitingChargePaise: Long = 0,
    val tollPaise: Long = 0,
    val discountPaise: Long = 0,
    val outstandingPaise: Long = 0,
)

data class QuoteOption(
    val vehicleType: VehicleType,
    val available: Boolean,
    val pickupETASeconds: Int,
    val distanceMeters: Int,
    val durationSeconds: Int,
    val totalFare: MoneyPaise,
    val breakdown: QuoteBreakdown,
    val surgeBasisPoints: Long = 0,
    val surgeReason: SurgeReason = SurgeReason.NONE,
    /** The pricing window the server priced in ("standard", "night", …), for the receipt line. */
    val windowName: String? = null,
    val couponCode: String? = null,
    val discount: MoneyPaise = MoneyPaise.ZERO,
    val taxNote: String? = null,
) {
    /** True only when the server named a surge reason: a `none` reason never shows a chip. */
    val showsSurgeChip: Boolean get() = surgeReason != SurgeReason.NONE

    val hasDiscount: Boolean get() = discount.paise > 0

    val outstanding: MoneyPaise get() = MoneyPaise(breakdown.outstandingPaise)

    val includesOutstanding: Boolean get() = breakdown.outstandingPaise > 0
}

data class QuoteSnapshot(
    val quoteId: String,
    val pickup: GeoPoint,
    val drop: GeoPoint,
    val distanceMeters: Int,
    val durationSeconds: Int,
    val options: List<QuoteOption>,
    val expiresAtEpochMs: Long,
    /** The coupon this quote was priced with, echoed by the server; null when none. */
    val couponCode: String? = null,
) {
    fun isExpiredAt(nowEpochMs: Long): Boolean = nowEpochMs > expiresAtEpochMs
}

/** `GET /v1/rider/coupons/validate` when the coupon applies. */
data class CouponValidation(
    val code: String,
    val description: String,
    val discountType: String,
    val discountValue: MoneyPaise? = null,
    val percentBasisPoints: Long? = null,
    val maxDiscount: MoneyPaise? = null,
)

data class CaptainInfo(
    val id: String,
    val name: String,
    val phone: String,
    val rating: Double,
    val vehicleModel: String,
    val vehicleNumber: String,
    val vehicleColor: String,
    val profilePhotoUrl: String = "",
)

data class RideBooking(
    val id: String,
    val customerUserId: String,
    val partnerId: String?,
    val vehicleId: String?,
    val quoteId: String?,
    val revision: Int,
    val vehicleType: VehicleType,
    val status: RideStatus,
    val pickup: GeoPoint,
    val drop: GeoPoint,
    val estimatedFare: MoneyPaise,
    val finalFare: MoneyPaise?,
    val paymentMethod: PaymentMethod,
    /** Only exposed to the customer; the captain types it in. */
    val otp: String?,
    val captain: CaptainInfo? = null,
    val requestedAtEpochMs: Long,
    val completedAtEpochMs: Long? = null,
    /** The fee charged for cancelling after [cancelFreeUntilEpochMs]; null when the server set none. */
    val cancellationFee: MoneyPaise? = null,
    /** Cancelling at or before this instant is free; null means the server gave no window. */
    val cancelFreeUntilEpochMs: Long? = null,
) {
    /** What cancelling now costs, from the ride's own rule. See [CancellationRule]. */
    fun cancellationFeeAt(nowEpochMs: Long): MoneyPaise = CancellationRule.feeAt(this, nowEpochMs)
}

/**
 * The cancellation-fee rule the ride carries, applied on the device only to
 * SHOW the customer what the server will charge. The server charges.
 */
object CancellationRule {
    fun feeAt(booking: RideBooking, nowEpochMs: Long): MoneyPaise {
        val fee = booking.cancellationFee ?: return MoneyPaise.ZERO
        val freeUntil = booking.cancelFreeUntilEpochMs ?: return fee
        return if (nowEpochMs <= freeUntil) MoneyPaise.ZERO else fee
    }

    /** Whole seconds left in the free window, or null when there is no window / it has passed. */
    fun freeSecondsLeft(booking: RideBooking, nowEpochMs: Long): Long? {
        val freeUntil = booking.cancelFreeUntilEpochMs ?: return null
        val left = (freeUntil - nowEpochMs) / MILLIS_PER_SECOND
        return left.takeIf { it > 0 }
    }

    private const val MILLIS_PER_SECOND = 1_000L
}

/** `GET /v1/rider/rides/{id}/payment`. */
enum class RidePaymentStatus(val code: String) {
    CASH_PENDING("cash_pending"),
    CASH_CONFIRMED("cash_confirmed"),
    CONFIRMING("confirming"),
    PAID("paid"),
    FAILED("failed"),
    REFUNDED("refunded"),
    PARTIALLY_REFUNDED("partially_refunded"),

    /** A status this build does not know. Treated as still confirming, never as paid. */
    UNKNOWN(""),
    ;

    /** The ride's money is settled: paid online, or cash the captain confirmed. */
    val isSettled: Boolean get() = this == PAID || this == CASH_CONFIRMED

    val isRefund: Boolean get() = this == REFUNDED || this == PARTIALLY_REFUNDED

    companion object {
        fun fromCode(code: String?): RidePaymentStatus =
            entries.firstOrNull { it != UNKNOWN && it.code.equals(code, ignoreCase = true) } ?: UNKNOWN
    }
}

data class RidePayment(
    val method: PaymentMethod,
    val status: RidePaymentStatus,
    val amount: MoneyPaise,
    val refunded: MoneyPaise = MoneyPaise.ZERO,
)

/** `POST /v1/rider/rides/{id}/payment/intent`: what the payment sheet opens with. */
data class RidePaymentIntent(
    val intentId: String,
    val amount: MoneyPaise,
    val currency: String,
    /** The server's `client_session` — provider, order id, key id — relayed as sent. */
    val clientSession: Map<String, String>,
    val status: String,
)

/** One line of the itemised receipt, derived from the server's fare breakdown. */
data class ReceiptLine(val label: String, val amount: MoneyPaise)

/** One refund filed against the ride's payment, as the receipt lists it. */
data class ReceiptRefund(
    val id: String,
    val amount: MoneyPaise,
    /** pending | refunded | failed */
    val status: String,
    val reason: String,
    val createdAtEpochMs: Long?,
)

data class RideReceipt(
    val rideId: String,
    val customerUserId: String,
    val partnerId: String?,
    val vehicleType: VehicleType,
    val status: String,
    val pickupAddress: String,
    val dropAddress: String,
    val distanceMeters: Int,
    val durationSeconds: Int,
    val totalFare: MoneyPaise,
    val paymentMethod: PaymentMethod,
    /** From the receipt's payment block when present; the legacy flat column otherwise. */
    val paymentStatus: RidePaymentStatus,
    val completedAtEpochMs: Long?,
    val lines: List<ReceiptLine> = emptyList(),
    val taxNote: String? = null,
    /** The ride's latest payment row; null before completion. */
    val payment: RidePayment? = null,
    val refunds: List<ReceiptRefund> = emptyList(),
    val breakdown: QuoteBreakdown = QuoteBreakdown(),
) {
    val totalRefunded: MoneyPaise get() = payment?.refunded ?: MoneyPaise.ZERO
}

/** `GET /v1/rider/me/outstanding`: a cancellation fee still to be paid. */
data class OutstandingCharge(
    val id: String,
    val rideId: String?,
    val reason: String,
    val amount: MoneyPaise,
    val createdAtEpochMs: Long?,
    /** pending | paid | failed, as the server lists it. */
    val status: String = "pending",
) {
    val isPending: Boolean get() = status != "paid"
}

data class CaptainOffer(
    val id: String,
    val rideId: String,
    val pickup: GeoPoint,
    val drop: GeoPoint,
    val distanceKM: Double,
    val estimatedEarnings: MoneyPaise,
    val score: Double,
    val expiresAtEpochMs: Long,
    val vehicleType: VehicleType = VehicleType.BIKE,
    val paymentMethod: PaymentMethod = PaymentMethod.CASH,
) {
    fun secondsLeftAt(nowEpochMs: Long): Long = ((expiresAtEpochMs - nowEpochMs) / MILLIS_PER_SECOND).coerceAtLeast(0)

    fun isExpiredAt(nowEpochMs: Long): Boolean = nowEpochMs >= expiresAtEpochMs

    private companion object {
        const val MILLIS_PER_SECOND = 1_000L
    }
}

data class CaptainState(
    val isOnline: Boolean,
    val activeRideId: String?,
    val rating: Double,
    val totalRidesCompleted: Int,
    val todayEarnings: MoneyPaise,
    val todayRides: Int = 0,
    val totalEarnings: MoneyPaise = MoneyPaise.ZERO,
)

data class CaptainTelemetry(
    val lat: Double,
    val lng: Double,
    val speedMps: Double,
    val heading: Double,
    val accuracyMeters: Double,
    val sequence: Long,
    val timestampEpochMs: Long,
)

data class SOSIncident(
    val id: String,
    val rideId: String,
    val triggeredBy: String,
    val severity: String,
    val status: String,
    val createdAtEpochMs: Long,
)

data class PartnerProfile(
    val id: String,
    val partnerType: String,
    val fullName: String,
    val phone: String,
    val email: String? = null,
    /** draft | submitted | under_review | approved | rejected | suspended | blocked */
    val status: String,
    /** pending | submitted | under_review | approved | rejected */
    val kycStatus: String,
    val cityId: String? = null,
    val profilePhotoUrl: String? = null,
    val rating: Double = DEFAULT_RATING,
    val ridesCompleted: Int = 0,
) {
    companion object {
        const val DEFAULT_RATING = 5.0
    }
}

data class PartnerDocument(
    val id: String,
    val partnerId: String,
    /** driving_license | aadhaar | pan | police_verification | vehicle_rc | vehicle_insurance */
    val documentType: String,
    val documentNumber: String? = null,
    val fileUrl: String,
    /** submitted | verified | rejected */
    val status: String,
    val rejectionReason: String? = null,
    val expiresAt: String? = null,
)

data class Vehicle(
    val id: String,
    val partnerId: String,
    val vehicleType: VehicleType,
    val registrationNumber: String,
    val brand: String? = null,
    val model: String? = null,
    val color: String? = null,
    val manufactureYear: Int? = null,
    /** pending_review | approved | rejected */
    val status: String,
)

data class SubscriptionPlan(
    val id: String,
    val code: String,
    val name: String,
    val vehicleType: String,
    val billingCycle: String,
    val price: MoneyPaise,
    val dailyLeadCap: Int?,
    val priorityScore: Int,
    val description: String = "",
    /** How long one payment covers (`billing_period_days`, 2026-09-18); 0 when the server did not say. */
    val billingPeriodDays: Int = 0,
) {
    /** The free trial: price zero, granted once ever, activated without a payment sheet. */
    val isTrial: Boolean get() = code == TRIAL_CODE || price.isZero

    companion object {
        const val TRIAL_CODE = "trial_7d"
    }
}

data class PartnerSubscription(
    val id: String,
    val partnerId: String,
    val planId: String,
    val planCode: String,
    val planName: String,
    /** trial | active | grace_period | expired | cancelled */
    val status: String,
    val leadsUsed: Int,
    val dailyLeadCap: Int?,
    val startsAt: String,
    val expiresAt: String,
) {
    val isUsable: Boolean get() = status == "trial" || status == "active"
}
