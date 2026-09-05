package com.us.android.feature.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.model.TaxClass
import com.us.android.feature.commerce.seller.NewProductForm
import com.us.android.feature.commerce.seller.asRupeeText
import com.us.android.feature.commerce.seller.parseRupees
import org.junit.Test

/**
 * The one place a human types the price every sale is charged at.
 *
 * The text is parsed STRAIGHT to integer paise. Not `toDouble() * 100` — that
 * is the conversion the whole minor-unit migration exists to remove, and doing
 * it here would put the rounding error at the source, where every exact figure
 * downstream then faithfully preserves it.
 */
class NewProductPriceTest {

    // ─── Parsing ───────────────────────────────────────────────────────

    @Test
    fun `whole rupees become paise`() {
        assertThat(parseRupees("1299")).isEqualTo(Paise(129900))
        assertThat(parseRupees("1")).isEqualTo(Paise(100))
    }

    @Test
    fun `the case a float cannot hold`() {
        // 1299.99 as a rupee Double is 1299.9899999999998. Parsed as text it
        // is exactly 129999 paise, every time, on every device.
        assertThat(parseRupees("1299.99")).isEqualTo(Paise(129999))
    }

    @Test
    fun `a single decimal place is tenths, not hundredths`() {
        // "1299.5" is one thousand two hundred and ninety-nine rupees fifty
        // paise. Reading the fraction as-is would make it 5 paise.
        assertThat(parseRupees("1299.5")).isEqualTo(Paise(129950))
        assertThat(parseRupees("0.5")).isEqualTo(Paise(50))
    }

    @Test
    fun `paise-only amounts parse`() {
        assertThat(parseRupees("0.99")).isEqualTo(Paise(99))
        assertThat(parseRupees(".99")).isEqualTo(Paise(99))
    }

    @Test
    fun `more than two decimal places is refused, never rounded`() {
        // A seller cannot charge 1.239, and silently rounding it into a price
        // they did not type is worse than asking again.
        assertThat(parseRupees("1.239")).isNull()
        assertThat(parseRupees("1299.999")).isNull()
    }

    @Test
    fun `zero and negative are not prices`() {
        assertThat(parseRupees("0")).isNull()
        assertThat(parseRupees("0.00")).isNull()
        assertThat(parseRupees("-5")).isNull()
    }

    @Test
    fun `nonsense is refused`() {
        for (raw in listOf("", "   ", "abc", "12.3.4", "1,299", "₹1299", "1e3")) {
            assertThat(parseRupees(raw)).isNull()
        }
    }

    @Test
    fun `whitespace around a valid amount is tolerated`() {
        assertThat(parseRupees("  1299.99  ")).isEqualTo(Paise(129999))
    }

    @Test
    fun `parsing and rendering round-trip`() {
        for (text in listOf("1299.99", "1299.00", "0.01", "99999.50")) {
            assertThat(parseRupees(text)!!.asRupeeText()).isEqualTo(
                if (text.contains('.')) text else "$text.00",
            )
        }
    }

    // ─── The form ──────────────────────────────────────────────────────

    private fun form(
        title: String = "A thing",
        selling: String = "1299",
        mrp: String = "",
        stock: String = "5",
        taxClassId: String? = "tax-1",
    ) = NewProductForm(
        title = title,
        sellingPrice = selling,
        mrp = mrp,
        openingStock = stock,
        taxClassId = taxClassId,
        taxClasses = listOf(TaxClass("tax-1", "GST 18%", 18.0)),
        loadingRates = false,
    )

    @Test
    fun `a complete form can be submitted`() {
        assertThat(form().isComplete).isTrue()
    }

    @Test
    fun `a product cannot be listed without a GST rate`() {
        // Not a UI nicety. A product with no tax class is unsellable —
        // checkout resolves the rate under a row lock and refuses with
        // PRODUCT_TAX_UNCONFIGURED — so the listing would go live and fail at
        // the last step of a purchase with an error the seller never sees.
        assertThat(form(taxClassId = null).isComplete).isFalse()
    }

    @Test
    fun `a product cannot be listed without a price`() {
        assertThat(form(selling = "").isComplete).isFalse()
        assertThat(form(selling = "0").isComplete).isFalse()
        assertThat(form(selling = "abc").isComplete).isFalse()
    }

    @Test
    fun `zero opening stock is allowed`() {
        // A seller listing something they are about to receive is a normal
        // thing to do, and the stock screen exists to raise it later.
        assertThat(form(stock = "0").isComplete).isTrue()
    }

    @Test
    fun `missing stock is not the same as zero`() {
        assertThat(form(stock = "").isComplete).isFalse()
    }

    @Test
    fun `a struck-through price below the selling price is caught`() {
        // Almost always the two typed the wrong way round. Left unchecked the
        // buyer sees a negative discount.
        val f = form(selling = "1299", mrp = "999")
        assertThat(f.mrpBelowSelling).isTrue()
        assertThat(f.isComplete).isFalse()
    }

    @Test
    fun `an equal struck-through price is fine`() {
        // Not a discount, but not a lie either — PriceRow simply stops
        // striking it through.
        val f = form(selling = "1299", mrp = "1299")
        assertThat(f.mrpBelowSelling).isFalse()
        assertThat(f.isComplete).isTrue()
    }

    @Test
    fun `no struck-through price at all is fine`() {
        val f = form(mrp = "")
        assertThat(f.mrpBelowSelling).isFalse()
        assertThat(f.isComplete).isTrue()
    }

    @Test
    fun `a title needs to be more than a character or two`() {
        assertThat(form(title = "").isComplete).isFalse()
        assertThat(form(title = "ab").isComplete).isFalse()
        assertThat(form(title = "Tea").isComplete).isTrue()
    }
}
