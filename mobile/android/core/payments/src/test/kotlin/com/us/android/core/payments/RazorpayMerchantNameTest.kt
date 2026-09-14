package com.us.android.core.payments

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * The merchant name on the payment sheet.
 *
 * Pinned because it is the one string on the sheet the buyer compares with
 * their bank statement, and it has drifted once already: the product was
 * renamed and the constant kept the old brand.
 *
 * The rule since 2026-09-14: the SERVER's per-application registry names the
 * merchant (`client_session.merchant_display_name`). The registered business
 * name, "Momentum Merchant", is the fallback for a server that sends none, or
 * sends something that is empty once cleaned. The client cleans defensively —
 * control characters out, trimmed, capped at 64 — even though the server
 * already trims and caps, because this string is rendered on a payment sheet.
 */
class RazorpayMerchantNameTest {

    private fun session(merchantDisplayName: String?) = PaymentSession(
        applicationId = "mstore",
        provider = "razorpay",
        providerOrderId = "order_x",
        keyId = "rzp_test_x",
        amountMinor = 204000,
        currency = "INR",
        description = "Order MS-1",
        merchantDisplayName = merchantDisplayName,
    )

    @Test
    fun `the fallback is the business registered with Razorpay`() {
        assertEquals("Momentum Merchant", RazorpayPaymentLauncher.DEFAULT_MERCHANT_NAME)
    }

    @Test
    fun `a server-supplied name is what the sheet shows`() {
        val s = session("Feast Kitchens")

        assertEquals("Feast Kitchens", RazorpayPaymentLauncher.merchantNameFor(s))
        assertEquals("Feast Kitchens", RazorpayPaymentLauncher.checkoutOptions(s)["name"])
    }

    @Test
    fun `the server name is trimmed`() {
        val padded = "  Feast Kitchens " + TAB

        assertEquals("Feast Kitchens", RazorpayPaymentLauncher.merchantNameFor(session(padded)))
    }

    @Test
    fun `an absent, empty or whitespace name falls back to the registered name`() {
        listOf(null, "", " ", "   ", "$TAB$LF ").forEach { name ->
            val s = session(name)
            assertEquals("'$name'", "Momentum Merchant", RazorpayPaymentLauncher.merchantNameFor(s))
            assertEquals("'$name'", "Momentum Merchant", RazorpayPaymentLauncher.checkoutOptions(s)["name"])
        }
    }

    @Test
    fun `an 80 character name is capped at 64`() {
        val eighty = "M".repeat(40) + "n".repeat(40)

        val shown = RazorpayPaymentLauncher.merchantNameFor(session(eighty))

        assertEquals(64, shown.length)
        assertEquals(eighty.take(64), shown)
        assertEquals(shown, RazorpayPaymentLauncher.checkoutOptions(session(eighty))["name"])
    }

    @Test
    fun `the cap never leaves half of a surrogate pair`() {
        // 63 chars, then an emoji (two UTF-16 units) straddling the 64 boundary.
        val name = "a".repeat(63) + String(Character.toChars(0x1F354))

        val shown = RazorpayPaymentLauncher.merchantNameFor(session(name))

        assertEquals("a".repeat(63), shown)
    }

    @Test
    fun `control characters are stripped`() {
        // NUL, ESC, DEL and the C1 NEL, inside and around the name.
        val dirty = "Feast" + NUL + " Kitchens" + ESC + DEL + NEL + CR + LF

        assertEquals("Feast Kitchens", RazorpayPaymentLauncher.merchantNameFor(session(dirty)))
        assertEquals("Feast Kitchens", RazorpayPaymentLauncher.checkoutOptions(session(dirty))["name"])
    }

    @Test
    fun `a name made only of control characters falls back`() {
        val onlyControls = "$NUL$ESC$DEL$NEL$CR$LF"

        assertEquals("Momentum Merchant", RazorpayPaymentLauncher.merchantNameFor(session(onlyControls)))
    }

    @Test
    fun `the rest of the sheet options are unchanged`() {
        val options = RazorpayPaymentLauncher.checkoutOptions(session("Feast Kitchens"))

        assertEquals("Order MS-1", options["description"])
        assertEquals("order_x", options["order_id"])
        assertEquals("INR", options["currency"])
        assertEquals(204000L, options["amount"])
        assertEquals(mapOf("enabled" to false), options["retry"])
        assertEquals(false, options["send_sms_hash"])
        assertEquals(
            "the key id is set on Checkout, never sent in the options",
            false,
            options.containsKey("key"),
        )
    }

    private companion object {
        // Built from code points so the source file itself holds no control bytes.
        val NUL = Char(0x00)
        val TAB = Char(0x09)
        val LF = Char(0x0A)
        val CR = Char(0x0D)
        val ESC = Char(0x1B)
        val DEL = Char(0x7F)
        val NEL = Char(0x85)
    }
}
