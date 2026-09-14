package com.us.android.core.payments

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The Activity binding forwards the SDK's result and interprets nothing.
 *
 * These two overrides lived on `:app`'s MainActivity before `:core:payments`;
 * they are the same two lines, now written once for every host.
 */
class ActivityPaymentHostTest {

    private class RecordingSink : PaymentResultSink {
        val delivered = mutableListOf<PaymentOutcome>()
        override fun deliver(outcome: PaymentOutcome) {
            delivered += outcome
        }
    }

    private class Host(override val paymentResultSink: PaymentResultSink) : ActivityPaymentHost

    @Test
    fun `the SDK's success is forwarded as evidence`() {
        val sink = RecordingSink()
        Host(sink).onPaymentSuccess("pay_1", null)
        assertEquals(listOf(PaymentOutcome.Succeeded("pay_1")), sink.delivered)
    }

    @Test
    fun `the SDK's error is forwarded as reported`() {
        val sink = RecordingSink()
        Host(sink).onPaymentError(0, "Payment cancelled by user", null)
        assertEquals(listOf(PaymentOutcome.Failed(0, "Payment cancelled by user")), sink.delivered)
    }

    @Test
    fun `the host reaches the launcher's in-flight sheet`() {
        val launcher = RazorpayPaymentLauncher()
        val outcomes = mutableListOf<PaymentOutcome>()
        launcher.claim(PaymentAttempt("mstore", "order-1", "attempt-1")) { outcomes += it }

        Host(launcher).onPaymentSuccess("pay_1", null)

        assertEquals(listOf(PaymentOutcome.Succeeded("pay_1")), outcomes)
        assertTrue("the slot is free once the result is delivered", launcher.inFlightAttempt() == null)
    }
}
