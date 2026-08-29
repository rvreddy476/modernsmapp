package com.us.android.feature.mopedu.rider.data

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

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
)

@Serializable
data class QuoteBreakdownDto(
    @SerialName("base_paise") val basePaise: Long = 0,
    @SerialName("distance_paise") val distancePaise: Long = 0,
    @SerialName("time_paise") val timePaise: Long = 0,
    @SerialName("platform_fee_paise") val platformFeePaise: Long = 0,
    @SerialName("tax_paise") val taxPaise: Long = 0,
    @SerialName("toll_paise") val tollPaise: Long = 0,
    @SerialName("surge_basis_points") val surgeBasisPoints: Long = 0,
)

@Serializable
data class QuoteOptionDto(
    @SerialName("vehicle_type") val vehicleType: String,
    @SerialName("available") val available: Boolean = true,
    @SerialName("pickup_eta_seconds") val pickupETASeconds: Int = 0,
    @SerialName("distance_meters") val distanceMeters: Int = 0,
    @SerialName("duration_seconds") val durationSeconds: Int = 0,
    @SerialName("currency") val currency: String = "INR",
    @SerialName("total_paise") val totalPaise: Long = 0,
    @SerialName("breakdown") val breakdown: QuoteBreakdownDto? = null,
)

@Serializable
data class QuoteSnapshotDto(
    @SerialName("quote_id") val quoteId: String,
    @SerialName("estimated_distance_km") val estimatedDistanceKm: Double = 0.0,
    @SerialName("estimated_duration_min") val estimatedDurationMin: Double = 0.0,
    @SerialName("options") val options: List<QuoteOptionDto> = emptyList(),
    @SerialName("expires_at") val expiresAt: String? = null,
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
    @SerialName("payment_method") val paymentMethod: String = "cash",
    @SerialName("idempotency_key") val idempotencyKey: String? = null,
)

@Serializable
data class RideDto(
    @SerialName("id") val id: String,
    @SerialName("customer_user_id") val customerUserId: String? = null,
    @SerialName("partner_id") val partnerId: String? = null,
    @SerialName("vehicle_id") val vehicleId: String? = null,
    @SerialName("quote_id") val quoteId: String? = null,
    @SerialName("revision") val revision: Int = 1,
    @SerialName("vehicle_type") val vehicleType: String,
    @SerialName("status") val status: String,
    @SerialName("pickup_address") val pickupAddress: String = "",
    @SerialName("pickup_lat") val pickupLat: Double = 0.0,
    @SerialName("pickup_lng") val pickupLng: Double = 0.0,
    @SerialName("drop_address") val dropAddress: String = "",
    @SerialName("drop_lat") val dropLat: Double = 0.0,
    @SerialName("drop_lng") val dropLng: Double = 0.0,
    @SerialName("estimated_fare") val estimatedFare: Double = 0.0,
    @SerialName("final_fare_paise") val finalFarePaise: Long? = null,
    @SerialName("payment_method") val paymentMethod: String = "cash",
    @SerialName("otp") val otp: String? = null,
    @SerialName("requested_at") val requestedAt: String? = null,
    @SerialName("created_at") val createdAt: String? = null,
)

@Serializable
data class ActiveRideResponseDto(
    @SerialName("active") val active: Boolean = false,
    @SerialName("ride") val ride: RideDto? = null,
)

@Serializable
data class RideReceiptDto(
    @SerialName("ride_id") val rideId: String,
    @SerialName("customer_user_id") val customerUserId: String? = null,
    @SerialName("partner_id") val partnerId: String? = null,
    @SerialName("vehicle_type") val vehicleType: String,
    @SerialName("status") val status: String,
    @SerialName("pickup_address") val pickupAddress: String = "",
    @SerialName("drop_address") val dropAddress: String = "",
    @SerialName("distance_meters") val distanceMeters: Int = 0,
    @SerialName("duration_seconds") val durationSeconds: Int = 0,
    @SerialName("total_paise") val totalPaise: Long = 0,
    @SerialName("payment_method") val paymentMethod: String = "cash",
    @SerialName("payment_status") val paymentStatus: String = "pending",
    @SerialName("completed_at") val completedAt: String? = null,
)

@Serializable
data class ShareTokenResponseDto(
    @SerialName("token") val token: String,
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

