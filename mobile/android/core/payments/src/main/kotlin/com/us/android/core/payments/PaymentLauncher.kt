package com.us.android.core.payments

import android.app.Activity

/**
 * The PSP handoff, as a port.
 *
 * Products never name a provider. They build a [PaymentSession] from what
 * their server returned and open it through [PaymentCoordinator]; which SDK
 * presents the sheet is decided by the binding in [PaymentsModule], so swapping
 * one is a change there plus a new implementation of this interface — not a
 * change to any screen, ViewModel or route.
 *
 * ## What a result from here means, and what it does not
 *
 * [PaymentOutcome] is **evidence about the SDK flow, not a payment fact**.
 * A1/R-3: the server marks a payment paid only when a signature-verified
 * provider webhook arrives, so every outcome below — including
 * [PaymentOutcome.Succeeded] — leads to the same place: the app polls the
 * product's status endpoint and believes the server.
 *
 * This is why [PaymentOutcome.Failed] is not treated as "the payment failed"
 * either. A client-reported failure can coexist with a capture that did
 * complete at the PSP — a dropped callback, a killed process, a network flap
 * on the way back. Reporting failure locally and skipping the poll is how an
 * app tells someone their payment failed while their money is gone.
 */
interface PaymentLauncher {

    /**
     * Opens the provider's checkout sheet.
     *
     * @param attempt the exact reference-and-attempt this opening belongs to.
     *   C3-LB-4: a buyer may retry the same order, so the reference alone
     *   cannot distinguish a late callback from the current one.
     * @param session what the server said the SDK needs — provider name, its
     *   order handle and the publishable key. Server-sourced so the key
     *   cannot disagree with the order it belongs to.
     * @param onOutcome called once, on the main thread, whatever happens.
     */
    fun open(
        activity: Activity,
        attempt: PaymentAttempt,
        session: PaymentSession,
        onOutcome: (PaymentOutcome) -> Unit,
    )

    /**
     * Abandons [attempt]'s in-flight sheet without delivering an outcome, when
     * the screen that opened it has gone. A no-op for any other attempt.
     */
    fun abandon(attempt: PaymentAttempt)
}

/**
 * Where the Activity forwards the provider SDK's result.
 *
 * Deliberately NOT part of [PaymentLauncher]: nothing that opens a sheet should
 * be able to inject a result into one. Only [ActivityPaymentHost] holds this.
 */
interface PaymentResultSink {

    /** Delivers the SDK's result to whatever sheet is in flight. */
    fun deliver(outcome: PaymentOutcome)
}

/**
 * What the SDK reported. None of these is a payment fact — see the note on
 * [PaymentLauncher].
 */
sealed interface PaymentOutcome {

    /** The SDK reported a completed flow. The server has not confirmed it. */
    data class Succeeded(val providerPaymentId: String?) : PaymentOutcome

    /**
     * The SDK reported a failure. The payment may STILL have been captured;
     * the caller polls the server rather than concluding anything.
     */
    data class Failed(val code: Int?, val message: String?) : PaymentOutcome

    /** The buyer dismissed the sheet. Also not proof of anything. */
    data object Cancelled : PaymentOutcome

    /**
     * The sheet could not be opened at all — no session from the server, a
     * provider that cannot derive one, or a malformed payload.
     *
     * Distinct from [Failed] because nothing was ever presented to the buyer,
     * so no payment can have been taken and the app can say so plainly.
     */
    data class Unavailable(val reason: String) : PaymentOutcome
}

/**
 * How one opening of the sheet ended, as far as the product needs to know.
 *
 * There is no "paid" here, by construction: the only way to learn that is
 * [PaymentCoordinator.confirm].
 */
sealed interface PaymentSheetResult {
    val attempt: PaymentAttempt
    val applicationId: String get() = attempt.applicationId
    val referenceId: String get() = attempt.referenceId

    /**
     * The provider sheet ended, however it ended. The caller polls the server;
     * it must not conclude success or failure from this.
     */
    data class Closed(override val attempt: PaymentAttempt) : PaymentSheetResult

    /**
     * The sheet could not be opened. Nothing was presented to the buyer, so
     * no payment can have been taken.
     */
    data class Unavailable(
        override val attempt: PaymentAttempt,
        val reason: String,
    ) : PaymentSheetResult
}

/**
 * Maps an SDK outcome onto a sheet result.
 *
 * C3-LB-4 requires this to be PRODUCTION code that tests invoke, not a rule
 * restated inside a test. Review 3 found the test reimplementing the private
 * mapper, which meant it would stay green if production regressed — a proof
 * of the test's own copy, not of the behaviour.
 *
 * The policy, which is the most consequential decision in the payment flow:
 * success, failure and cancellation ALL become [PaymentSheetResult.Closed],
 * and the caller polls the server.
 *
 * That looks lossy and is deliberate. A client-reported failure can coexist
 * with a capture that completed — a dropped callback, a killed process, a
 * flaky network on the way back. Treating it as failure and skipping the poll
 * is how an app tells someone their payment failed while their money is gone.
 *
 * Only [PaymentOutcome.Unavailable], where no sheet was ever presented, is
 * reported as such: nothing was shown, so nothing can have been charged.
 */
fun PaymentOutcome.toSheetResult(attempt: PaymentAttempt): PaymentSheetResult =
    when (this) {
        is PaymentOutcome.Succeeded,
        is PaymentOutcome.Failed,
        PaymentOutcome.Cancelled,
        -> PaymentSheetResult.Closed(attempt)

        is PaymentOutcome.Unavailable ->
            PaymentSheetResult.Unavailable(attempt, reason)
    }
