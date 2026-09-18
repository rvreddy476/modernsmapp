package com.us.android.feature.mopedu.rider.payment

import android.app.Activity
import com.us.android.core.mobility.model.RidePayment
import com.us.android.core.mobility.model.RidePaymentIntent
import com.us.android.core.mobility.model.RidePaymentStatus
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentSession
import com.us.android.core.payments.PaymentStatusReading
import com.us.android.core.payments.PaymentStatusSource
import com.us.android.core.payments.toHandoffEvent
import com.us.android.feature.mopedu.rider.data.MopeduResult
import com.us.android.feature.mopedu.rider.data.MopeduRiderRepository
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Mopedu's application id in `:core:payments` — the ONE place Mopedu names it.
 *
 * Every ride-fare attempt, session, confirmation and in-flight record is
 * stamped with it, so a pending Mopedu payment can never be resumed or shown
 * in Feast, Dating or MStore checkout, and their endings never reach a ride.
 * It matches the payments-service application registered in 1f6e9f34.
 */
const val MOPEDU_PAYMENT_APPLICATION_ID = "mopedu"

/**
 * What a Mopedu screen hands `:app` to open the sheet: the attempt (always
 * Mopedu's; its reference is the ride id, or an outstanding charge id) and the
 * session built from the server's `client_session`.
 *
 * Carried in memory only, through a navigation callback. Nothing here is ever
 * written to saved state — the in-flight record holds identities alone.
 */
data class MopeduPaymentRequest(
    val attempt: PaymentAttempt,
    val session: PaymentSession,
) {
    init {
        require(attempt.applicationId == MOPEDU_PAYMENT_APPLICATION_ID) {
            "a Mopedu payment request cannot carry a ${attempt.applicationId} attempt"
        }
        require(session.applicationId == MOPEDU_PAYMENT_APPLICATION_ID) {
            "a Mopedu payment request cannot carry a ${session.applicationId} session"
        }
    }

    val referenceId: String get() = attempt.referenceId
}

/**
 * `GET /v1/rider/rides/{id}/payment`, read for the coordinator.
 *
 * A read that could not be made is [PaymentStatusReading.Unreachable] — never
 * Failed. A refund outranks paid. An unknown status keeps confirming. Cash the
 * captain confirmed counts as settled: the ride's money is done.
 */
class MopeduPaymentStatusSource(
    private val repository: MopeduRiderRepository,
) : PaymentStatusSource {

    override val applicationId: String = MOPEDU_PAYMENT_APPLICATION_ID

    override suspend fun status(referenceId: String): PaymentStatusReading =
        when (val result = repository.ridePayment(referenceId)) {
            is MopeduResult.Failure -> PaymentStatusReading.Unreachable(result.error.toString())
            is MopeduResult.Success -> result.value.toReading()
        }
}

/**
 * An outstanding cancellation fee has no status route of its own: the charge
 * is read back from `GET /v1/rider/me/outstanding`. Gone from the list, or
 * listed as paid, is paid; listed as failed is failed; anything else is still
 * confirming. A list that could not be read is unreachable, never failed.
 */
class OutstandingPaymentStatusSource(
    private val repository: MopeduRiderRepository,
) : PaymentStatusSource {

    override val applicationId: String = MOPEDU_PAYMENT_APPLICATION_ID

    override suspend fun status(referenceId: String): PaymentStatusReading =
        when (val result = repository.outstandingStatus(referenceId)) {
            is MopeduResult.Failure -> PaymentStatusReading.Unreachable(result.error.toString())
            is MopeduResult.Success -> when (result.value) {
                null, OUTSTANDING_PAID -> PaymentStatusReading.Paid
                OUTSTANDING_FAILED -> PaymentStatusReading.Failed(retryable = true)
                else -> PaymentStatusReading.Confirming
            }
        }

    private companion object {
        const val OUTSTANDING_PAID = "paid"
        const val OUTSTANDING_FAILED = "failed"
    }
}

/** The server's ride payment status in the coordinator's vocabulary. */
fun RidePayment.toReading(): PaymentStatusReading = when (status) {
    RidePaymentStatus.REFUNDED -> PaymentStatusReading.Refunded
    // Part of the money is coming back: never shown as simply paid.
    RidePaymentStatus.PARTIALLY_REFUNDED -> PaymentStatusReading.RefundPending
    RidePaymentStatus.PAID, RidePaymentStatus.CASH_CONFIRMED -> PaymentStatusReading.Paid
    // A failed attempt can be retried on the SAME ride with a new intent, or switched to cash.
    RidePaymentStatus.FAILED -> PaymentStatusReading.Failed(retryable = true)
    RidePaymentStatus.CASH_PENDING, RidePaymentStatus.CONFIRMING, RidePaymentStatus.UNKNOWN -> PaymentStatusReading.Confirming
}

/**
 * The sheet session from the intent response, stamped Mopedu's; null when the
 * server attached no `client_session` (the fare cannot be paid online now).
 * The amount and currency are the intent's own, priced by the server.
 */
fun RidePaymentIntent.toPaymentSession(description: String): PaymentSession? {
    val session = clientSession.takeIf { it.isNotEmpty() } ?: return null
    return PaymentSession.fromClientSession(
        applicationId = MOPEDU_PAYMENT_APPLICATION_ID,
        clientSession = session,
        amountMinor = amount.paise,
        currency = currency,
        description = description,
    )
}

/**
 * Opens the sheet for a Mopedu payment from the Activity (`MainActivity`),
 * because the provider sheet opens onto an Activity and calls back on it, then
 * publishes exactly one ending onto Mopedu's stream of [PaymentHandoff].
 *
 * The intent (and so the server session) already exists: the ride screen
 * created it before asking for the sheet.
 */
@Singleton
class MopeduPaymentOpener @Inject constructor(
    private val payments: PaymentCoordinator,
    private val handoff: PaymentHandoff,
) {

    fun start(activity: Activity, request: MopeduPaymentRequest) {
        payments.launch(activity, request.attempt, request.session) { sheet ->
            handoff.publish(sheet.toHandoffEvent())
        }
    }

    /** Releases the launcher's one in-flight slot when the ride screen goes away. */
    fun abandon(request: MopeduPaymentRequest) {
        payments.abandon(request.attempt)
    }
}

/** The status the outstanding list reports for [chargeId]: null once it has left the list. */
internal suspend fun MopeduRiderRepository.outstandingStatus(chargeId: String): MopeduResult<String?> =
    when (val result = outstanding()) {
        is MopeduResult.Failure -> result
        is MopeduResult.Success -> MopeduResult.Success(result.value.firstOrNull { it.id == chargeId }?.status)
    }
