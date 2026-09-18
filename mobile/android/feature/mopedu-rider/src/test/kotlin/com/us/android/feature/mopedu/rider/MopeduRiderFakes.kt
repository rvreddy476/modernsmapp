package com.us.android.feature.mopedu.rider

import android.app.Activity
import com.us.android.core.mobility.model.CaptainInfo
import com.us.android.core.mobility.model.CouponValidation
import com.us.android.core.mobility.model.GeoPoint
import com.us.android.core.mobility.model.MoneyPaise
import com.us.android.core.mobility.model.OutstandingCharge
import com.us.android.core.mobility.model.PaymentMethod
import com.us.android.core.mobility.model.QuoteBreakdown
import com.us.android.core.mobility.model.QuoteOption
import com.us.android.core.mobility.model.QuoteSnapshot
import com.us.android.core.mobility.model.RideBooking
import com.us.android.core.mobility.model.RidePayment
import com.us.android.core.mobility.model.RidePaymentIntent
import com.us.android.core.mobility.model.RidePaymentStatus
import com.us.android.core.mobility.model.RideReceipt
import com.us.android.core.mobility.model.RideStatus
import com.us.android.core.mobility.model.SurgeReason
import com.us.android.core.mobility.model.VehicleType
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentLauncher
import com.us.android.core.payments.PaymentOutcome
import com.us.android.core.payments.PaymentSession
import com.us.android.feature.mopedu.rider.data.MopeduError
import com.us.android.feature.mopedu.rider.data.MopeduResult
import com.us.android.feature.mopedu.rider.data.MopeduRiderRepository
import com.us.android.feature.mopedu.rider.location.CurrentLocationSource

/*
 * Shared test doubles for the rider feature (the DatingFakes shape).
 */

val PICKUP = GeoPoint(17.4401, 78.3489, "Hitec City, Hyderabad", "Pickup")
val DROP = GeoPoint(17.4156, 78.4347, "Banjara Hills, Hyderabad", "Drop")

fun quoteOption(
    vehicleType: VehicleType = VehicleType.BIKE,
    totalPaise: Long = 6500L,
    surgeReason: SurgeReason = SurgeReason.NONE,
    surgeBasisPoints: Long = 0,
    discountPaise: Long = 0,
    outstandingPaise: Long = 0,
    couponCode: String? = null,
) = QuoteOption(
    vehicleType = vehicleType,
    available = true,
    pickupETASeconds = 180,
    distanceMeters = 5000,
    durationSeconds = 600,
    totalFare = MoneyPaise(totalPaise),
    breakdown = QuoteBreakdown(
        basePaise = 3000L,
        distancePaise = 2500L,
        timePaise = 500L,
        taxPaise = 500L,
        discountPaise = discountPaise,
        outstandingPaise = outstandingPaise,
    ),
    surgeBasisPoints = surgeBasisPoints,
    surgeReason = surgeReason,
    couponCode = couponCode,
    discount = MoneyPaise(discountPaise),
)

fun quote(
    options: List<QuoteOption> = listOf(quoteOption(), quoteOption(VehicleType.AUTO, 9500L)),
    couponCode: String? = null,
    expiresAtEpochMs: Long = Long.MAX_VALUE,
) = QuoteSnapshot(
    quoteId = "quote-1",
    pickup = PICKUP,
    drop = DROP,
    distanceMeters = 5000,
    durationSeconds = 600,
    options = options,
    expiresAtEpochMs = expiresAtEpochMs,
    couponCode = couponCode,
)

