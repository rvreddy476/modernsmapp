package com.us.android.feature.commerce

import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.repository.ChangedLine
import com.us.android.core.commerce.repository.CommerceError
import com.us.android.core.commerce.repository.UnavailableLine
import com.us.android.feature.commerce.ui.describe
import com.us.android.feature.commerce.ui.isRetryable
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Copy rules the money boundary depends on.
 *
 * The server went to real trouble to stop returning `INTERNAL_ERROR` for
 * everything, and that work is wasted if the client collapses the distinct
 * codes back into one banner. These assert the two properties that actually
 * matter to a customer's money.
 */
class CommerceErrorCopyTest {

    private val allErrors: List<CommerceError> = listOf(
        CommerceError.OutOfStock(
            listOf(UnavailableLine("v1", "p1", "A thing", requested = 2, available = 1)),
        ),
        CommerceError.PriceChanged(
            lines = listOf(ChangedLine("v1", Paise(10000), Paise(12000))),
            newTotal = Paise(12000),
        ),
        CommerceError.QuoteStale,
        CommerceError.QuoteExpired,
        CommerceError.NotServiceable("No courier serves 194101"),
        CommerceError.NotServiceable(null),
        CommerceError.IdempotencyConflict,
        CommerceError.ProductUnavailable,
        CommerceError.MultipleSellers,
        CommerceError.CouponUnavailable,
        CommerceError.CartEmpty,
        CommerceError.CodNotSupported,
        CommerceError.OrderNotFound,
        CommerceError.CancelNotPermitted,
        CommerceError.TryAgain,
        CommerceError.Network(RuntimeException("boom")),
        CommerceError.Unexpected("WEIRD_CODE", "internal detail"),
    )

    @Test
    fun `every error has non-empty customer copy`() {
        allErrors.forEach { error ->
            val copy = error.describe()
            assertTrue("$error produced blank copy", copy.isNotBlank())
        }
    }

    /**
     * A raw server code is not customer copy. It is unactionable noise, and
     * for `Unexpected` in particular the code exists for the log.
     */
    @Test
    fun `unexpected errors do not leak the server code to the customer`() {
        val copy = CommerceError.Unexpected("WEIRD_CODE", "internal detail").describe()
        assertFalse(copy.contains("WEIRD_CODE"))
        assertFalse(copy.contains("internal detail"))
    }

    /**
     * Retry means "sending this same request again is sensible".
     *
     * Most commerce failures need the customer to change something first —
     * re-quote, edit the cart, acknowledge a price — and offering a bare
     * Retry for those just reproduces the failure.
     */
    @Test
    fun `only transient failures are retryable`() {
        assertTrue(CommerceError.Network(null).isRetryable())
        assertTrue(CommerceError.TryAgain.isRetryable())

        listOf(
            CommerceError.QuoteStale,
            CommerceError.QuoteExpired,
            CommerceError.CartEmpty,
            CommerceError.IdempotencyConflict,
            CommerceError.CodNotSupported,
            CommerceError.CancelNotPermitted,
            CommerceError.PriceChanged(emptyList(), null),
        ).forEach { error ->
            assertFalse(
                "$error must not offer a bare retry; the customer has to change something first",
                error.isRetryable(),
            )
        }
    }

    /** A serviceability reason from the server is preferred over generic copy. */
    @Test
    fun `serviceability reason is surfaced when the server gives one`() {
        val withReason = CommerceError.NotServiceable("No courier serves 194101").describe()
        assertTrue(withReason.contains("194101"))

        val withoutReason = CommerceError.NotServiceable(null).describe()
        assertTrue(withoutReason.isNotBlank())
    }

    /**
     * The out-of-stock copy names the item when there is exactly one, because
     * "something in your cart is unavailable" is not actionable.
     */
    @Test
    fun `single out of stock line names the product`() {
        val copy = CommerceError.OutOfStock(
            listOf(UnavailableLine("v1", "p1", "Blue kettle", requested = 2, available = 0)),
        ).describe()
        assertTrue(copy.contains("Blue kettle"))
    }
}
