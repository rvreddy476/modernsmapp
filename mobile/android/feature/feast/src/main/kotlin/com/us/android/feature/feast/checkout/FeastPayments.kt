package com.us.android.feature.feast.checkout

import android.app.Activity
import android.util.Log
import com.us.android.core.food.network.OrderPaymentDto
import com.us.android.core.food.network.PaymentIntentDto
import com.us.android.core.food.repository.FeastRepository
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.serverMessage
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentHandoffEvent
import com.us.android.core.payments.PaymentSession
import com.us.android.core.payments.PaymentStatusReading
import com.us.android.core.payments.PaymentStatusSource
import com.us.android.core.payments.toHandoffEvent
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Feast's application id in `:core:payments` — the ONE place Feast names it.
 *
 * Every Feast payment attempt, session, confirmation and in-flight record is
 * stamped with it, so a pending Feast payment can never be resumed or shown in
 * MStore checkout, and an MStore ending never reaches Feast. Client-side only
 * today (see `PaymentApplication`); provisional until the founder confirms the
 * registry key.
 */
const val FEAST_PAYMENT_APPLICATION_ID = "feast"

/** The instruments food-service enables at launch (PAYMENT_METHOD_INVALID for anything else). */
enum class FeastPaymentMethod(val wire: String, val label: String) {
    UPI("upi", "UPI"),
    CARD("card", "Card"),
    ;

    companion object {
        fun fromWire(wire: String?): FeastPaymentMethod = entries.firstOrNull { it.wire == wire } ?: UPI
    }
}

/**
 * What the checkout screen hands `:app` to open the sheet: the attempt (always
 * Feast's), the order number for the sheet's description, and the instrument.
 */
data class FeastPaymentRequest(
    val attempt: PaymentAttempt,
    val orderNumber: String,
    val method: FeastPaymentMethod,
) {
    init {
        require(attempt.applicationId == FEAST_PAYMENT_APPLICATION_ID) {
            "a Feast payment request cannot carry a ${attempt.applicationId} attempt"
        }
    }

    val orderId: String get() = attempt.referenceId
}

/**
 * `GET /v1/food/orders/:id/payment`, read for the coordinator.
 *
 * A read that could not be made is [PaymentStatusReading.Unreachable] — never
 * Failed. A refund outranks paid. An unknown status keeps confirming.
 */
class FeastPaymentStatusSource(
    private val repository: FeastRepository,
) : PaymentStatusSource {

    override val applicationId: String = FEAST_PAYMENT_APPLICATION_ID

    override suspend fun status(referenceId: String): PaymentStatusReading =
        when (val result = repository.orderPayment(referenceId)) {
            is FoodResult.Failure -> PaymentStatusReading.Unreachable(result.error.toString())
            is FoodResult.Success -> result.value.toReading()
        }
}

/** The server's payment status in the coordinator's vocabulary. */
internal fun OrderPaymentDto.toReading(): PaymentStatusReading {
    val refund = refundStatus?.trim()?.lowercase().orEmpty()
    return when {
        refund in REFUND_DONE -> PaymentStatusReading.Refunded
        refund.isNotEmpty() -> PaymentStatusReading.RefundPending
        status == "paid" -> PaymentStatusReading.Paid
        // The server keeps a failed attempt's order pending for 15 minutes, so
        // the customer retries on the SAME order.
        status == "failed" -> PaymentStatusReading.Failed(retryable = true)
        else -> PaymentStatusReading.Confirming
    }
}

private val REFUND_DONE = setOf("refunded", "succeeded", "processed", "completed")

/**
 * Opens payment for a Feast order, from the Activity (`MainActivity`), because
 * the provider sheet opens onto an Activity and calls back on it:
 *
 *  1. ask the SERVER for the intent — the client names an order and an
 *     instrument, never an amount;
 *  2. hand the server's `client_session` to the coordinator, stamped `feast`,
 *     with the registry's merchant name relayed as received;
 *  3. publish exactly one ending onto Feast's stream of [PaymentHandoff].
 *
 * The attempt id is the intent's Idempotency-Key: a resent request for the
 * same attempt is the same intent; a retry is a new attempt and a new key.
 */
@Singleton
class FeastPaymentOpener @Inject constructor(
    private val repository: FeastRepository,
    private val payments: PaymentCoordinator,
    private val handoff: PaymentHandoff,
) {

    fun start(activity: Activity, scope: CoroutineScope, request: FeastPaymentRequest) {
        val attempt = request.attempt
        scope.launch {
            when (val result = repository.createPaymentIntent(request.orderId, attempt.id, request.method.wire)) {
                is FoodResult.Failure -> {
                    Log.w(TAG, "could not open Feast payment for ${request.orderId}: ${result.error}")
                    handoff.publish(
                        PaymentHandoffEvent.Unavailable(
                            attempt = attempt,
                            reason = result.error.serverMessage ?: "We couldn't start the payment. Please try again.",
                        ),
                    )
                }

                is FoodResult.Success -> {
                    val intent = result.value
                    if (intent.clientSession.isNullOrEmpty()) {
                        handoff.publish(
                            PaymentHandoffEvent.Unavailable(attempt, "Online payment isn't available right now."),
                        )
                        return@launch
                    }
                    payments.launch(activity, attempt, intent.toPaymentSession(request.orderNumber)) { sheet ->
                        handoff.publish(sheet.toHandoffEvent())
                    }
                }
            }
        }
    }

    /** Releases the launcher's one in-flight slot when the checkout screen goes away. */
    fun abandon(request: FeastPaymentRequest) {
        payments.abandon(request.attempt)
    }

    private companion object {
        const val TAG = "FeastPayment"
    }
}

/**
 * The sheet session from the server's intent, stamped Feast's. The amount and
 * currency are the server intent's own figures, relayed; Razorpay prices the
 * sheet from the provider order regardless.
 */
internal fun PaymentIntentDto.toPaymentSession(orderNumber: String): PaymentSession =
    PaymentSession.fromClientSession(
        applicationId = FEAST_PAYMENT_APPLICATION_ID,
        clientSession = clientSession.orEmpty(),
        amountMinor = paymentIntent?.amountMinor ?: amount.value,
        currency = paymentIntent?.currency?.takeIf { it.isNotBlank() } ?: currency,
        description = "Feast order $orderNumber",
        merchantDisplayName = clientSession?.get(PaymentSession.CLIENT_SESSION_MERCHANT_DISPLAY_NAME),
    )
