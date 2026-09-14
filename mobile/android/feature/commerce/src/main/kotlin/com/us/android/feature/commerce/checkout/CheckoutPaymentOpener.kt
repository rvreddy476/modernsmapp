package com.us.android.feature.commerce.checkout

import android.app.Activity
import android.util.Log
import com.us.android.core.commerce.model.PaymentHandle
import com.us.android.core.commerce.payment.PaymentAttempt
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentHandoffEvent
import com.us.android.core.payments.PaymentSession
import com.us.android.core.payments.PaymentSheetResult
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import javax.inject.Inject
import javax.inject.Singleton
import com.us.android.core.payments.PaymentAttempt as SheetAttempt

/**
 * Opens payment for a commerce order, end to end.
 *
 * This was `:app`'s `CheckoutPaymentCoordinator` until the payment sheet moved
 * to `:core:payments` (2026-09-14). It lives beside checkout now because it no
 * longer knows the provider: the generic [PaymentCoordinator] does. The flow:
 *
 *  1. ask the SERVER to open (or return) the payment intent for this order —
 *     the client names an order, never an amount (LB-4);
 *  2. hand the server's `client_session` to the coordinator, so the
 *     publishable key is the one the provider order was created against;
 *  3. publish the outcome, whatever it is, onto :core:payments' [PaymentHandoff], so the
 *     checkout screen polls the server for the truth.
 *
 * Every path publishes exactly one event. A path that opened a sheet and then
 * returned nothing would leave the buyer on a spinner forever, which is worse
 * than any error message.
 *
 * Every session and attempt is stamped with [MSTORE_PAYMENT_APPLICATION_ID].
 * The intent request is unchanged: the server does not take an application id
 * yet.
 *
 * Called from `:app`'s Activity, because the sheet opens onto an Activity.
 */
@Singleton
class CheckoutPaymentOpener @Inject constructor(
    private val repo: CommerceRepository,
    private val payments: PaymentCoordinator,
    private val handoff: PaymentHandoff,
) {

    fun start(
        activity: Activity,
        scope: CoroutineScope,
        attempt: PaymentAttempt,
        orderNumber: String,
    ) {
        val orderId = attempt.orderId
        scope.launch {
            when (val result = repo.openPayment(orderId)) {
                is CommerceResult.Failure -> {
                    Log.w(TAG, "could not open payment for $orderId: ${result.error}")
                    handoff.publish(
                        PaymentHandoffEvent.Unavailable(
                            attempt = attempt.toSheetAttempt(),
                            reason = "We couldn't start the payment. Please try again.",
                        ),
                    )
                }

                is CommerceResult.Success -> {
                    val handle = result.value
                    if (handle.clientSession.isEmpty()) {
                        // The provider could not derive a session (Cashfree
                        // needs a stored payment_session_id), or the server is
                        // configured without a publishable key. Either way the
                        // sheet cannot open, and saying so beats a spinner.
                        handoff.publish(
                            PaymentHandoffEvent.Unavailable(
                                attempt = attempt.toSheetAttempt(),
                                reason = "Payment isn't available for this order right now.",
                            ),
                        )
                        return@launch
                    }

                    val session = handle.toPaymentSession(orderNumber)

                    payments.launch(
                        activity = activity,
                        attempt = attempt.toSheetAttempt(),
                        session = session,
                    ) { sheet ->
                        handoff.publish(sheet.toHandoffEvent(attempt))
                    }
                }
            }
        }
    }

    /**
     * C3-LB-4: releases the launcher's single in-flight slot when the checkout
     * screen goes away, so a buyer who backs out mid-sheet is not refused on
     * every later attempt.
     */
    fun abandon(attempt: PaymentAttempt) {
        payments.abandon(attempt.toSheetAttempt())
    }

    private companion object {
        const val TAG = "CheckoutPayment"
    }
}

/**
 * The sheet session for a server payment handle, stamped as MStore's.
 *
 * The amount and currency travel with the session, from the SERVER's intent.
 * The client relays them; it does not choose them, and Razorpay prices the
 * sheet from the order anyway. `merchant_display_name` is relayed as received
 * (null from a server that omits it); the launcher cleans it and falls back.
 */
internal fun PaymentHandle.toPaymentSession(orderNumber: String): PaymentSession =
    PaymentSession.fromClientSession(
        applicationId = MSTORE_PAYMENT_APPLICATION_ID,
        clientSession = clientSession,
        amountMinor = amount.value,
        currency = currency,
        description = "Order $orderNumber",
        merchantDisplayName = clientSession[PaymentSession.CLIENT_SESSION_MERCHANT_DISPLAY_NAME],
    )

/**
 * A commerce attempt, as the payment sheet sees it: MStore's application, the
 * order, the attempt. Deterministic, so the attempt that abandons a sheet is
 * equal to the one that opened it.
 */
internal fun PaymentAttempt.toSheetAttempt(): SheetAttempt =
    SheetAttempt(applicationId = MSTORE_PAYMENT_APPLICATION_ID, referenceId = orderId, id = id)

/**
 * How the sheet ended, as a commerce handoff event, stamped with the COMMERCE
 * attempt the checkout ViewModel matches against. A closed sheet of any kind
 * is [PaymentHandoffEvent.SheetClosed] (poll the server); only a sheet that
 * never opened is [PaymentHandoffEvent.Unavailable].
 */
internal fun PaymentSheetResult.toHandoffEvent(attempt: PaymentAttempt): PaymentHandoffEvent =
    when (this) {
        is PaymentSheetResult.Closed -> PaymentHandoffEvent.SheetClosed(attempt.toSheetAttempt())
        is PaymentSheetResult.Unavailable -> PaymentHandoffEvent.Unavailable(attempt.toSheetAttempt(), reason)
    }
