package com.us.android.core.payments

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Audit L8: what a payment outcome may say in logcat. Its kind, and a
 * failure's numeric code; never the SDK's raw response, which carries payment
 * and order ids and an error description.
 */
class PaymentOutcomeLogTest {

    @Test
    fun `a logged outcome carries its kind and code and nothing else`() {
        val raw = """{"error":{"code":"BAD_REQUEST_ERROR","description":"Payment failed",""" +
            """"metadata":{"payment_id":"pay_29QQoUBi66xm2f","order_id":"order_9A33XWu170gUtm"}}}"""

        val failed = PaymentOutcome.Failed(code = 2, message = raw).logSummary()
        assertThat(failed).isEqualTo("Failed(code=2)")
        assertThat(failed).doesNotContain("pay_")
        assertThat(failed).doesNotContain("order_")

        assertThat(PaymentOutcome.Succeeded(providerPaymentId = "pay_29QQoUBi66xm2f").logSummary())
            .isEqualTo("Succeeded")
        assertThat(PaymentOutcome.Failed(code = null, message = null).logSummary()).isEqualTo("Failed(code=none)")
        assertThat(PaymentOutcome.Cancelled.logSummary()).isEqualTo("Cancelled")
        assertThat(PaymentOutcome.Unavailable("A payment is already in progress.").logSummary())
            .isEqualTo("Unavailable")
    }
}
