package com.us.android.feature.mopedu.captain

import com.us.android.core.mobility.model.CaptainOffer
import com.us.android.core.mobility.model.CaptainState
import com.us.android.core.mobility.model.GeoPoint
import com.us.android.core.mobility.model.MoneyPaise
import com.us.android.core.mobility.model.PartnerDocument
import com.us.android.core.mobility.model.PartnerProfile
import com.us.android.core.mobility.model.PartnerSubscription
import com.us.android.core.mobility.model.PaymentMethod
import com.us.android.core.mobility.model.RidePayment
import com.us.android.core.mobility.model.RidePaymentStatus
import com.us.android.core.mobility.model.SubscriptionPlan
import com.us.android.core.mobility.model.Vehicle
import com.us.android.core.mobility.model.VehicleType
import com.us.android.feature.mopedu.captain.data.AadhaarStartResponseDto
import com.us.android.feature.mopedu.captain.data.CaptainError
import com.us.android.feature.mopedu.captain.data.CaptainProfile
import com.us.android.feature.mopedu.captain.data.CaptainResult
import com.us.android.feature.mopedu.captain.data.LocationTelemetryDto
import com.us.android.feature.mopedu.captain.data.MopeduCaptainRepository
import com.us.android.feature.mopedu.captain.data.SubscribeResponseDto
import com.us.android.feature.mopedu.captain.home.LocationDisclosureStore

val PICKUP = GeoPoint(17.4401, 78.3489, "Hitec City, Hyderabad")
val DROP = GeoPoint(17.4156, 78.4347, "Banjara Hills, Hyderabad")

fun approvedProfile(isOnline: Boolean = false) = CaptainProfile(
    profile = PartnerProfile("p-1", "individual_driver", "Rahul", "+919876543210", status = "approved", kycStatus = "approved", rating = 4.8, ridesCompleted = 12),
    isOnline = isOnline,
)

fun draftProfile() = CaptainProfile(
    profile = PartnerProfile("p-1", "individual_driver", "Rahul", "+919876543210", status = "draft", kycStatus = "pending"),
    isOnline = false,
)

fun trial() = PartnerSubscription("sub-1", "p-1", "plan-1", "trial_7d", "Trial", "trial", 0, 10, "2026-08-28T00:00:00Z", "2026-09-04T00:00:00Z")

fun offer(paymentMethod: PaymentMethod = PaymentMethod.CASH, expiresAtEpochMs: Long = 30_000L) = CaptainOffer(
    id = "offer-1",
    rideId = "ride-123",
    pickup = PICKUP,
    drop = DROP,
    distanceKM = 4.5,
    estimatedEarnings = MoneyPaise(9500L),
    score = 98.0,
    expiresAtEpochMs = expiresAtEpochMs,
    paymentMethod = paymentMethod,
)

fun payment(method: PaymentMethod, status: RidePaymentStatus) = RidePayment(method, status, MoneyPaise(9500L))

class FixedClock(var now: Long = 0L) : CaptainClock {
    override fun nowMillis(): Long = now
}

class FakeDisclosureStore(private var accepted: Boolean = false) : LocationDisclosureStore {
    override fun accepted(): Boolean = accepted
    override fun markAccepted() {
        accepted = true
    }
}

/** A scripted repository: every call is recorded, and answers can be changed between calls. */
@Suppress("TooManyFunctions")
class FakeCaptainRepository : MopeduCaptainRepository {
    var profileAnswer: CaptainResult<CaptainProfile> = CaptainResult.Failure(CaptainError.NotFound)
    var subscriptionAnswer: CaptainResult<PartnerSubscription?> = CaptainResult.Success(null)
    var offersAnswer: CaptainResult<List<CaptainOffer>> = CaptainResult.Success(emptyList())
    var setOnlineAnswer: CaptainResult<Unit> = CaptainResult.Success(Unit)
    var otpAnswer: CaptainResult<Unit> = CaptainResult.Success(Unit)
    var paymentAnswer: CaptainResult<RidePayment> = CaptainResult.Failure(CaptainError.NotFound)

    val onlineCalls = mutableListOf<Boolean>()
    val accepted = mutableListOf<String>()
    val rejected = mutableListOf<String>()
    val arrived = mutableListOf<String>()
    val started = mutableListOf<Pair<String, String>>()
    val completed = mutableListOf<String>()
    val cashConfirmed = mutableListOf<String>()
    var paymentReads = 0
    var keys = 0

