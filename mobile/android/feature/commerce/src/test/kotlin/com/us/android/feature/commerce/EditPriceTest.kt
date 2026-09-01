package com.us.android.feature.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.Paise
import com.us.android.feature.commerce.seller.EditPriceUiState
import org.junit.Test

/**
 * Repricing a listing that is already selling.
 *
 * An empty field means "leave this alone", so most of what matters here is the
 * difference between an untouched field and a mistyped one: an untouched field
 * is omitted from the request entirely, and a mistyped one must block the save
 * rather than being silently skipped.
 */
class EditPriceTest {

    private fun content(
        currentPrice: Long = 129900,
        currentMrp: Long = 149900,
        price: String = "",
        mrp: String = "",
        paused: Boolean = false,
    ) = EditPriceUiState.Content(
        currentPrice = Paise(currentPrice),
        currentMrp = Paise(currentMrp),
        paused = paused,
        price = price,
        mrp = mrp,
    )

    @Test
    fun `an untouched form has nothing to save`() {
        // Both fields empty is not an error, it is a seller who opened the
        // screen and changed their mind. Saving would re-assert the same price
        // — a write nobody asked for.
        val s = content()
        assertThat(s.changed).isFalse()
        assertThat(s.canSave).isFalse()
    }

    @Test
    fun `changing only the price sends only the price`() {
        val s = content(price = "999")
        assertThat(s.newPrice).isEqualTo(Paise(99900))
        assertThat(s.newMrp).isNull()
        assertThat(s.canSave).isTrue()
    }

    @Test
    fun `changing only the struck-through price is allowed`() {
        val s = content(mrp = "199900")
        assertThat(s.newPrice).isNull()
        assertThat(s.newMrp).isEqualTo(Paise(19990000))
        assertThat(s.canSave).isTrue()
    }

    @Test
    fun `an exact price survives`() {
        // The reason paise are the authority on the way in: as a rupee double,
        // 1299.99 is 1299.9899999999998.
        assertThat(content(price = "1299.99").newPrice).isEqualTo(Paise(129999))
    }

    @Test
    fun `a mistyped price blocks the save rather than being skipped`() {
        // "12ab" is not an untouched field. Treating it as one would save a
        // form the seller believes they edited.
        val s = content(price = "12ab")
        assertThat(s.priceMalformed).isTrue()
        assertThat(s.canSave).isFalse()
    }

    @Test
    fun `a zero price blocks the save`() {
        // The server refuses it too — a listing priced at zero gives the stock
        // away — but the button should never have been enabled.
        val s = content(price = "0")
        assertThat(s.newPrice).isNull()
        assertThat(s.canSave).isFalse()
    }

    @Test
    fun `fractional paise block the save`() {
        assertThat(content(price = "1299.999").canSave).isFalse()
    }

    // ─── The two prices are checked against each other ─────────────────

    @Test
    fun `raising the price above an unchanged MRP is caught`() {
        // Only the price is being edited, but the comparison has to consider
        // the MRP already on the listing — otherwise a seller ends up with a
        // struck-through price below what they are charging.
        val s = content(currentMrp = 149900, price = "199900")
        assertThat(s.mrpBelowPrice).isTrue()
        assertThat(s.canSave).isFalse()
    }

    @Test
    fun `lowering the MRP below an unchanged price is caught`() {
        val s = content(currentPrice = 129900, mrp = "999")
        assertThat(s.mrpBelowPrice).isTrue()
        assertThat(s.canSave).isFalse()
    }

    @Test
    fun `changing both consistently is fine`() {
        val s = content(price = "999", mrp = "1499")
        assertThat(s.mrpBelowPrice).isFalse()
        assertThat(s.canSave).isTrue()
    }

    @Test
    fun `an equal MRP and price is allowed`() {
        // Not a discount, but not a lie either.
        val s = content(price = "999", mrp = "999")
        assertThat(s.mrpBelowPrice).isFalse()
        assertThat(s.canSave).isTrue()
    }

    @Test
    fun `nothing can be saved while a save is in flight`() {
        assertThat(content(price = "999").copy(saving = true).canSave).isFalse()
    }
}
