package com.us.android.core.mobility.model

/**
 * Pure Kotlin/JVM domain models for Mopedu Mobility.
 * No Android dependencies, no serialization annotations, all money in integer paise.
 */

data class GeoPoint(
    val lat: Double,
    val lng: Double,
    val address: String = "",
    val label: String = "",
    val placeId: String = "",
)

data class MoneyPaise(val paise: Long) {
    val rupees: Double get() = paise / 100.0
    val formattedINR: String
        get() {
            val inr = paise / 100.0
            return if (paise % 100 == 0L) {
                "₹${paise / 100}"
            } else {
                String.format("₹%.2f", inr)
            }
        }

    operator fun plus(other: MoneyPaise) = MoneyPaise(paise + other.paise)
    operator fun minus(other: MoneyPaise) = MoneyPaise(paise - other.paise)
}

enum class VehicleType(val code: String, val displayName: String) {
    BIKE("bike", "Mopedu Bike"),
    AUTO("auto", "Mopedu Auto"),
    MINI_CAB("mini_cab", "Mini Cab"),
    SEDAN("sedan", "Sedan"),
    SUV("suv", "SUV"),
    PREMIUM("premium", "Premium Cab"),
    EV_BIKE("ev_bike", "Electric Bike"),
    EV_CAR("ev_car", "Electric Car");

    companion object {
        fun fromCode(code: String): VehicleType {
            return entries.firstOrNull { it.code.equals(code, ignoreCase = true) } ?: BIKE
        }
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
    SCHEDULED("scheduled");

    val isTerminal: Boolean
        get() = this in setOf(
            COMPLETED,
            CANCELLED_BY_CUSTOMER,
            CANCELLED_BY_PARTNER,
            CANCELLED_BY_ADMIN,
            EXPIRED,
            FAILED,
        )

    val isActive: Boolean get() = !isTerminal && this != SCHEDULED

    companion object {
        fun fromCode(code: String): RideStatus {
            return entries.firstOrNull { it.code.equals(code, ignoreCase = true) } ?: REQUESTED
        }
    }
}

data class QuoteBreakdown(
    val basePaise: Long,
    val distancePaise: Long,
    val timePaise: Long,
    val platformFeePaise: Long,
    val taxPaise: Long,
    val tollPaise: Long,
    val surgeBasisPoints: Long,
)

data class QuoteOption(
    val vehicleType: VehicleType,
    val available: Boolean,
    val pickupETASeconds: Int,
    val distanceMeters: Int,
    val durationSeconds: Int,
    val totalFare: MoneyPaise,
    val breakdown: QuoteBreakdown,
)

data class QuoteSnapshot(
    val quoteId: String,
    val pickup: GeoPoint,
    val drop: GeoPoint,
    val distanceMeters: Int,
    val durationSeconds: Int,
    val options: List<QuoteOption>,
    val expiresAtEpochMs: Long,
) {
    val isExpired: Boolean get() = System.currentTimeMillis() > expiresAtEpochMs
}

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
    val paymentMethod: String,
    val otp: String?, // Only exposed to rider
    val captain: CaptainInfo? = null,
    val requestedAtEpochMs: Long,
    val completedAtEpochMs: Long? = null,
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
    val paymentMethod: String,
    val paymentStatus: String,
    val completedAtEpochMs: Long?,
)

data class CaptainOffer(
    val id: String,
    val rideId: String,
    val pickup: GeoPoint,
    val drop: GeoPoint,
    val distanceKM: Double,
    val estimatedEarnings: MoneyPaise,
    val score: Double,
    val expiresAtEpochMs: Long,
)

data class CaptainState(
    val isOnline: Boolean,
    val activeRideId: String?,
    val rating: Double,
    val totalRidesCompleted: Int,
    val todayEarnings: MoneyPaise,
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
    val status: String, // 'draft' | 'submitted' | 'under_review' | 'approved' | 'rejected' | 'suspended' | 'blocked'
    val kycStatus: String, // 'pending' | 'submitted' | 'under_review' | 'approved' | 'rejected'
    val cityId: String? = null,
    val profilePhotoUrl: String? = null,
    val rating: Double = 5.0,
    val ridesCompleted: Int = 0,
)

data class PartnerDocument(
    val id: String,
    val partnerId: String,
    val documentType: String, // 'driving_license' | 'aadhaar' | 'pan' | 'police_verification' | 'vehicle_rc' | 'vehicle_insurance'
    val documentNumber: String? = null,
    val fileUrl: String,
    val status: String, // 'submitted' | 'verified' | 'rejected'
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
    val status: String, // 'pending_review' | 'approved' | 'rejected'
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
)

data class PartnerSubscription(
    val id: String,
    val partnerId: String,
    val planId: String,
    val planCode: String,
    val planName: String,
    val status: String, // 'trial' | 'active' | 'grace_period' | 'expired' | 'cancelled'
    val leadsUsed: Int,
    val dailyLeadCap: Int?,
    val startsAt: String,
    val expiresAt: String,
)

