package com.us.android.feature.mopedu.captain.data

import com.us.android.core.mobility.model.CaptainOffer
import com.us.android.core.mobility.model.CaptainState
import com.us.android.core.mobility.model.CaptainTelemetry
import com.us.android.core.mobility.model.GeoPoint
import com.us.android.core.mobility.model.MoneyPaise
import com.us.android.core.mobility.model.PartnerDocument
import com.us.android.core.mobility.model.PartnerProfile
import com.us.android.core.mobility.model.PartnerSubscription
import com.us.android.core.mobility.model.RideBooking
import com.us.android.core.mobility.model.RideStatus
import com.us.android.core.mobility.model.SubscriptionPlan
import com.us.android.core.mobility.model.Vehicle
import com.us.android.core.mobility.model.VehicleType
import java.util.UUID
import javax.inject.Inject
import javax.inject.Singleton

interface MopeduCaptainRepository {
    suspend fun setOnline(online: Boolean): Result<Unit>
    suspend fun sendLocation(telemetry: CaptainTelemetry): Result<Unit>
    suspend fun getIncomingOffers(): Result<List<CaptainOffer>>
    suspend fun acceptOffer(offerId: String): Result<String> // returns rideId
    suspend fun rejectOffer(offerId: String): Result<Unit>
    suspend fun markArriving(rideId: String): Result<Unit>
    suspend fun markArrived(rideId: String): Result<Unit>
    suspend fun verifyOtpAndStart(rideId: String, otp: String): Result<Unit>
    suspend fun completeRide(rideId: String, finalDistanceKm: Double, finalDurationMin: Int): Result<Unit>
    suspend fun confirmCashPayment(rideId: String): Result<Unit>
    suspend fun getEarnings(): Result<CaptainState>

    // --- Onboarding & Profile ---
    suspend fun getProfile(): Result<PartnerProfile>
    suspend fun createProfile(fullName: String, phone: String, email: String? = null, cityId: String? = null): Result<PartnerProfile>
    suspend fun updateProfile(fullName: String? = null, email: String? = null, profilePhotoUrl: String? = null, cityId: String? = null): Result<PartnerProfile>

    // --- Documents & Aadhaar ---
    suspend fun getDocuments(): Result<List<PartnerDocument>>
    suspend fun submitDocument(documentType: String, documentNumber: String? = null, fileUrl: String, expiresAt: String? = null): Result<PartnerDocument>
    suspend fun startAadhaar(): Result<AadhaarStartResponseDto>
    suspend fun callbackAadhaar(requestId: String, assertionToken: String): Result<PartnerProfile>

    // --- Vehicles ---
    suspend fun getVehicles(): Result<List<Vehicle>>
    suspend fun addVehicle(
        vehicleType: VehicleType,
        registrationNumber: String,
        brand: String? = null,
        model: String? = null,
        color: String? = null,
        year: Int? = null,
    ): Result<Vehicle>
    suspend fun submitVehicleDocument(vehicleId: String, documentType: String, fileUrl: String): Result<PartnerDocument>

    // --- Subscriptions ---
    suspend fun getSubscriptionPlans(): Result<List<SubscriptionPlan>>
    suspend fun subscribe(planId: String, paymentMethod: String = "wallet"): Result<SubscribeResponseDto>
    suspend fun submitPaymentProof(paymentId: String, fileUrl: String): Result<Unit>
    suspend fun getMySubscription(): Result<PartnerSubscription?>
}

