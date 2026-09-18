package com.us.android.feature.mopedu.captain

import android.app.Activity
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
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentLauncher
import com.us.android.core.payments.PaymentOutcome
import com.us.android.core.payments.PaymentSession
import com.us.android.feature.mopedu.captain.data.AadhaarStartResponseDto
import com.us.android.feature.mopedu.captain.data.CaptainError
import com.us.android.feature.mopedu.captain.data.CaptainProfile
import com.us.android.feature.mopedu.captain.data.CaptainResult
import com.us.android.feature.mopedu.captain.data.LocationTelemetryDto
import com.us.android.feature.mopedu.captain.data.MopeduCaptainRepository
import com.us.android.feature.mopedu.captain.data.PartnerReview
import com.us.android.feature.mopedu.captain.data.ReviewState
import com.us.android.feature.mopedu.captain.data.SubscriptionCheckout
import com.us.android.feature.mopedu.captain.data.SubscriptionPayment
import com.us.android.feature.mopedu.captain.data.SubscriptionPaymentStatus
import com.us.android.feature.mopedu.captain.home.LocationDisclosureStore

val PICKUP = GeoPoint(17.4401, 78.3489, "Hitec City, Hyderabad")
val DROP = GeoPoint(17.4156, 78.4347, "Banjara Hills, Hyderabad")

/** One day, in the epoch-millis the tests' [FixedClock] speaks. */
const val DAY = 24L * 60L * 60L * 1_000L

fun approvedProfile(isOnline: Boolean = false) = CaptainProfile(
    profile = PartnerProfile("p-1", "individual_driver", "Rahul", "+919876543210", status = "approved", kycStatus = "approved", rating = 4.8, ridesCompleted = 12),
    isOnline = isOnline,
    review = PartnerReview(ReviewState.APPROVED),
)

fun draftProfile() = CaptainProfile(
    profile = PartnerProfile("p-1", "individual_driver", "Rahul", "+919876543210", status = "draft", kycStatus = "pending"),
    isOnline = false,
)

/** Documents in, nothing waiting on a person, but [pending] still missing. */
fun incompleteProfile(vararg pending: String) = CaptainProfile(
    profile = PartnerProfile("p-1", "individual_driver", "Rahul", "+919876543210", status = "submitted", kycStatus = "submitted"),
    isOnline = false,
    review = PartnerReview(ReviewState.INCOMPLETE, pending.toList()),
)

/** A manually uploaded document is with a reviewer. */
fun underReviewProfile(vararg pending: String) = CaptainProfile(
    profile = PartnerProfile("p-1", "individual_driver", "Rahul", "+919876543210", status = "under_review", kycStatus = "under_review"),
    isOnline = false,
    review = PartnerReview(ReviewState.UNDER_REVIEW, pending.toList()),
)

/** An ISO instant [millis] after the epoch: what the fake server says for `expires_at`. */
fun isoAt(millis: Long): String = java.time.Instant.ofEpochMilli(millis).toString()

fun trial(expiresAt: String = "2026-09-04T00:00:00Z", status: String = "trial") =
    PartnerSubscription("sub-1", "p-1", "plan-1", "trial_7d", "Trial", status, 0, 10, "2026-08-28T00:00:00Z", expiresAt)

fun monthlyPlan(expiresAt: String, status: String = "active") =
    PartnerSubscription("sub-2", "p-1", "plan-2", "bike_monthly", "Bike Monthly", status, 0, 30, "2026-09-01T00:00:00Z", expiresAt)

val TRIAL_PLAN = SubscriptionPlan("plan-1", "trial_7d", "7-Day Free Trial", "bike", "trial", MoneyPaise.ZERO, 10, 10, "Free trial", billingPeriodDays = 7)
val MONTHLY_PLAN = SubscriptionPlan("plan-2", "bike_monthly", "Bike Monthly", "bike", "monthly", MoneyPaise(49_900L), 30, 20, "", billingPeriodDays = 30)

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

/** The checkout answer for a paid plan: pending, with the sheet's session. */
fun paidCheckout(subscriptionId: String = "sub-2") = SubscriptionCheckout(
    subscriptionId = subscriptionId,
    intentId = "intent-1",
    amount = MoneyPaise(49_900L),
    currency = "INR",
    clientSession = mapOf("provider" to "razorpay", "order_id" to "order_rzp_1", "key_id" to "rzp_test_public"),
    status = "pending",
)

/** The trial's checkout answer: active on the spot, no session. */
fun trialCheckout() = SubscriptionCheckout("sub-1", null, MoneyPaise.ZERO, "INR", emptyMap(), "active")

fun subscriptionPayment(status: SubscriptionPaymentStatus) = SubscriptionPayment(status, "intent-1", null)

