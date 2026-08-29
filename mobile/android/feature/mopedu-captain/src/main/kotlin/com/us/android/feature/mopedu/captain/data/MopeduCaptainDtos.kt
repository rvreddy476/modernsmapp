package com.us.android.feature.mopedu.captain.data

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class LocationTelemetryDto(
    @SerialName("lat") val lat: Double,
    @SerialName("lng") val lng: Double,
    @SerialName("speed_mps") val speedMps: Double = 0.0,
    @SerialName("heading") val heading: Double = 0.0,
    @SerialName("accuracy_meters") val accuracyMeters: Double = 5.0,
    @SerialName("sequence") val sequence: Long = 0,
)

@Serializable
data class CaptainOfferDto(
    @SerialName("id") val id: String,
    @SerialName("ride_id") val rideId: String,
    @SerialName("score") val score: Double = 0.0,
    @SerialName("distance_km") val distanceKm: Double = 0.0,
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
    @SerialName("partner_id") val partnerId: String,
    @SerialName("status") val status: String,
    @SerialName("otp_expires_at") val otpExpiresAt: String? = null,
)

@Serializable
data class StartRideRequestDto(
    @SerialName("otp") val otp: String,
)

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
)

// --- Onboarding & Partner Profile DTOs ---

@Serializable
data class PartnerProfileDto(
    @SerialName("id") val id: String,
    @SerialName("user_id") val userId: String? = null,
    @SerialName("partner_type") val partnerType: String,
    @SerialName("full_name") val fullName: String,
    @SerialName("phone") val phone: String,
    @SerialName("email") val email: String? = null,
    @SerialName("status") val status: String,
    @SerialName("kyc_status") val kycStatus: String,
    @SerialName("city_id") val cityId: String? = null,
    @SerialName("profile_photo_url") val profilePhotoUrl: String? = null,
    @SerialName("rating") val rating: Double = 5.0,
    @SerialName("rides_completed") val ridesCompleted: Int = 0,
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
    @SerialName("partner_id") val partnerId: String,
    @SerialName("document_type") val documentType: String,
    @SerialName("document_number") val documentNumber: String? = null,
    @SerialName("file_url") val fileUrl: String,
    @SerialName("status") val status: String,
    @SerialName("rejection_reason") val rejectionReason: String? = null,
    @SerialName("expires_at") val expiresAt: String? = null,
)

@Serializable
data class SubmitDocumentRequestDto(
    @SerialName("document_type") val documentType: String,
    @SerialName("document_number") val documentNumber: String? = null,
    @SerialName("file_url") val fileUrl: String,
    @SerialName("expires_at") val expiresAt: String? = null,
)

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
    @SerialName("partner_id") val partnerId: String,
    @SerialName("vehicle_type") val vehicleType: String,
    @SerialName("registration_number") val registrationNumber: String,
    @SerialName("brand") val brand: String? = null,
    @SerialName("model") val model: String? = null,
    @SerialName("color") val color: String? = null,
    @SerialName("manufacture_year") val manufactureYear: Int? = null,
    @SerialName("year") val year: Int? = null,
    @SerialName("status") val status: String,
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

@Serializable
data class SubscriptionPlanDto(
    @SerialName("id") val id: String,
    @SerialName("code") val code: String,
    @SerialName("name") val name: String,
    @SerialName("vehicle_type") val vehicleType: String,
    @SerialName("billing_cycle") val billingCycle: String,
    @SerialName("price_paise") val pricePaise: Long,
    @SerialName("daily_lead_cap") val dailyLeadCap: Int? = null,
    @SerialName("priority_score") val priorityScore: Int = 10,
    @SerialName("description") val description: String = "",
)

@Serializable
data class SubscribeRequestDto(
    @SerialName("plan_id") val planId: String,
    @SerialName("payment_method") val paymentMethod: String = "wallet", // 'wallet' | 'upi'
    @SerialName("idempotency_key") val idempotencyKey: String,
)

@Serializable
data class SubscribeResponseDto(
    @SerialName("subscription_id") val subscriptionId: String,
    @SerialName("payment_id") val paymentId: String? = null,
    @SerialName("status") val status: String,
    @SerialName("upi_intent") val upiIntent: String? = null,
    @SerialName("starts_at") val startsAt: String? = null,
    @SerialName("expires_at") val expiresAt: String? = null,
)

@Serializable
data class PaymentProofRequestDto(
    @SerialName("payment_id") val paymentId: String,
    @SerialName("file_url") val fileUrl: String,
)

@Serializable
data class PartnerSubscriptionDto(
    @SerialName("id") val id: String,
    @SerialName("partner_id") val partnerId: String,
    @SerialName("plan_id") val planId: String,
    @SerialName("plan_code") val planCode: String = "",
    @SerialName("plan_name") val planName: String = "",
    @SerialName("status") val status: String,
    @SerialName("leads_used") val leadsUsed: Int = 0,
    @SerialName("daily_lead_cap") val dailyLeadCap: Int? = null,
    @SerialName("starts_at") val startsAt: String = "",
    @SerialName("expires_at") val expiresAt: String = "",
)