@Singleton
class RealMopeduCaptainRepository @Inject constructor(
    private val api: MopeduCaptainApi,
) : MopeduCaptainRepository {

    override suspend fun setOnline(online: Boolean): Result<Unit> = runCatching {
        if (online) api.goOnline() else api.goOffline()
    }

    override suspend fun sendLocation(telemetry: CaptainTelemetry): Result<Unit> = runCatching {
        val dto = LocationTelemetryDto(
            lat = telemetry.lat,
            lng = telemetry.lng,
            speedMps = telemetry.speedMps,
            heading = telemetry.heading,
            accuracyMeters = telemetry.accuracyMeters,
            sequence = telemetry.sequence,
        )
        api.updateLocation(dto)
    }

    override suspend fun getIncomingOffers(): Result<List<CaptainOffer>> = runCatching {
        val envelope = api.getIncomingOffers()
        val list = envelope.data ?: emptyList()
        list.map { dto ->
            CaptainOffer(
                id = dto.id,
                rideId = dto.rideId,
                pickup = GeoPoint(dto.pickupLat, dto.pickupLng, dto.pickupAddress),
                drop = GeoPoint(dto.dropLat, dto.dropLng, dto.dropAddress),
                distanceKM = dto.distanceKm,
                estimatedEarnings = MoneyPaise(dto.estimatedFarePaise),
                score = dto.score,
                expiresAtEpochMs = System.currentTimeMillis() + 15_000L,
            )
        }
    }

    override suspend fun acceptOffer(offerId: String): Result<String> = runCatching {
        val envelope = api.acceptOffer(offerId)
        val data = envelope.data ?: throw IllegalStateException("Accept offer failed")
        data.rideId
    }

    override suspend fun rejectOffer(offerId: String): Result<Unit> = runCatching {
        api.rejectOffer(offerId)
    }

    override suspend fun markArriving(rideId: String): Result<Unit> = runCatching {
        api.markArriving(rideId)
    }

    override suspend fun markArrived(rideId: String): Result<Unit> = runCatching {
        api.markArrived(rideId)
    }

    override suspend fun verifyOtpAndStart(rideId: String, otp: String): Result<Unit> = runCatching {
        api.startRide(rideId, StartRideRequestDto(otp = otp))
    }

    override suspend fun completeRide(
        rideId: String,
        finalDistanceKm: Double,
        finalDurationMin: Int,
    ): Result<Unit> = runCatching {
        api.completeRide(
            rideId = rideId,
            request = CompleteRideRequestDto(
                finalDistanceKm = finalDistanceKm,
                finalDurationMin = finalDurationMin,
                idempotencyKey = UUID.randomUUID().toString(),
            ),
        )
    }

    override suspend fun confirmCashPayment(rideId: String): Result<Unit> = runCatching {
        api.confirmCashPayment(rideId)
    }

    override suspend fun getEarnings(): Result<CaptainState> = runCatching {
        val envelope = api.getEarnings()
        val data = envelope.data
        CaptainState(
            isOnline = true,
            activeRideId = null,
            rating = data?.rating ?: 4.88,
            totalRidesCompleted = data?.todayRidesCount ?: 0,
            todayEarnings = MoneyPaise(data?.todayEarningsPaise ?: 0),
        )
    }

    // --- Onboarding & Profile Implementation ---

    override suspend fun getProfile(): Result<PartnerProfile> = runCatching {
        val envelope = api.getProfile()
        val dto = envelope.data ?: throw IllegalStateException("Failed to load partner profile")
        dto.toDomain()
    }

    override suspend fun createProfile(
        fullName: String,
        phone: String,
        email: String?,
        cityId: String?,
    ): Result<PartnerProfile> = runCatching {
        val envelope = api.createProfile(
            CreatePartnerRequestDto(
                fullName = fullName,
                phone = phone,
                email = email,
                cityId = cityId,
            )
        )
        val dto = envelope.data ?: throw IllegalStateException("Failed to create partner profile")
        dto.toDomain()
    }

    override suspend fun updateProfile(
        fullName: String?,
        email: String?,
        profilePhotoUrl: String?,
        cityId: String?,
    ): Result<PartnerProfile> = runCatching {
        val envelope = api.updateProfile(
            UpdatePartnerRequestDto(
                fullName = fullName,
                email = email,
                profilePhotoUrl = profilePhotoUrl,
                cityId = cityId,
            )
        )
        val dto = envelope.data ?: throw IllegalStateException("Failed to update partner profile")
        dto.toDomain()
    }

    // --- Documents & Aadhaar Implementation ---

    override suspend fun getDocuments(): Result<List<PartnerDocument>> = runCatching {
        val envelope = api.getDocuments()
        val list = envelope.data ?: emptyList()
        list.map { it.toDomain() }
    }

    override suspend fun submitDocument(
        documentType: String,
        documentNumber: String?,
        fileUrl: String,
        expiresAt: String?,
    ): Result<PartnerDocument> = runCatching {
        val envelope = api.submitDocument(
            SubmitDocumentRequestDto(
                documentType = documentType,
                documentNumber = documentNumber,
                fileUrl = fileUrl,
                expiresAt = expiresAt,
            )
        )
        val dto = envelope.data ?: throw IllegalStateException("Failed to submit document")
        dto.toDomain()
    }

    override suspend fun startAadhaar(): Result<AadhaarStartResponseDto> = runCatching {
        val envelope = api.startAadhaar()
        envelope.data ?: throw IllegalStateException("Failed to start Aadhaar DigiLocker")
    }

    override suspend fun callbackAadhaar(
        requestId: String,
        assertionToken: String,
    ): Result<PartnerProfile> = runCatching {
        val envelope = api.callbackAadhaar(
            AadhaarCallbackRequestDto(
                requestId = requestId,
                assertionToken = assertionToken,
            )
        )
        val dto = envelope.data ?: throw IllegalStateException("Failed to complete Aadhaar callback")
        dto.toDomain()
    }

    // --- Vehicles Implementation ---

    override suspend fun getVehicles(): Result<List<Vehicle>> = runCatching {
        val envelope = api.getVehicles()
        val list = envelope.data ?: emptyList()
        list.map { it.toDomain() }
    }

    override suspend fun addVehicle(
        vehicleType: VehicleType,
        registrationNumber: String,
        brand: String?,
        model: String?,
        color: String?,
        year: Int?,
    ): Result<Vehicle> = runCatching {
        val envelope = api.addVehicle(
            CreateVehicleRequestDto(
                vehicleType = vehicleType.code,
                registrationNumber = registrationNumber,
                brand = brand,
                model = model,
                color = color,
                manufactureYear = year,
            )
        )
        val dto = envelope.data ?: throw IllegalStateException("Failed to add vehicle")
        dto.toDomain()
    }

    override suspend fun submitVehicleDocument(
        vehicleId: String,
        documentType: String,
        fileUrl: String,
    ): Result<PartnerDocument> = runCatching {
        val envelope = api.submitVehicleDocument(
            vehicleId = vehicleId,
            request = SubmitDocumentRequestDto(
                documentType = documentType,
                fileUrl = fileUrl,
            )
        )
        val dto = envelope.data ?: throw IllegalStateException("Failed to submit vehicle document")
        dto.toDomain()
    }

    // --- Subscriptions Implementation ---

    override suspend fun getSubscriptionPlans(): Result<List<SubscriptionPlan>> = runCatching {
        val envelope = api.getSubscriptionPlans()
        val list = envelope.data ?: emptyList()
        list.map { dto ->
            SubscriptionPlan(
                id = dto.id,
                code = dto.code,
                name = dto.name,
                vehicleType = dto.vehicleType,
                billingCycle = dto.billingCycle,
                price = MoneyPaise(dto.pricePaise),
                dailyLeadCap = dto.dailyLeadCap,
                priorityScore = dto.priorityScore,
                description = dto.description,
            )
        }
    }

    override suspend fun subscribe(
        planId: String,
        paymentMethod: String,
    ): Result<SubscribeResponseDto> = runCatching {
        val envelope = api.subscribe(
            SubscribeRequestDto(
                planId = planId,
                paymentMethod = paymentMethod,
                idempotencyKey = UUID.randomUUID().toString(),
            )
        )
        envelope.data ?: throw IllegalStateException("Subscription failed")
    }

    override suspend fun submitPaymentProof(
        paymentId: String,
        fileUrl: String,
    ): Result<Unit> = runCatching {
        api.submitPaymentProof(
            PaymentProofRequestDto(
                paymentId = paymentId,
                fileUrl = fileUrl,
            )
        )
    }

    override suspend fun getMySubscription(): Result<PartnerSubscription?> = runCatching {
        val envelope = api.getMySubscription()
        envelope.data?.let { dto ->
            PartnerSubscription(
                id = dto.id,
                partnerId = dto.partnerId,
                planId = dto.planId,
                planCode = dto.planCode,
                planName = dto.planName,
                status = dto.status,
                leadsUsed = dto.leadsUsed,
                dailyLeadCap = dto.dailyLeadCap,
                startsAt = dto.startsAt,
                expiresAt = dto.expiresAt,
            )
        }
    }

    // --- Mapping Helpers ---

    private fun PartnerProfileDto.toDomain(): PartnerProfile = PartnerProfile(
        id = id,
        partnerType = partnerType,
        fullName = fullName,
        phone = phone,
        email = email,
        status = status,
        kycStatus = kycStatus,
        cityId = cityId,
        profilePhotoUrl = profilePhotoUrl,
        rating = rating,
        ridesCompleted = ridesCompleted,
    )

    private fun PartnerDocumentDto.toDomain(): PartnerDocument = PartnerDocument(
        id = id,
        partnerId = partnerId,
        documentType = documentType,
        documentNumber = documentNumber,
        fileUrl = fileUrl,
        status = status,
        rejectionReason = rejectionReason,
        expiresAt = expiresAt,
    )

    private fun VehicleDto.toDomain(): Vehicle = Vehicle(
        id = id,
        partnerId = partnerId,
        vehicleType = VehicleType.fromCode(vehicleType),
        registrationNumber = registrationNumber,
        brand = brand,
        model = model,
        color = color,
        manufactureYear = manufactureYear ?: year,
        status = status,
    )
}
