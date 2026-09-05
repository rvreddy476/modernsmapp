package com.us.android.payment

import android.app.Activity
import com.us.android.core.commerce.payment.PaymentAttempt
import com.us.android.core.commerce.payment.PaymentHandoffEvent

/**
 * The PSP handoff, as a port.
 *
 * This lives in `:app` rather than in `:feature:commerce` on purpose. A
 * feature module must not know which provider is wired, or swapping one
 * becomes a change to every screen that touches payment. The feature exposes
 * an `onOpenPaymentSheet` callback; `:app` decides that means Razorpay.
 *
 * ## What a result from here means, and what it does not
 *
 * [PaymentSheetOutcome] is **evidence about the SDK flow, not a payment
 * fact**. A1/R-3: the server marks an order paid only when a
 * signature-verified provider webhook arrives, so every outcome below — including
 * [PaymentSheetOutcome.Succeeded] — leads to the same place: the app polls
 * `GET /orders/:id/payment/status` and believes the server.
 *
 * This is why [PaymentSheetOutcome.Failed] is not treated as "the payment
 * failed" either. A client-reported failure can coexist with a capture that
 * did complete at the PSP — a dropped callback, a killed process, a network
 * flap on the way back. Reporting failure locally and skipping the poll is
 * how an app tells someone their payment failed while their money is gone.
 */
interface PaymentLauncher {

    /**
     * Opens the provider's checkout sheet.
     *
     * @param attempt the exact order-and-attempt this opening belongs to.
     *   C3-LB-4: a buyer may retry the same order, so the order id alone
     *   cannot distinguish a late callback from the current one.
     * @param session what the server said the SDK needs — provider name, its
     *   order handle and the publishable key. Server-sourced so the key
     *   cannot disagree with the order it belongs to.
     * @param onOutcome called once, on the main thread, whatever happens.
     */
    fun open(
        activity: Activity,
        attempt: PaymentAttempt,
        session: Map<String, String>,
        description: String,
        onOutcome: (PaymentSheetOutcome) -> Unit,
    )
}

/**
 * What the SDK reported. None of these is a payment fact — see the note on
 * [PaymentLauncher].
 */
sealed interface PaymentSheetOutcome {

    /** The SDK reported a completed flow. The server has not confirmed it. */
    data class Succeeded(val providerPaymentId: String?) : PaymentSheetOutcome

    /**
     * The SDK reported a failure. The payment may STILL have been captured;
     * the caller polls the server rather than concluding anything.
     */
    data class Failed(val code: Int?, val message: String?) : PaymentSheetOutcome

    /** The buyer dismissed the sheet. Also not proof of anything. */
    data object Cancelled : PaymentSheetOutcome

    /**
     * The sheet could not be opened at all — no session from the server, a
     * provider that cannot derive one, or a malformed payload.
     *
     * Distinct from [Failed] because nothing was ever presented to the buyer,
     * so no payment can have been taken and the app can say so plainly.
     */
    data class Unavailable(val reason: String) : PaymentSheetOutcome
}

/**
 * Maps an SDK outcome onto a handoff event.
 *
 * C3-LB-4 requires this to be PRODUCTION code that tests invoke, not a rule
 * restated inside a test. Review 3 found the test reimplementing the private
 * mapper, which meant it would stay green if production regressed — a proof
 * of the test's own copy, not of the behaviour.
 *
 * The policy, which is the most consequential decision in the payment flow:
 * success, failure and cancellation ALL become [PaymentHandoffEvent.SheetClosed],
 * and the caller polls the server.
 *
 * That looks lossy and is deliberate. A client-reported failure can coexist
 * with a capture that completed — a dropped callback, a killed process, a
 * flaky network on the way back. Treating it as failure and skipping the poll
 * is how an app tells someone their payment failed while their money is gone.
 *
 * Only [PaymentSheetOutcome.Unavailable], where no sheet was ever presented,
 * is reported as such: nothing was shown, so nothing can have been charged.
 */
fun PaymentSheetOutcome.toHandoffEvent(attempt: PaymentAttempt): PaymentHandoffEvent =
    when (this) {
        is PaymentSheetOutcome.Succeeded,
        is PaymentSheetOutcome.Failed,
        PaymentSheetOutcome.Cancelled,
        -> PaymentHandoffEvent.SheetClosed(attempt)

        is PaymentSheetOutcome.Unavailable ->
            PaymentHandoffEvent.Unavailable(attempt, reason)
    }
