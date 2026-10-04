package com.us.android.feature.doorstep.payment

import android.app.Activity
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentSession
import com.us.android.core.payments.PaymentStatusReading
import com.us.android.core.payments.PaymentStatusSource
import com.us.android.core.payments.toHandoffEvent
import com.us.android.feature.doorstep.data.BookingPaymentsDto
import com.us.android.feature.doorstep.data.DoorstepRepository
import com.us.android.feature.doorstep.data.DoorstepResult
import com.us.android.feature.doorstep.data.PaymentIntentDto
import com.us.android.feature.doorstep.data.textOrNull
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Doorstep's application id in `:core:payments` — the ONE place Doorstep names
 * it. It matches the payments-service application registered by migration
 * 015_doorstep_application.sql. Every booking and extras attempt, session,
 * confirmation and in-flight record is stamped with it, so a pending Doorstep
 * payment can never be resumed or shown in Feast, Mopedu, Dating or MStore.
 */
const val DOORSTEP_PAYMENT_APPLICATION_ID = "doorstep"

/** payments-service reference types (x-doorstep-payments). */
object DoorstepReference {
    const val BOOKING = "doorstep_booking"
    const val EXTRAS = "doorstep_extras"
}

/**
 * What a Doorstep screen hands `:app` to open the sheet: the attempt (always
 * Doorstep's; its reference is a booking id or an extras bill id) and the
 * session built from the server's `checkout`.
 *
 * Carried in memory only, through a navigation callback. Nothing here is ever
 * written to saved state — the in-flight record holds identities alone.
 */
data class DoorstepPaymentRequest(
    val attempt: PaymentAttempt,
    val session: PaymentSession,
) {
    init {
        require(attempt.applicationId == DOORSTEP_PAYMENT_APPLICATION_ID) {
            "a Doorstep payment request cannot carry a ${attempt.applicationId} attempt"
        }
        require(session.applicationId == DOORSTEP_PAYMENT_APPLICATION_ID) {
            "a Doorstep payment request cannot carry a ${session.applicationId} session"
        }
    }

    val referenceId: String get() = attempt.referenceId
}

/**
 * `GET /v1/doorstep/bookings/{id}/payment`, read for the coordinator — the
 * payment rows the SIGNED `payment.succeeded` event settled (ApplyOnce on the
 * server). Nothing else in the app may conclude "paid".
 *
 * One source per booking: [bookingId] is where the rows are read, and the
 * coordinator's reference is the row's `reference_id` — the booking id itself
 * for [DoorstepReference.BOOKING], an extras bill id for
 * [DoorstepReference.EXTRAS].
 */
class DoorstepPaymentStatusSource(
    private val repository: DoorstepRepository,
    private val bookingId: String,
    private val referenceType: String,
) : PaymentStatusSource {

    override val applicationId: String = DOORSTEP_PAYMENT_APPLICATION_ID

    override suspend fun status(referenceId: String): PaymentStatusReading =
        when (val result = repository.bookingPayments(bookingId)) {
            is DoorstepResult.Failure -> PaymentStatusReading.Unreachable(result.error.toString())
            is DoorstepResult.Success -> result.value.readingFor(referenceType, referenceId)
        }
}

/**
 * The server's payment rows in the coordinator's vocabulary.
 *
 *  - no row yet, `created` or `pending` (or a status this build does not know)
 *    → still confirming;
 *  - a refund in flight on that payment outranks `succeeded` — a capture that
 *    landed after the hold lapsed is being returned and must never render as
 *    a booked visit;
 *  - `failed` is retryable on the SAME booking with a new attempt.
 */
fun BookingPaymentsDto.readingFor(referenceType: String, referenceId: String): PaymentStatusReading {
    val payment = payments.lastOrNull { it.referenceType == referenceType && it.referenceId == referenceId }
        ?: return PaymentStatusReading.Confirming
    val refundInFlight = refunds.any { it.paymentId == payment.paymentId && it.status in REFUND_IN_FLIGHT }
    return when {
        payment.status == "refunded" -> PaymentStatusReading.Refunded
        payment.status == "partially_refunded" || refundInFlight -> PaymentStatusReading.RefundPending
        payment.status == "succeeded" -> PaymentStatusReading.Paid
        payment.status == "failed" -> PaymentStatusReading.Failed(retryable = true)
        else -> PaymentStatusReading.Confirming
    }
}

private val REFUND_IN_FLIGHT = setOf("requested", "pending")

/**
 * The sheet session from the intent, stamped Doorstep's; null when the server
 * attached no usable checkout (the payment cannot be opened now). The amount
 * is the intent's own, priced by the server; Razorpay prices the sheet from
 * the provider order regardless.
 */
fun PaymentIntentDto.toPaymentSession(description: String): PaymentSession? {
    val session = checkout.mapNotNull { (key, value) -> value.textOrNull()?.let { key to it } }.toMap()
    if (session[PaymentSession.CLIENT_SESSION_ORDER_ID].isNullOrBlank()) return null
    return PaymentSession.fromClientSession(
        applicationId = DOORSTEP_PAYMENT_APPLICATION_ID,
        clientSession = session,
        amountMinor = amountPaise,
        currency = CURRENCY,
        description = description,
    )
}

private const val CURRENCY = "INR"

/**
 * Opens the sheet for a Doorstep payment from the Activity (`MainActivity`),
 * because the provider sheet opens onto an Activity and calls back on it, then
 * publishes exactly one ending onto Doorstep's stream of [PaymentHandoff].
 *
 * The intent (and so the server session) already exists: `POST /bookings`
 * returned it, or the screen reopened it, before asking for the sheet.
 */
@Singleton
class DoorstepPaymentOpener @Inject constructor(
    private val payments: PaymentCoordinator,
    private val handoff: PaymentHandoff,
) {

    fun start(activity: Activity, request: DoorstepPaymentRequest) {
        payments.launch(activity, request.attempt, request.session) { sheet ->
            handoff.publish(sheet.toHandoffEvent())
        }
    }

    /** Releases the launcher's one in-flight slot when the screen goes away. */
    fun abandon(request: DoorstepPaymentRequest) {
        payments.abandon(request.attempt)
    }
}