fun booking(
    status: RideStatus = RideStatus.SEARCHING_PARTNER,
    paymentMethod: PaymentMethod = PaymentMethod.CASH,
    cancellationFeePaise: Long? = 2000L,
    cancelFreeUntilEpochMs: Long? = 100_000L,
) = RideBooking(
    id = "ride-456",
    customerUserId = "user-123",
    partnerId = "p-1",
    vehicleId = "v-1",
    quoteId = "quote-1",
    revision = 1,
    vehicleType = VehicleType.BIKE,
    status = status,
    pickup = PICKUP,
    drop = DROP,
    estimatedFare = MoneyPaise(6500L),
    finalFare = null,
    paymentMethod = paymentMethod,
    otp = "4321",
    captain = CaptainInfo("c-1", "Ramesh", "+919876543210", 4.9, "Pulsar 150", "TS09AB1234", "Black"),
    requestedAtEpochMs = 0L,
    cancellationFee = cancellationFeePaise?.let(::MoneyPaise),
    cancelFreeUntilEpochMs = cancelFreeUntilEpochMs,
)

fun receipt(paymentMethod: PaymentMethod = PaymentMethod.CASH, paymentStatus: RidePaymentStatus = RidePaymentStatus.CASH_PENDING) = RideReceipt(
    rideId = "ride-456",
    customerUserId = "user-123",
    partnerId = "p-1",
    vehicleType = VehicleType.BIKE,
    status = "completed",
    pickupAddress = PICKUP.address,
    dropAddress = DROP.address,
    distanceMeters = 5000,
    durationSeconds = 600,
    totalFare = MoneyPaise(6500L),
    paymentMethod = paymentMethod,
    paymentStatus = paymentStatus,
    completedAtEpochMs = 1L,
)

fun payment(method: PaymentMethod, status: RidePaymentStatus, amountPaise: Long = 6500L) =
    RidePayment(method = method, status = status, amount = MoneyPaise(amountPaise))

fun intent(rideOrChargeId: String = "ride-456", withSession: Boolean = true) = RidePaymentIntent(
    intentId = "intent-$rideOrChargeId",
    amount = MoneyPaise(6500L),
    currency = "INR",
    clientSession = if (withSession) mapOf("provider" to "razorpay", "order_id" to "order_rzp_1", "key_id" to "rzp_test_key") else emptyMap(),
    status = "created",
)

/** A scripted repository: every call is recorded, and answers are queued per call. */
@Suppress("TooManyFunctions")
class FakeRiderRepository : MopeduRiderRepository {
    val estimateCalls = mutableListOf<Pair<GeoPoint, String?>>()
    val couponCalls = mutableListOf<String>()
    val bookings = mutableListOf<Triple<String, VehicleType, PaymentMethod>>()
    val cancelCalls = mutableListOf<String>()
    val intentCalls = mutableListOf<Pair<String, PaymentMethod>>()
    val outstandingIntentCalls = mutableListOf<String>()
    var switchToCashCalls = 0
    var paymentReads = 0
    var keys = 0

    var quoteAnswer: (couponCode: String?) -> MopeduResult<QuoteSnapshot> = { code -> MopeduResult.Success(quote(couponCode = code)) }
    var couponAnswer: MopeduResult<CouponValidation> = MopeduResult.Success(CouponValidation("SAVE10", "10% off", "percent", percentBasisPoints = 1000))
    var bookingAnswer: (PaymentMethod) -> MopeduResult<RideBooking> = { method -> MopeduResult.Success(booking(paymentMethod = method)) }
    var activeRide: RideBooking? = null
    var receiptAnswer: MopeduResult<RideReceipt> = MopeduResult.Success(receipt())
    val paymentAnswers = ArrayDeque<MopeduResult<RidePayment>>()
    var lastPaymentAnswer: MopeduResult<RidePayment> = MopeduResult.Success(payment(PaymentMethod.CASH, RidePaymentStatus.CASH_PENDING))
    var intentAnswer: MopeduResult<RidePaymentIntent> = MopeduResult.Success(intent())
    var cancelAnswer: MopeduResult<Unit> = MopeduResult.Success(Unit)
    var switchToCashAnswer: MopeduResult<RidePayment> = MopeduResult.Success(payment(PaymentMethod.CASH, RidePaymentStatus.CASH_PENDING))
    var outstandingAnswer: MopeduResult<List<OutstandingCharge>> = MopeduResult.Success(emptyList())
    var historyAnswer: MopeduResult<List<RideBooking>> = MopeduResult.Success(emptyList())

