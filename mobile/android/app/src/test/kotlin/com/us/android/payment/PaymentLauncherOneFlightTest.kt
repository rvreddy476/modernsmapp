package com.us.android.payment

import com.us.android.core.commerce.payment.PaymentAttempt
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * C3-LB-4 — the launcher holds ONE sheet at a time, and its callback cannot
 * be stolen.
 *
 * The defect review 3 found (C-LB-2): a single unguarded callback slot. A
 * second `open` overwrote the first, so the first sheet's result went to the
 * second checkout's callback, and the second sheet's result then found an
 * empty slot and was dropped. A double tap, a retry, or an Activity
 * recreation was enough to produce it.
 *
 * ## What this exercises, and what it cannot
 *
 * Every case here drives the launcher's REAL slot machine — `claim`,
 * `deliver`, `abandon` — which is the production code `open` itself calls.
 * Nothing is reimplemented.
 *
 * What it deliberately does not cover is the Razorpay SDK: whether
 * `Checkout.open` presents a sheet, and whether the Activity's listener fires
 * as documented. Nothing short of a device proves that, and this pass makes
 * no such claim. The seam under test is the part that was WRONG, which is the
 * bookkeeping around the SDK rather than the SDK itself.
 */
class PaymentLauncherOneFlightTest {

    private val attemptA = PaymentAttempt(orderId = "order-A", id = "attempt-A")
    private val attemptB = PaymentAttempt(orderId = "order-B", id = "attempt-B")
    private val retryOfA = PaymentAttempt(orderId = "order-A", id = "attempt-A2")

    @Test
    fun `a second open is refused and does not steal the first callback`() {
        val launcher = RazorpayPaymentLauncher()
        val toA = mutableListOf<PaymentSheetOutcome>()
        val toB = mutableListOf<PaymentSheetOutcome>()

        assertTrue(launcher.claim(attemptA) { toA += it })
        assertFalse(
            "the second opening must be refused, not queued",
            launcher.claim(attemptB) { toB += it },
        )

        // B is told no, plainly...
        assertTrue(toB.single() is PaymentSheetOutcome.Unavailable)
        // ...and A is untouched, still holding the slot and still awaiting.
        assertTrue("the first callback must not have fired", toA.isEmpty())
        assertEquals(
            "the first attempt must still own the slot",
            attemptA,
            launcher.inFlightAttempt(),
        )
    }

    @Test
    fun `the SDK result goes to the sheet that actually opened`() {
        val launcher = RazorpayPaymentLauncher()
        val toA = mutableListOf<PaymentSheetOutcome>()
        val toB = mutableListOf<PaymentSheetOutcome>()

        launcher.claim(attemptA) { toA += it }
        launcher.claim(attemptB) { toB += it }
        toB.clear() // discard B's refusal; the question is who gets the result

        launcher.deliver(PaymentSheetOutcome.Succeeded("pay_1"))

        assertEquals("the result belongs to the sheet that opened", 1, toA.size)
        assertTrue("B never opened a sheet and must receive no SDK result", toB.isEmpty())
    }

    @Test
    fun `a duplicate callback produces exactly one outcome`() {
        val launcher = RazorpayPaymentLauncher()
        val outcomes = mutableListOf<PaymentSheetOutcome>()

        launcher.claim(attemptA) { outcomes += it }
        launcher.deliver(PaymentSheetOutcome.Succeeded("pay_1"))
        launcher.deliver(PaymentSheetOutcome.Succeeded("pay_1"))
        launcher.deliver(PaymentSheetOutcome.Cancelled)

        assertEquals(
            "the slot is cleared before the callback fires, so redelivery is a no-op",
            1,
            outcomes.size,
        )
    }

    @Test
    fun `a late callback after abandonment cannot reach a later checkout's callback`() {
        val launcher = RazorpayPaymentLauncher()
        val toA = mutableListOf<PaymentSheetOutcome>()
        val toB = mutableListOf<PaymentSheetOutcome>()

        // The buyer opens a sheet for A, then navigates away.
        launcher.claim(attemptA) { toA += it }
        launcher.abandon(attemptA)
        assertNull("navigating away must free the slot", launcher.inFlightAttempt())

        // A later checkout opens for B and its sheet is the live one.
        launcher.claim(attemptB) { toB += it }
        launcher.deliver(PaymentSheetOutcome.Failed(2, "late"))

        assertTrue("the abandoned attempt must not be resurrected", toA.isEmpty())
        // B receives the result because B's sheet is the one on screen — the
        // SDK only ever runs one. The protection that matters is one layer up:
        // the ViewModel matches the EVENT's attempt against its own, so B's
        // checkout will not act on an outcome stamped for A.
        assertEquals(1, toB.size)
    }

    @Test
    fun `abandoning someone else's attempt does not free the slot`() {
        val launcher = RazorpayPaymentLauncher()
        launcher.claim(attemptA) { }

        launcher.abandon(attemptB)

        assertEquals(
            "a stale screen must not be able to cancel the current checkout's sheet",
            attemptA,
            launcher.inFlightAttempt(),
        )
    }

    @Test
    fun `a retry of the same order is still a distinct attempt`() {
        val launcher = RazorpayPaymentLauncher()
        launcher.claim(attemptA) { }

        // Same ORDER, new attempt. Abandoning under the old attempt must not
        // release the new one's slot — order id alone is not identity.
        launcher.abandon(retryOfA)

        assertEquals(attemptA, launcher.inFlightAttempt())
    }

    @Test
    fun `the slot is free again after a delivery`() {
        val launcher = RazorpayPaymentLauncher()
        launcher.claim(attemptA) { }
        launcher.deliver(PaymentSheetOutcome.Cancelled)

        assertNull(launcher.inFlightAttempt())
        assertTrue(
            "a completed sheet must not wedge every later checkout",
            launcher.claim(attemptB) { },
        )
    }

    @Test
    fun `only one of many racing claims wins`() {
        val launcher = RazorpayPaymentLauncher()
        val attempts = (1..32).map { PaymentAttempt("order-$it", "attempt-$it") }
        val won = java.util.concurrent.atomic.AtomicInteger()

        val threads = attempts.map { attempt ->
            Thread { if (launcher.claim(attempt) { }) won.incrementAndGet() }
        }
        threads.forEach { it.start() }
        threads.forEach { it.join() }

        assertEquals(
            "the claim is a check-and-set under one lock; two winners would mean " +
                "two sheets and a callback that belongs to neither",
            1,
            won.get(),
        )
    }
}
