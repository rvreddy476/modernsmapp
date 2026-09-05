package com.us.android.core.commerce

import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.model.discountPercent
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * The ONE piece of price arithmetic the client is allowed to do.
 *
 * MStore's cards show "20% off" next to a struck-through MRP. The server
 * publishes `discount_pct` and the repository prefers it; this derivation is
 * the fallback for a server that does not yet, and it exists under test
 * because a wrong percentage next to a right price is a support ticket.
 *
 * Two rules it must never break:
 *
 *  1. It FLOORS. Advertising a 24.9% saving as 25% is a claim the price does
 *     not support.
 *  2. It refuses to invent one. An MRP at or below the price is not a
 *     discount, and a zero MRP is a missing value — rendering it as "100% off"
 *     is precisely the defect a zero-MRP listing used to produce.
 */
class DiscountTest {

    @Test
    fun `a genuine saving is floored, never rounded up`() {
        // 1499 -> 1180 is 21.28%. Not 21.3, and certainly not 22.
        assertEquals(21, discountPercent(Paise(118_000), Paise(149_900)))

        // Exactly a quarter off stays a quarter.
        assertEquals(25, discountPercent(Paise(75_00), Paise(100_00)))

        // 24.9% must not present itself as 25%.
        assertEquals(24, discountPercent(Paise(751_0), Paise(100_00)))
    }

    @Test
    fun `no MRP above the price is no discount`() {
        // The common case: a seller who ran no discount, where the repository
        // sends the selling price as the MRP.
        assertNull(discountPercent(Paise(100_00), Paise(100_00)))

        // Typed the wrong way round. A negative saving is not shown at all.
        assertNull(discountPercent(Paise(100_00), Paise(75_00)))
    }

    @Test
    fun `a zero MRP is a missing value, not a hundred percent off`() {
        assertNull(discountPercent(Paise(100_00), Paise.ZERO))
        assertNull(discountPercent(Paise.ZERO, Paise.ZERO))
    }

    @Test
    fun `a saving too small to round to one percent is not shown`() {
        // 1 paise off ten thousand rupees floors to 0. A "0% off" badge is
        // worse than none: it advertises a discount that does not exist.
        assertNull(discountPercent(Paise(999_999), Paise(1_000_000)))
    }
}
