package com.us.android.feature.premium.payment

import android.app.Activity
import com.us.android.core.common.result.AppResult
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentSession
import com.us.android.core.payments.PaymentStatusReading
import com.us.android.core.payments.PaymentStatusSource
import com.us.android.core.payments.toHandoffEvent
import com.us.android.feature.premium.data.PassStatus
import com.us.android.feature.premium.data.PremiumCheckout
import com.us.android.feature.premium.data.PremiumPass
import com.us.android.feature.premium.data.PremiumRepository
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Premium's application id in `:core:payments` — the ONE place it is named.
 * It matches the payments-service application `premium` that
 * monetization-service opens the intent under (internal/payments/premium.go).
 * Every attempt, session, confirmation and in-flight record is stamped with
 * it, so a pending Premium payment can never be resumed or shown in MStore,
 * Feast, Dating or memberships, and theirs never reach here.
 */
const val PREMIUM_PAYMENT_APPLICATION_ID = "premium"

/**
 * What the Premium screen hands the Activity to open the sheet: the attempt
 * (always Premium's) and the session built from the server's
 * `client_session`. In memory only; the saved state holds identities alone.
 */
data class PremiumPaymentRequest(
    val attempt: PaymentAttempt,
    val session: PaymentSession,
) {
    init {
        require(attempt.applicationId == PREMIUM_PAYMENT_APPLICATION_ID) {
            "a Premium payment request cannot carry a ${attempt.applicationId} attempt"
        }
        require(session.applicationId == PREMIUM_PAYMENT_APPLICATION_ID) {
            "a Premium payment request cannot carry a ${session.applicationId} session"
        }
    }

    /** The pass id — what the poll is keyed by. */
    val passId: String get() = attempt.referenceId
}

/**
 * The sheet session from a checkout, stamped Premium's; null when there is
 * nothing the app can open (no pass, no `client_session`, a non-Razorpay
 * provider, no key or order, no amount).
 *
 * The amount is the server's `amount_paise` — the GST-inclusive total —
 * relayed to Razorpay for its sheet, never sent to our server; with
 * `order_id` present Razorpay prices the sheet from the ORDER anyway.
 */
fun PremiumCheckout.toPaymentSession(description: String): PaymentSession? {
    if (passId.isBlank() || clientSession.isEmpty() || amountPaise <= 0) return null
    val provider = clientSession[PaymentSession.CLIENT_SESSION_PROVIDER].orEmpty()
    if (provider.isNotEmpty() && provider != PROVIDER_RAZORPAY) return null
    if (clientSession[PaymentSession.CLIENT_SESSION_ORDER_ID].isNullOrBlank()) return null
    if (clientSession[PaymentSession.CLIENT_SESSION_KEY_ID].isNullOrBlank()) return null
    return PaymentSession.fromClientSession(
        applicationId = PREMIUM_PAYMENT_APPLICATION_ID,
        clientSession = clientSession,
        amountMinor = amountPaise,
        currency = currency,
        description = description,
    )
}

private const val PROVIDER_RAZORPAY = "razorpay"

/** The pass status in the coordinator's vocabulary. A refund outranks paid; unknown keeps confirming. */
fun PremiumPass.toReading(): PaymentStatusReading = when (status) {
    PassStatus.REFUNDED -> PaymentStatusReading.Refunded
    PassStatus.PAID -> PaymentStatusReading.Paid
    // A failed (or abandoned) pass: Buy again opens a new checkout.
    PassStatus.FAILED -> PaymentStatusReading.Failed(retryable = true)
    PassStatus.CONFIRMING -> PaymentStatusReading.Confirming
}

/**
 * `GET /premium/passes/:id`, read for the coordinator. A read that could not
 * be made is [PaymentStatusReading.Unreachable] — never Failed. It keeps the
 * last pass it read, so the success view can show the server's end date,
 * receipt and invoice without asking again.
 */
class PremiumPassStatusSource(
    private val repository: PremiumRepository,
) : PaymentStatusSource {

    override val applicationId: String = PREMIUM_PAYMENT_APPLICATION_ID

    @Volatile
    var lastPass: PremiumPass? = null
        private set

    override suspend fun status(referenceId: String): PaymentStatusReading =
        when (val result = repository.pass(referenceId)) {
            is AppResult.Failure -> PaymentStatusReading.Unreachable(result.error.javaClass.simpleName)
            is AppResult.Success -> {
                lastPass = result.data
                result.data.toReading()
            }
        }
}

/**
 * Opens the sheet for a Premium payment onto the Activity (the provider SDK
 * opens onto an Activity and calls back on it — `MainActivity`), then
 * publishes exactly one ending onto Premium's stream of [PaymentHandoff].
 * The checkout (and so the server session) already exists.
 */
@Singleton
class PremiumPaymentOpener @Inject constructor(
    private val payments: PaymentCoordinator,
    private val handoff: PaymentHandoff,
) {

    fun start(activity: Activity, request: PremiumPaymentRequest) {
        payments.launch(activity, request.attempt, request.session) { sheet ->
            handoff.publish(sheet.toHandoffEvent())
        }
    }

    /** Releases the launcher's one in-flight slot when the screen that asked goes away. */
    fun abandon(request: PremiumPaymentRequest) {
        payments.abandon(request.attempt)
    }
}
