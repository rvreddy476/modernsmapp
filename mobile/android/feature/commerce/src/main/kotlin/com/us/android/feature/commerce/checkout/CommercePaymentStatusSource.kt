package com.us.android.feature.commerce.checkout

import com.us.android.core.commerce.model.OrderPayment
import com.us.android.core.commerce.model.OrderPaymentState
import com.us.android.core.commerce.model.PaymentStatus
import com.us.android.core.commerce.repository.CommerceError
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.core.commerce.repository.isPermanentRefusal
import com.us.android.core.payments.PaymentStatusReading
import com.us.android.core.payments.PaymentStatusSource

/**
 * MStore's [PaymentStatusSource]: the three-state
 * `GET /v1/commerce/orders/:id/payment` (contract §4.5), through the
 * repository, in the coordinator's vocabulary.
 *
 * 2026-09-30, three rules:
 *
 *  * A read that could not be made — network, 5xx, an undecodable body —
 *    is [PaymentStatusReading.Unreachable], never a failed payment. "We do
 *    not know yet" and "the payment failed" are different facts.
 *  * A PERMANENT refusal — ORDER_NOT_FOUND, a 403, any 4xx the server will
 *    keep giving — is [PaymentStatusReading.Failed], so the poll STOPS.
 *    Before this every failure was Unreachable and the coordinator polled a
 *    missing order for the full 180 s.
 *  * A server that predates the route (a bare 404, [CommerceError.NotAvailable])
 *    falls back ONCE, for this attempt, to the older `/payment/status` read
 *    and derives the reading as before. Once fallen back, this source stays
 *    on the older route: a route that was absent a second ago is still
 *    absent, and re-asking on every tick would double the poll's traffic.
 *
 * One source per checkout ViewModel, so the fallback latch lives exactly as
 * long as the screen that polls: the next checkout asks the new route again.
 *
 * The application id is not sent: the endpoint does not take one yet.
 */
internal class CommercePaymentStatusSource(
    private val repo: CommerceRepository,
) : PaymentStatusSource {

    override val applicationId: String = MSTORE_PAYMENT_APPLICATION_ID

    /** Set once the three-state route has answered "no such route" for this attempt. */
    @Volatile
    private var legacy = false

    override suspend fun status(referenceId: String): PaymentStatusReading {
        if (legacy) return legacyStatus(referenceId)
        return when (val result = repo.orderPayment(referenceId)) {
            is CommerceResult.Success -> result.value.toReading()
            is CommerceResult.Failure -> when {
                result.error is CommerceError.NotAvailable -> {
                    legacy = true
                    legacyStatus(referenceId)
                }

                else -> result.error.toReading()
            }
        }
    }

    private suspend fun legacyStatus(referenceId: String): PaymentStatusReading =
        when (val result = repo.paymentStatus(referenceId)) {
            is CommerceResult.Failure -> result.error.toReading()
            is CommerceResult.Success -> result.value.toReading()
        }
}

/**
 * A failed READ, as a reading: a permanent refusal stops the poll as
 * [PaymentStatusReading.Failed] (not retryable — there is no order to retry
 * on); anything else keeps confirming as [PaymentStatusReading.Unreachable].
 */
internal fun CommerceError.toReading(): PaymentStatusReading =
    if (isPermanentRefusal()) {
        PaymentStatusReading.Failed(reason = toString(), retryable = false)
    } else {
        PaymentStatusReading.Unreachable(toString())
    }

/**
 * The three-state payment, as a reading. A refund outranks paid: a capture
 * that landed after the order lapsed is being returned and must never render
 * as a successful order. An unrecognised state keeps confirming.
 */
internal fun OrderPayment.toReading(): PaymentStatusReading = when {
    refundStatus != null && refundStatus in REFUND_DONE -> PaymentStatusReading.Refunded
    !refundStatus.isNullOrBlank() -> PaymentStatusReading.RefundPending
    state == OrderPaymentState.PAID -> PaymentStatusReading.Paid
    // A failed commerce payment keeps its order, and the intent route
    // re-opens it (contract §4.7), so checkout offers "Try payment again".
    state == OrderPaymentState.FAILED -> PaymentStatusReading.Failed(retryable = true)
    else -> PaymentStatusReading.Confirming
}

private val REFUND_DONE = setOf("refunded", "partially_refunded")

/** Commerce's older payment status, as a reading. Internal so a test can pin it. */
internal fun PaymentStatus.toReading(): PaymentStatusReading = when (this) {
    PaymentStatus.PAID -> PaymentStatusReading.Paid
    // A failed commerce payment keeps its order, and checkout offers
    // "Try payment again" on it.
    PaymentStatus.FAILED -> PaymentStatusReading.Failed(retryable = true)
    PaymentStatus.REFUND_PENDING -> PaymentStatusReading.RefundPending
    PaymentStatus.REFUNDED -> PaymentStatusReading.Refunded
    PaymentStatus.PENDING,
    PaymentStatus.AWAITING_CONFIRMATION,
    PaymentStatus.UNKNOWN,
    -> PaymentStatusReading.Confirming
}
