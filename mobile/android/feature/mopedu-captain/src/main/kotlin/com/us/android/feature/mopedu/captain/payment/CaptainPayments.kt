package com.us.android.feature.mopedu.captain.payment

import android.app.Activity
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentSession
import com.us.android.core.payments.PaymentStatusReading
import com.us.android.core.payments.PaymentStatusSource
import com.us.android.core.payments.toHandoffEvent
import com.us.android.feature.mopedu.captain.data.CaptainResult
import com.us.android.feature.mopedu.captain.data.MopeduCaptainRepository
import com.us.android.feature.mopedu.captain.data.SubscriptionCheckout
import com.us.android.feature.mopedu.captain.data.SubscriptionPaymentStatus
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Mopedu's application id in `:core:payments` — ONE payments application for
 * the rider's fare and the captain's plan (2026-09-18).
 *
 * The rider feature names the same key; features may not share code, so it is
 * spelled here too. The two never cross even so: a ride payment's reference is
 * a ride id, a plan payment's is a subscription id, and each app is its own
 * process with its own [PaymentHandoff] slot — PaymentApplicationIsolationTest
 * proves the seams.
 */
const val MOPEDU_PAYMENT_APPLICATION_ID = "mopedu"

/**
 * What the plans screen hands `:app-captain` to open the sheet: the attempt
 * (always Mopedu's; its reference is the subscription id) and the session
 * built from checkout's `client_session`.
 *
 * Carried in memory only, through a navigation callback. Nothing here is ever
 * written to saved state — the in-flight record holds identities alone.
 */
data class CaptainPaymentRequest(
    val attempt: PaymentAttempt,
    val session: PaymentSession,
) {
    init {
        require(attempt.applicationId == MOPEDU_PAYMENT_APPLICATION_ID) {
            "a captain plan payment cannot carry a ${attempt.applicationId} attempt"
        }
        require(session.applicationId == MOPEDU_PAYMENT_APPLICATION_ID) {
            "a captain plan payment cannot carry a ${session.applicationId} session"
        }
    }

    val subscriptionId: String get() = attempt.referenceId
}

/**
 * `GET /v1/rider/subscriptions/me/payment`, read for the coordinator.
 *
 * The route has no id: it answers for the captain's current plan payment, and
 * the reference the coordinator confirms is the subscription id checkout
 * returned. A read that could not be made is [PaymentStatusReading.Unreachable]
 * — never Failed. An unknown status keeps confirming. Only the server's `paid`
 * is paid.
 */
class SubscriptionPaymentStatusSource(
    private val repository: MopeduCaptainRepository,
) : PaymentStatusSource {

    override val applicationId: String = MOPEDU_PAYMENT_APPLICATION_ID

    override suspend fun status(referenceId: String): PaymentStatusReading =
        when (val result = repository.subscriptionPayment()) {
            is CaptainResult.Failure -> PaymentStatusReading.Unreachable(result.error.toString())
            is CaptainResult.Success -> when (result.value.status) {
                SubscriptionPaymentStatus.PAID -> PaymentStatusReading.Paid
                // A failed attempt is retried with a NEW checkout of the same plan.
                SubscriptionPaymentStatus.FAILED -> PaymentStatusReading.Failed(retryable = true)
                SubscriptionPaymentStatus.PENDING, SubscriptionPaymentStatus.CONFIRMING, SubscriptionPaymentStatus.UNKNOWN ->
                    PaymentStatusReading.Confirming
            }
        }
}

/**
 * The sheet session from the checkout answer, stamped Mopedu's; null when the
 * server attached no `client_session` (the trial, or a plan that cannot be
 * paid online now). The amount and currency are the server's own figures.
 */
fun SubscriptionCheckout.toPaymentSession(description: String): PaymentSession? {
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
 * Opens the sheet for a plan payment from the Activity (`CaptainActivity`),
 * because the provider sheet opens onto an Activity and calls back on it, then
 * publishes exactly one ending onto Mopedu's stream of [PaymentHandoff].
 *
 * Checkout (and so the server session) already happened: the plans screen
 * created the subscription before asking for the sheet.
 */
@Singleton
class CaptainPaymentOpener @Inject constructor(
    private val payments: PaymentCoordinator,
    private val handoff: PaymentHandoff,
) {

    fun start(activity: Activity, request: CaptainPaymentRequest) {
        payments.launch(activity, request.attempt, request.session) { sheet ->
            handoff.publish(sheet.toHandoffEvent())
        }
    }

    /** Releases the launcher's one in-flight slot when the plans screen goes away. */
    fun abandon(request: CaptainPaymentRequest) {
        payments.abandon(request.attempt)
    }
}