    override suspend fun setOnline(online: Boolean): CaptainResult<Unit> {
        onlineCalls += online
        return setOnlineAnswer
    }

    override suspend fun sendLocation(telemetry: LocationTelemetryDto): CaptainResult<Unit> = CaptainResult.Success(Unit)
    override suspend fun incomingOffers(): CaptainResult<List<CaptainOffer>> = offersAnswer
    override suspend fun acceptOffer(offerId: String): CaptainResult<String> {
        accepted += offerId
        return CaptainResult.Success("ride-123")
    }

    override suspend fun rejectOffer(offerId: String): CaptainResult<Unit> {
        rejected += offerId
        return CaptainResult.Success(Unit)
    }

    override suspend fun markArriving(rideId: String): CaptainResult<Unit> = CaptainResult.Success(Unit)
    override suspend fun markArrived(rideId: String): CaptainResult<Unit> {
        arrived += rideId
        return CaptainResult.Success(Unit)
    }

    override suspend fun verifyOtpAndStart(rideId: String, otp: String): CaptainResult<Unit> {
        started += rideId to otp
        return otpAnswer
    }

    override suspend fun completeRide(rideId: String, finalDistanceKm: Double, finalDurationMin: Int, idempotencyKey: String): CaptainResult<Unit> {
        completed += rideId
        return CaptainResult.Success(Unit)
    }

    override suspend fun confirmCashPayment(rideId: String): CaptainResult<Unit> {
        cashConfirmed += rideId
        return CaptainResult.Success(Unit)
    }

    override suspend fun ridePayment(rideId: String): CaptainResult<RidePayment> {
        paymentReads++
        return paymentAnswer
    }

    override suspend fun earnings(): CaptainResult<CaptainState> =
        CaptainResult.Success(CaptainState(false, null, 4.8, 12, MoneyPaise(45000L), todayRides = 5))

    override suspend fun profile(): CaptainResult<CaptainProfile> = profileAnswer
    override suspend fun createProfile(fullName: String, phone: String, email: String?, cityId: String?): CaptainResult<PartnerProfile> =
        CaptainResult.Success(PartnerProfile("p-1", "individual_driver", fullName, phone, email, "draft", "pending", cityId))

    override suspend fun documents(): CaptainResult<List<PartnerDocument>> = CaptainResult.Success(emptyList())
    override suspend fun submitDocument(documentType: String, documentNumber: String?, fileUrl: String): CaptainResult<PartnerDocument> =
        CaptainResult.Success(PartnerDocument("doc-1", "p-1", documentType, documentNumber, fileUrl, "submitted"))

    override suspend fun startAadhaar(): CaptainResult<AadhaarStartResponseDto> =
        CaptainResult.Success(AadhaarStartResponseDto("https://digilocker.gov.in/auth", "req-1"))

    override suspend fun callbackAadhaar(requestId: String, assertionToken: String): CaptainResult<PartnerProfile> = CaptainResult.Success(approvedProfile().profile)
    override suspend fun vehicles(): CaptainResult<List<Vehicle>> = CaptainResult.Success(emptyList())
    override suspend fun addVehicle(vehicleType: VehicleType, registrationNumber: String, brand: String?, model: String?): CaptainResult<Vehicle> =
        CaptainResult.Success(Vehicle("v-1", "p-1", vehicleType, registrationNumber, brand, model, null, null, "approved"))

    override suspend fun subscriptionPlans(): CaptainResult<List<SubscriptionPlan>> = CaptainResult.Success(
        listOf(SubscriptionPlan("plan-1", "trial_7d", "7-Day Free Trial", "bike", "trial", MoneyPaise.ZERO, 10, 10, "Free trial")),
    )

    override suspend fun subscribe(planId: String, idempotencyKey: String): CaptainResult<SubscribeResponseDto> =
        CaptainResult.Success(SubscribeResponseDto("sub-1", "pay-1", "active"))

    override suspend fun mySubscription(): CaptainResult<PartnerSubscription?> = subscriptionAnswer

    override fun newIdempotencyKey(): String = "key-${++keys}"
}