    override suspend fun estimate(pickup: GeoPoint, drop: GeoPoint, couponCode: String?): MopeduResult<QuoteSnapshot> {
        estimateCalls += pickup to couponCode
        return quoteAnswer(couponCode)
    }

    override suspend fun validateCoupon(code: String, cityId: String?, vehicleType: VehicleType?): MopeduResult<CouponValidation> {
        couponCalls += code
        return couponAnswer
    }

    override suspend fun bookRide(
        quote: QuoteSnapshot,
        vehicleType: VehicleType,
        paymentMethod: PaymentMethod,
        idempotencyKey: String,
    ): MopeduResult<RideBooking> {
        bookings += Triple(idempotencyKey, vehicleType, paymentMethod)
        return bookingAnswer(paymentMethod)
    }

    override suspend fun activeRide(): MopeduResult<RideBooking?> = MopeduResult.Success(activeRide)

    override suspend fun rideHistory(limit: Int): MopeduResult<List<RideBooking>> = historyAnswer

    override suspend fun cancelRide(rideId: String, reason: String): MopeduResult<Unit> {
        cancelCalls += rideId
        return cancelAnswer
    }

    override suspend fun receipt(rideId: String): MopeduResult<RideReceipt> = receiptAnswer

    override suspend fun triggerSOS(rideId: String, lat: Double, lng: Double): MopeduResult<Unit> = MopeduResult.Success(Unit)

    override suspend fun createShareLink(rideId: String): MopeduResult<String> = MopeduResult.Success("https://atpost.app/share/$rideId")

    override suspend fun rateRide(rideId: String, rating: Int, feedback: String): MopeduResult<Unit> = MopeduResult.Success(Unit)

    override suspend fun paymentIntent(rideId: String, method: PaymentMethod): MopeduResult<RidePaymentIntent> {
        intentCalls += rideId to method
        return intentAnswer
    }

    override suspend fun ridePayment(rideId: String): MopeduResult<RidePayment> {
        paymentReads++
        return paymentAnswers.removeFirstOrNull()?.also { if (paymentAnswers.isEmpty()) lastPaymentAnswer = it } ?: lastPaymentAnswer
    }

    override suspend fun switchToCash(rideId: String): MopeduResult<RidePayment> {
        switchToCashCalls++
        return switchToCashAnswer
    }

    override suspend fun outstanding(): MopeduResult<List<OutstandingCharge>> = outstandingAnswer

    override suspend fun outstandingPaymentIntent(chargeId: String, method: PaymentMethod): MopeduResult<RidePaymentIntent> {
        outstandingIntentCalls += chargeId
        return intentAnswer
    }

    override fun newIdempotencyKey(): String = "key-${++keys}"
}

class FakeLocationSource(
    var permission: Boolean = true,
    var fix: GeoPoint? = PICKUP,
    var geocoded: Map<String, GeoPoint> = mapOf(PICKUP.address to PICKUP, DROP.address to DROP),
) : CurrentLocationSource {
    override fun hasPermission(): Boolean = permission
    override suspend fun current(): GeoPoint? = fix
    override suspend fun geocode(query: String): GeoPoint? = geocoded[query]
}

class FixedClock(var now: Long = 0L) : RideClock {
    override fun nowMillis(): Long = now
}

/** The coordinator with a launcher that must never be asked to open: the ride screen only CONFIRMS. */
fun confirmingOnlyCoordinator() = PaymentCoordinator(
    object : PaymentLauncher {
        override fun open(activity: Activity, attempt: PaymentAttempt, session: PaymentSession, onOutcome: (PaymentOutcome) -> Unit) =
            error("the ride ViewModel must never open a sheet itself")

        override fun abandon(attempt: PaymentAttempt) = Unit
    },
)

fun refused(code: String, message: String, status: Int = 422) = MopeduResult.Failure(MopeduError.Refused(status, code, message))
