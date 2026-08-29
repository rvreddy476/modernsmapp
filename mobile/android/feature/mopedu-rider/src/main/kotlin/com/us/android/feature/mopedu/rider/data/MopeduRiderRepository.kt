package com.us.android.feature.mopedu.rider.data

import com.us.android.core.mobility.model.GeoPoint
import com.us.android.core.mobility.model.MoneyPaise
import com.us.android.core.mobility.model.QuoteBreakdown
import com.us.android.core.mobility.model.QuoteOption
import com.us.android.core.mobility.model.QuoteSnapshot
import com.us.android.core.mobility.model.RideBooking
import com.us.android.core.mobility.model.RideReceipt
import com.us.android.core.mobility.model.RideStatus
import com.us.android.core.mobility.model.VehicleType
import java.util.UUID
import javax.inject.Inject
import javax.inject.Singleton

interface MopeduRiderRepository {
    suspend fun getQuote(pickup: GeoPoint, drop: GeoPoint): Result<QuoteSnapshot>
    suspend fun bookRide(quoteId: String, pickup: GeoPoint, drop: GeoPoint, vehicleType: VehicleType): Result<RideBooking>
    suspend fun getActiveRide(): Result<RideBooking?>
    suspend fun getReceipt(rideId: String): Result<RideReceipt>
    suspend fun triggerSOS(rideId: String, lat: Double, lng: Double): Result<Unit>
    suspend fun createShareLink(rideId: String): Result<String>
    suspend fun rateRide(rideId: String, rating: Int, feedback: String): Result<Unit>
}

@Singleton
class RealMopeduRiderRepository @Inject constructor(
    private val api: MopeduRiderApi,
) : MopeduRiderRepository {

    override suspend fun getQuote(pickup: GeoPoint, drop: GeoPoint): Result<QuoteSnapshot> = runCatching {
        val req = EstimateRequestDto(
            pickupLat = pickup.lat,
            pickupLng = pickup.lng,
            pickupLabel = pickup.label,
            dropLat = drop.lat,
            dropLng = drop.lng,
            dropLabel = drop.label,
        )
        val envelope = api.getFareEstimate(req)
        val data = envelope.data ?: throw IllegalStateException("Empty quote response")
        QuoteSnapshot(
            quoteId = data.quoteId,
            pickup = pickup,
            drop = drop,
            distanceMeters = (data.estimatedDistanceKm * 1000).toInt(),
            durationSeconds = (data.estimatedDurationMin * 60).toInt(),
            options = data.options.map { opt ->
                QuoteOption(
                    vehicleType = VehicleType.fromCode(opt.vehicleType),
                    available = opt.available,
                    pickupETASeconds = opt.pickupETASeconds,
                    distanceMeters = opt.distanceMeters,
                    durationSeconds = opt.durationSeconds,
                    totalFare = MoneyPaise(opt.totalPaise),
                    breakdown = QuoteBreakdown(
                        basePaise = opt.breakdown?.basePaise ?: 0,
                        distancePaise = opt.breakdown?.distancePaise ?: 0,
                        timePaise = opt.breakdown?.timePaise ?: 0,
                        platformFeePaise = opt.breakdown?.platformFeePaise ?: 0,
                        taxPaise = opt.breakdown?.taxPaise ?: 0,
                        tollPaise = opt.breakdown?.tollPaise ?: 0,
                        surgeBasisPoints = opt.breakdown?.surgeBasisPoints ?: 0,
                    ),
                )
            },
            expiresAtEpochMs = System.currentTimeMillis() + 300_000L,
        )
    }

    override suspend fun bookRide(
        quoteId: String,
        pickup: GeoPoint,
        drop: GeoPoint,
        vehicleType: VehicleType,
    ): Result<RideBooking> = runCatching {
        val req = CreateRideRequestDto(
            quoteId = quoteId,
            pickup = LocationDto(pickup.address, pickup.lat, pickup.lng),
            drop = LocationDto(drop.address, drop.lat, drop.lng),
            vehicleType = vehicleType.code,
            idempotencyKey = UUID.randomUUID().toString(),
        )
        val envelope = api.createRide(req, req.idempotencyKey)
        val r = envelope.data ?: throw IllegalStateException("Empty booking response")
        RideBooking(
            id = r.id,
            customerUserId = r.customerUserId ?: "",
            partnerId = r.partnerId,
            vehicleId = r.vehicleId,
            quoteId = r.quoteId,
            revision = r.revision,
            vehicleType = VehicleType.fromCode(r.vehicleType),
            status = RideStatus.fromCode(r.status),
            pickup = GeoPoint(r.pickupLat, r.pickupLng, r.pickupAddress),
            drop = GeoPoint(r.dropLat, r.dropLng, r.dropAddress),
            estimatedFare = MoneyPaise((r.estimatedFare * 100).toLong()),
            finalFare = r.finalFarePaise?.let { MoneyPaise(it) },
            paymentMethod = r.paymentMethod,
            otp = r.otp,
            requestedAtEpochMs = System.currentTimeMillis(),
        )
    }

    override suspend fun getActiveRide(): Result<RideBooking?> = runCatching {
        val envelope = api.getActiveRide()
        val r = envelope.data?.ride ?: return@runCatching null
        RideBooking(
            id = r.id,
            customerUserId = r.customerUserId ?: "",
            partnerId = r.partnerId,
            vehicleId = r.vehicleId,
            quoteId = r.quoteId,
            revision = r.revision,
            vehicleType = VehicleType.fromCode(r.vehicleType),
            status = RideStatus.fromCode(r.status),
            pickup = GeoPoint(r.pickupLat, r.pickupLng, r.pickupAddress),
            drop = GeoPoint(r.dropLat, r.dropLng, r.dropAddress),
            estimatedFare = MoneyPaise((r.estimatedFare * 100).toLong()),
            finalFare = r.finalFarePaise?.let { MoneyPaise(it) },
            paymentMethod = r.paymentMethod,
            otp = r.otp,
            requestedAtEpochMs = System.currentTimeMillis(),
        )
    }

    override suspend fun getReceipt(rideId: String): Result<RideReceipt> = runCatching {
        val envelope = api.getRideReceipt(rideId)
        val r = envelope.data ?: throw IllegalStateException("Empty receipt response")
        RideReceipt(
            rideId = r.rideId,
            customerUserId = r.customerUserId ?: "",
            partnerId = r.partnerId,
            vehicleType = VehicleType.fromCode(r.vehicleType),
            status = r.status,
            pickupAddress = r.pickupAddress,
            dropAddress = r.dropAddress,
            distanceMeters = r.distanceMeters,
            durationSeconds = r.durationSeconds,
            totalFare = MoneyPaise(r.totalPaise),
            paymentMethod = r.paymentMethod,
            paymentStatus = r.paymentStatus,
            completedAtEpochMs = System.currentTimeMillis(),
        )
    }

    override suspend fun triggerSOS(rideId: String, lat: Double, lng: Double): Result<Unit> = runCatching {
        api.triggerSOS(rideId, SOSRequestDto(lat = lat, lng = lng))
    }

    override suspend fun createShareLink(rideId: String): Result<String> = runCatching {
        val envelope = api.createShareToken(rideId)
        envelope.data?.url ?: "https://atpost.app/share/$rideId"
    }

    override suspend fun rateRide(rideId: String, rating: Int, feedback: String): Result<Unit> = runCatching {
        api.rateRide(rideId, RateRideRequestDto(rating = rating, feedback = feedback))
    }
}
