package com.us.android.payment

import android.app.Activity
import android.util.Log
import com.us.android.core.commerce.payment.PaymentAttempt
import com.us.android.core.commerce.payment.PaymentHandoff
import com.us.android.core.commerce.payment.PaymentHandoffEvent
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Opens payment for an order, end to end.
 *
 * `:app` owns this because it is where the provider choice lives. The flow:
 *
 *  1. ask the SERVER to open (or return) the payment intent for this order —
 *     the client names an order, never an amount (LB-4);
 *  2. hand the server's `client_session` to the launcher, so the publishable
 *     key is the one the provider order was created against;
 *  3. publish the outcome, whatever it is, so the checkout screen polls the
 *     server for the truth.
 *
 * Every path publishes exactly one event. A path that opened a sheet and then
 * returned nothing would leave the buyer on a spinner forever, which is worse
 * than any error message.
 */
@Singleton
class CheckoutPaymentCoordinator @Inject constructor(
    private val repo: CommerceRepository,
    private val launcher: PaymentLauncher,
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
                            attempt = attempt,
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
                                attempt = attempt,
                                reason = "Payment isn't available for this order right now.",
                            ),
                        )
                        return@launch
                    }

                    // The amount and currency travel with the session, from
                    // the SERVER's intent. The client relays them; it does not
                    // choose them, and Razorpay prices the sheet from the
                    // order anyway.
                    val session = handle.clientSession +
                        mapOf(
                            "amount_minor" to handle.amount.value.toString(),
                            "currency" to handle.currency,
                        )

                    launcher.open(
                        activity = activity,
                        attempt = attempt,
                        session = session,
                        description = "Order $orderNumber",
                    ) { outcome ->
                        // The production mapper, in PaymentLauncher.kt, which
                        // the tests invoke directly (C3-LB-4).
                        handoff.publish(outcome.toHandoffEvent(attempt))
                    }
                }
            }
        }
    }

    private companion object {
        const val TAG = "CheckoutPayment"
    }
}
