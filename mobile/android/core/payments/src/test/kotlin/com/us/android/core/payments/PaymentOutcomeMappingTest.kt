package com.us.android.core.payments

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The single most dangerous decision in the payment handoff: what a client
 * callback is allowed to mean.
 *
 * A1/R-3 — a payment becomes paid only when a signature-verified provider
 * webhook reaches the server. So the SDK's opinion is evidence about where to
 * look, never a fact about money. This pins the two rules that follow:
 *
 *  1. Every ENDING of the sheet — success, failure, cancellation — maps to
 *     [PaymentSheetResult.Closed], which makes the app poll the server.
 *     Mapping a reported failure to a failure state would be the dangerous
 *     one: a dropped callback or a killed process can sit on top of a capture
 *     that completed, and telling someone their payment failed while their
 *     money is gone is the worst outcome this flow can produce.
 *
 *  2. Only [PaymentOutcome.Unavailable] — where no sheet was ever shown —
 *     maps to an unavailable result, because then no payment CAN have been
 *     taken and making the buyer wait through a poll would be dishonest in
 *     the other direction.
 *
 * ## C3-LB-4: this calls the production mapper
 *
 * An earlier version of this file reimplemented the mapping, because the
 * production one was a private method. Review 3 was right to call that out: a
 * test that owns its own copy of the rule stays green when production stops
 * following it, which is the opposite of what a proof is for.
 *
 * `toSheetResult` is a top-level function in `PaymentLauncher.kt` and is the
 * same one [PaymentCoordinator.launch] calls. Nothing is duplicated here.
 * Moved from `:app` with the launcher (2026-09-14); the cases are unchanged,
 * and commerce's own last hop onto its handoff bus is pinned in
 * `:feature:commerce` by CheckoutPaymentOpenerTest.
 */
class PaymentOutcomeMappingTest {

    private val attempt = PaymentAttempt(applicationId = "mstore", referenceId = "order-1", id = "attempt-1")

    @Test
    fun `a reported success does not assert payment`() {
        val event = PaymentOutcome.Succeeded("pay_123").toSheetResult(attempt)
        assertTrue(
            "a client success must lead to a server poll, not a paid state",
            event is PaymentSheetResult.Closed,
        )
    }

    @Test
    fun `a reported failure still leads to a server poll`() {
        // THE important one. The payment may have been captured anyway.
        val event = PaymentOutcome.Failed(code = 2, message = "network").toSheetResult(attempt)
        assertTrue(
            "a client failure must NOT be treated as a failed payment; the capture " +
                "may have completed and only the callback was lost",
            event is PaymentSheetResult.Closed,
        )
    }

    @Test
    fun `a cancellation still leads to a server poll`() {
        assertTrue(PaymentOutcome.Cancelled.toSheetResult(attempt) is PaymentSheetResult.Closed)
    }

    @Test
    fun `a sheet that never opened is reported as unavailable`() {
        val event = PaymentOutcome.Unavailable("no session").toSheetResult(attempt)
        assertTrue(event is PaymentSheetResult.Unavailable)
        assertEquals("no session", (event as PaymentSheetResult.Unavailable).reason)
    }

    @Test
    fun `every outcome carries its attempt forward`() {
        // C3-LB-4: not just the order id. A retry of the same order produces a
        // second attempt, and an outcome that only named the order could be
        // applied to either.
        listOf(
            PaymentOutcome.Succeeded("p"),
            PaymentOutcome.Failed(1, "x"),
            PaymentOutcome.Cancelled,
            PaymentOutcome.Unavailable("r"),
        ).forEach {
            val event = it.toSheetResult(attempt)
            assertEquals(attempt, event.attempt)
            assertEquals("order-1", event.referenceId)
        }
    }
}
