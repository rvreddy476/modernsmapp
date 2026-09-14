package com.us.android.feature.commerce.checkout

import com.us.android.core.commerce.model.PaymentStatus
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.core.payments.PaymentStatusReading
import com.us.android.core.payments.PaymentStatusSource

/**
 * MStore's [PaymentStatusSource]: `GET /v1/commerce/orders/:id/payment/status`,
 * through the repository, in the coordinator's vocabulary.
 *
 * The mapping is the one the checkout poll applied before `:core:payments`
 * existed:
 *
 *  * a failed READ (network, 5xx, an error envelope) keeps confirming — it is
 *    [PaymentStatusReading.Unreachable], never a failed payment;
 *  * `refund_pending` / `refunded` (and `partially_refunded`) are LB-22's late
 *    capture after the hold expired, which checkout renders as Expired;
 *  * pending, processing and anything unrecognised keep confirming.
 *
 * The application id is not sent: the endpoint does not take one yet.
 */
internal class CommercePaymentStatusSource(
    private val repo: CommerceRepository,
) : PaymentStatusSource {

    override val applicationId: String = MSTORE_PAYMENT_APPLICATION_ID

    override suspend fun status(referenceId: String): PaymentStatusReading =
        when (val result = repo.paymentStatus(referenceId)) {
            is CommerceResult.Failure -> PaymentStatusReading.Unreachable(result.error.toString())
            is CommerceResult.Success -> result.value.toReading()
        }
}

/** Commerce's payment status, as a reading. Internal so a test can pin it. */
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
