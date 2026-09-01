package com.us.android.core.commerce

import com.us.android.core.commerce.model.Paise
import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The money type's contract.
 *
 * These are not formatting niceties. LB-19 exists because money crossed a
 * service boundary as a float and lost paise on the way; this type is what
 * stops the client reintroducing that, and these tests are what stop the
 * type quietly acquiring a lossy path.
 */
class PaiseTest {

    @Test
    fun `arithmetic is exact`() {
        assertEquals(Paise(118000), Paise(100000) + Paise(18000))
        assertEquals(Paise(82000), Paise(100000) - Paise(18000))
        assertEquals(Paise(354000), Paise(118000) * 3)
    }

    @Test
    fun `quantity scaling never loses a paise`() {
        // ₹333.33 × 7. Through a Double this gives 2333.3100000000004.
        val unit = Paise(33333)
        assertEquals(Paise(233331), unit * 7)
    }

    @Test
    fun `formats with Indian digit grouping`() {
        // 2,2,3 from the right — ₹12,34,567.89, not ₹1,234,567.89.
        assertEquals("12,34,567.89", Paise(123456789).format())
        assertEquals("1,234.56", Paise(123456).format())
        assertEquals("999.99", Paise(99999).format())
        assertEquals("1,000.00", Paise(100000).format())
        assertEquals("0.01", Paise(1).format())
        assertEquals("0.00", Paise.ZERO.format())
    }

    @Test
    fun `formats a negative amount`() {
        assertEquals("-1,234.56", Paise(-123456).format())
    }

    @Test
    fun `formats with the rupee symbol`() {
        assertEquals("₹1,180.00", Paise(118000).formatWithSymbol())
    }

    @Test
    fun `ofRupees is for constants only and is exact`() {
        assertEquals(Paise(49900), Paise.ofRupees(499))
    }

    // ─── Wire contract ───────────────────────────────────────────────

    @Test
    fun `serialises as a bare integer`() {
        assertEquals("118000", Json.encodeToString(Paise.serializer(), Paise(118000)))
    }

    @Test
    fun `deserialises an integer`() {
        assertEquals(Paise(118000), Json.decodeFromString(Paise.serializer(), "118000"))
    }

    /**
     * The important one. A fractional value in a paise field means the sender
     * is still thinking in rupees. Truncating it silently is precisely how
     * the original cross-service defect propagated, so it must fail loudly.
     */
    @Test
    fun `refuses a fractional wire value`() {
        assertThrows(Exception::class.java) {
            Json.decodeFromString(Paise.serializer(), "1180.50")
        }
    }

    @Test
    fun `comparison works for sorting and thresholds`() {
        assertTrue(Paise(100) > Paise(99))
        assertTrue(Paise(-1) < Paise.ZERO)
        assertEquals(
            listOf(Paise(1), Paise(50), Paise(100)),
            listOf(Paise(100), Paise(1), Paise(50)).sorted(),
        )
    }

    @Test
    fun `zero and positive predicates`() {
        assertTrue(Paise.ZERO.isZero)
        assertTrue(Paise(1).isPositive)
        assertTrue(!Paise(-1).isPositive)
    }

    /**
     * A round trip through the wire format must be lossless for every
     * amount, including the awkward ones a float mangles.
     */
    @Test
    fun `round trip is lossless across awkward amounts`() {
        val amounts = listOf(
            1L, 7L, 99L, 100L, 101L, 999L, 100_00L,
            10_99L, // ₹10.99 — 10.989999… as a Double
            118_000L, // ₹1,180 at 18% inclusive
            33_333L,
            999_999_999L, // a large order
        )
        for (a in amounts) {
            val encoded = Json.encodeToString(Paise.serializer(), Paise(a))
            val decoded = Json.decodeFromString(Paise.serializer(), encoded)
            assertEquals("round trip lost precision for $a", Paise(a), decoded)
        }
    }
}