class FixedClock(var now: Long = 0L) : CaptainClock {
    override fun nowMillis(): Long = now
}

class FakeDisclosureStore(private var accepted: Boolean = false) : LocationDisclosureStore {
    override fun accepted(): Boolean = accepted
    override fun markAccepted() {
        accepted = true
    }
}

/** The coordinator with a launcher that must never be asked to open: the console only CONFIRMS; the Activity opens. */
fun confirmingOnlyCoordinator() = PaymentCoordinator(
    object : PaymentLauncher {
        override fun open(activity: Activity, attempt: PaymentAttempt, session: PaymentSession, onOutcome: (PaymentOutcome) -> Unit) =
            error("the captain ViewModel must never open a sheet itself")

        override fun abandon(attempt: PaymentAttempt) = Unit
    },
)

/** A scripted repository: every call is recorded, and answers can be changed between calls. */
@Suppress("TooManyFunctions")
class FakeCaptainRepository : MopeduCaptainRepository {
    var profileAnswer: CaptainResult<CaptainProfile> = CaptainResult.Failure(CaptainError.NotFound)

    /** Consumed in order; the last one repeats. Empty means [profileAnswer]. */
    val profileAnswers = ArrayDeque<CaptainResult<CaptainProfile>>()
    var subscriptionAnswer: CaptainResult<PartnerSubscription?> = CaptainResult.Success(null)
    var plansAnswer: CaptainResult<List<SubscriptionPlan>> = CaptainResult.Success(listOf(TRIAL_PLAN, MONTHLY_PLAN))
    var checkoutAnswer: CaptainResult<SubscriptionCheckout> = CaptainResult.Success(paidCheckout())

    /** Consumed in order; the last one repeats. Empty means unreachable. */
    val subscriptionPaymentAnswers = ArrayDeque<CaptainResult<SubscriptionPayment>>()
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
    val checkouts = mutableListOf<Pair<String, PaymentMethod>>()
    val submittedDocuments = mutableListOf<String>()
    var profileReads = 0
    var paymentReads = 0
    var subscriptionPaymentReads = 0
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

    override suspend fun profile(): CaptainResult<CaptainProfile> {
        profileReads++
        if (profileAnswers.isEmpty()) return profileAnswer
        val next = profileAnswers.removeFirst()
        if (profileAnswers.isEmpty()) profileAnswer = next
        return next
    }

    override suspend fun createProfile(fullName: String, phone: String, email: String?, cityId: String?): CaptainResult<PartnerProfile> =
        CaptainResult.Success(PartnerProfile("p-1", "individual_driver", fullName, phone, email, "draft", "pending", cityId))

    override suspend fun documents(): CaptainResult<List<PartnerDocument>> = CaptainResult.Success(emptyList())
    override suspend fun submitDocument(documentType: String, documentNumber: String?, fileUrl: String): CaptainResult<PartnerDocument> {
        submittedDocuments += documentType
        return CaptainResult.Success(PartnerDocument("doc-1", "p-1", documentType, documentNumber, fileUrl, "submitted"))
    }

    override suspend fun startAadhaar(): CaptainResult<AadhaarStartResponseDto> =
        CaptainResult.Success(AadhaarStartResponseDto("https://digilocker.gov.in/auth", "req-1"))

    override suspend fun callbackAadhaar(requestId: String, assertionToken: String): CaptainResult<PartnerProfile> = CaptainResult.Success(approvedProfile().profile)
    override suspend fun vehicles(): CaptainResult<List<Vehicle>> = CaptainResult.Success(emptyList())
    override suspend fun addVehicle(vehicleType: VehicleType, registrationNumber: String, brand: String?, model: String?): CaptainResult<Vehicle> =
        CaptainResult.Success(Vehicle("v-1", "p-1", vehicleType, registrationNumber, brand, model, null, null, "approved"))

    override suspend fun subscriptionPlans(): CaptainResult<List<SubscriptionPlan>> = plansAnswer

    override suspend fun checkout(planCode: String, method: PaymentMethod): CaptainResult<SubscriptionCheckout> {
        checkouts += planCode to method
        return checkoutAnswer
    }

    override suspend fun subscriptionPayment(): CaptainResult<SubscriptionPayment> {
        subscriptionPaymentReads++
        if (subscriptionPaymentAnswers.isEmpty()) return CaptainResult.Failure(CaptainError.Network(null))
        val next = subscriptionPaymentAnswers.removeFirst()
        if (subscriptionPaymentAnswers.isEmpty()) subscriptionPaymentAnswers.addLast(next)
        return next
    }

    override suspend fun mySubscription(): CaptainResult<PartnerSubscription?> = subscriptionAnswer

    override fun newIdempotencyKey(): String = "key-${++keys}"
}
