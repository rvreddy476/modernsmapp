package com.us.android.payment

import com.us.android.core.commerce.payment.PaymentAttempt
import com.us.android.core.commerce.payment.PaymentHandoffEvent
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The single most dangerous decision in the payment handoff: what a client
 * callback is allowed to mean.
 *
 * A1/R-3 — an order becomes paid only when a signature-verified provider
 * webhook reaches the server. So the SDK's opinion is evidence about where to
 * look, never a fact about money. This pins the two rules that follow:
 *
 *  1. Every ENDING of the sheet — success, failure, cancellation — maps to
 *     [PaymentHandoffEvent.SheetClosed], which makes the app poll the server.
 *     Mapping a reported failure to a failure state would be the dangerous
 *     one: a dropped callback or a killed process can sit on top of a capture
 *     that completed, and telling someone their payment failed while their
 *     money is gone is the worst outcome this flow can produce.
 *
 *  2. Only [PaymentSheetOutcome.Unavailable] — where no sheet was ever shown —
 *     maps to an unavailable event, because then no payment CAN have been
 *     taken and making the buyer wait through a poll would be dishonest in
 *     the other direction.
 *
 * ## C3-LB-4: this calls the production mapper
 *
 * The previous version of this file reimplemented the mapping, because the
 * production one was a private method on [CheckoutPaymentCoordinator]. Review
 * 3 was right to call that out: a test that owns its own copy of the rule
 * stays green when production stops following it, which is the opposite of
 * what a proof is for.
 *
 * `toHandoffEvent` is now a top-level function in `PaymentLauncher.kt` and is
 * the same one the coordinator calls. Nothing is duplicated here.
 */
class PaymentOutcomeMappingTest {

    private val attempt = PaymentAttempt(orderId = "order-1", id = "attempt-1")

    @Test
    fun `a reported success does not assert payment`() {
        val event = PaymentSheetOutcome.Succeeded("pay_123").toHandoffEvent(attempt)
        assertTrue(
            "a client success must lead to a server poll, not a paid state",
            event is PaymentHandoffEvent.SheetClosed,
        )
    }

    @Test
    fun `a reported failure still leads to a server poll`() {
        // THE important one. The payment may have been captured anyway.
        val event = PaymentSheetOutcome.Failed(code = 2, message = "network").toHandoffEvent(attempt)
        assertTrue(
            "a client failure must NOT be treated as a failed payment; the capture " +
                "may have completed and only the callback was lost",
            event is PaymentHandoffEvent.SheetClosed,
        )
    }

    @Test
    fun `a cancellation still leads to a server poll`() {
        assertTrue(PaymentSheetOutcome.Cancelled.toHandoffEvent(attempt) is PaymentHandoffEvent.SheetClosed)
    }

    @Test
    fun `a sheet that never opened is reported as unavailable`() {
        val event = PaymentSheetOutcome.Unavailable("no session").toHandoffEvent(attempt)
        assertTrue(event is PaymentHandoffEvent.Unavailable)
        assertEquals("no session", (event as PaymentHandoffEvent.Unavailable).reason)
    }

    @Test
    fun `every outcome carries its attempt forward`() {
        // C3-LB-4: not just the order id. A retry of the same order produces a
        // second attempt, and an outcome that only named the order could be
        // applied to either.
        listOf(
            PaymentSheetOutcome.Succeeded("p"),
            PaymentSheetOutcome.Failed(1, "x"),
            PaymentSheetOutcome.Cancelled,
            PaymentSheetOutcome.Unavailable("r"),
        ).forEach {
            val event = it.toHandoffEvent(attempt)
            assertEquals(attempt, event.attempt)
            assertEquals("order-1", event.orderId)
        }
    }
}
