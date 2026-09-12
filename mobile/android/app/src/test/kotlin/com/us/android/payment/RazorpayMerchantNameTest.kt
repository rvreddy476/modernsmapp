package com.us.android.payment

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * The merchant name on the payment sheet is the registered business name.
 *
 * Pinned because it is the one string on the sheet the buyer compares with
 * their bank statement, and it has drifted once already: the product was
 * renamed and the constant kept the old brand. A test is the only thing that
 * makes the next rename touch this file.
 */
class RazorpayMerchantNameTest {

    @Test
    fun `the sheet names the business registered with Razorpay`() {
        assertEquals("Momentum Merchant", RazorpayPaymentLauncher.MERCHANT_NAME)
    }
}
