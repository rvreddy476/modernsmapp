package com.us.android.feature.dating.premium

import android.app.Activity
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.network.PremiumPaymentDto
import com.us.android.feature.dating.network.PremiumPurchaseResultDto
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentSession
import com.us.android.core.payments.PaymentStatusReading
import com.us.android.core.payments.PaymentStatusSource
import com.us.android.core.payments.toHandoffEvent
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Dating's application id in `:core:payments` — the ONE place Dating names it.
 *
 * Every Premium attempt, session, confirmation and in-flight record is stamped
 * with it, so a pending Dating payment can never be resumed or shown in Feast
 * or MStore checkout, and their endings never reach Dating. It matches the
 * payments-service application `dating` (P1).
 */
const val DATING_PAYMENT_APPLICATION_ID = "dating"

/**
 * What the Premium screen hands `:app` to open the sheet: the attempt (always
 * Dating's) and the session built from the server's `client_session`.
 *
 * Carried in memory only, through a navigation callback. Nothing here is ever
 * written to saved state — the in-flight record holds identities alone.
 */
data class DatingPaymentRequest(
    val attempt: PaymentAttempt,
    val session: PaymentSession,
) {
    init {
        require(attempt.applicationId == DATING_PAYMENT_APPLICATION_ID) {
            "a Dating payment request cannot carry a ${attempt.applicationId} attempt"
        }
        require(session.applicationId == DATING_PAYMENT_APPLICATION_ID) {
            "a Dating payment request cannot carry a ${session.applicationId} session"
        }
    }

    val purchaseId: String get() = attempt.referenceId
}

/**
 * `GET /v1/dating/premium/purchases/:id/payment`, read for the coordinator.
 *
 * A read that could not be made is [PaymentStatusReading.Unreachable] — never
 * Failed. A refund outranks paid. An unknown status keeps confirming.
 */
class DatingPaymentStatusSource(
    private val repository: DatingRepository,
) : PaymentStatusSource {

    override val applicationId: String = DATING_PAYMENT_APPLICATION_ID

    override suspend fun status(referenceId: String): PaymentStatusReading =
        when (val result = repository.purchasePayment(referenceId)) {
            is DatingResult.Failure -> PaymentStatusReading.Unreachable(result.error.toString())
            is DatingResult.Success -> result.value.toReading()
        }
}

/** The server's Premium payment status in the coordinator's vocabulary. */
internal fun PremiumPaymentDto.toReading(): PaymentStatusReading {
    val refund = refundStatus?.trim()?.lowercase().orEmpty()
    return when {
        refund == REFUNDED -> PaymentStatusReading.Refunded
        // partially_refunded, or a refund state newer than this app: money is
        // coming back, so the pass is never shown as simply bought.
        refund.isNotEmpty() -> PaymentStatusReading.RefundPending
        status == PAID -> PaymentStatusReading.Paid
        // A failed purchase has nothing left to pay (the same key returns no
        // client_session), so a retry is a NEW purchase.
        status == FAILED -> PaymentStatusReading.Failed(retryable = true)
        else -> PaymentStatusReading.Confirming
    }
}

private const val REFUNDED = "refunded"
private const val PAID = "paid"
private const val FAILED = "failed"

/**
 * The sheet session from the purchase response, stamped Dating's; null when the
 * server attached no `client_session` (the purchase cannot be paid now). The
 * amount and currency are the purchase's own, priced by the server's catalogue.
 */
internal fun PremiumPurchaseResultDto.toPaymentSession(productName: String): PaymentSession? {
    val clientSession = clientSession?.takeIf { it.isNotEmpty() } ?: return null
    return PaymentSession.fromClientSession(
        applicationId = DATING_PAYMENT_APPLICATION_ID,
        clientSession = clientSession,
        amountMinor = purchase.amountMinor,
        currency = purchase.currency,
        description = productName,
    )
}

/**
 * Opens the sheet for a Dating purchase from the Activity (`MainActivity`),
 * because the provider sheet opens onto an Activity and calls back on it, then
 * publishes exactly one ending onto Dating's stream of [PaymentHandoff].
 *
 * The purchase (and so the server session) already exists: the Premium screen
 * created it under a saved idempotency key before asking for the sheet.
 */
@Singleton
class DatingPaymentOpener @Inject constructor(
    private val payments: PaymentCoordinator,
    private val handoff: PaymentHandoff,
) {

    fun start(activity: Activity, request: DatingPaymentRequest) {
        payments.launch(activity, request.attempt, request.session) { sheet ->
            handoff.publish(sheet.toHandoffEvent())
        }
    }

    /** Releases the launcher's one in-flight slot when the Premium screen goes away. */
    fun abandon(request: DatingPaymentRequest) {
        payments.abandon(request.attempt)
    }
}
