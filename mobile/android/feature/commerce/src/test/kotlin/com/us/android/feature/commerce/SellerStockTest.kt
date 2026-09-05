package com.us.android.feature.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.StockLevel
import com.us.android.core.commerce.model.StockReason
import com.us.android.feature.commerce.seller.StockUiState
import org.junit.Test

/**
 * The stock form's arithmetic and its refusals.
 *
 * These are pure state assertions rather than UI tests because everything that
 * can go wrong here is a number: whether a delta is signed correctly, and
 * whether a removal that would eat into reserved units is allowed to leave the
 * device.
 *
 * The screen asks how many units were ADDED or REMOVED, never what the new
 * total is. A new-total field is a lost-update generator — the screen renders
 * 42, two units sell while the seller types, they submit 52 meaning "I added
 * ten", and the two sold units go back on the shelf.
 */
class SellerStockTest {

    private fun content(
        total: Int = 10,
        reserved: Int = 0,
        amount: String = "",
        removing: Boolean = false,
    ) = StockUiState.Content(
        level = StockLevel("v1", total = total, reserved = reserved, available = total - reserved),
        amount = amount,
        removing = removing,
    )

    @Test
    fun `an addition is a positive delta`() {
        assertThat(content(amount = "10").delta).isEqualTo(10)
    }

    @Test
    fun `a removal is a negative delta`() {
        assertThat(content(amount = "10", removing = true).delta).isEqualTo(-10)
    }

    @Test
    fun `the delta is the units typed, never the resulting total`() {
        // 42 in stock, the seller adds 10. The server must be sent 10, not 52.
        // Sending 52 would restore anything that sold while they were typing.
        val state = content(total = 42, amount = "10")
        assertThat(state.delta).isEqualTo(10)
        assertThat(state.delta).isNotEqualTo(state.level.total + 10)
    }

    @Test
    fun `an empty or zero amount cannot be submitted`() {
        assertThat(content(amount = "").canSubmit).isFalse()
        assertThat(content(amount = "0").canSubmit).isFalse()
        assertThat(content(amount = "0").delta).isNull()
    }

    @Test
    fun `a removal below the reserved line is refused before the round trip`() {
        // 10 in stock, 6 promised to orders mid-checkout. Removing 7 would
        // leave 3 and make those orders unfulfillable.
        val state = content(total = 10, reserved = 6, amount = "7", removing = true)
        assertThat(state.wouldBreachReserved).isTrue()
        assertThat(state.canSubmit).isFalse()
    }

    @Test
    fun `writing down exactly to the reserved line is allowed`() {
        // Those units exist. Refusing this would stop a seller recording real
        // damage just because the remainder is spoken for.
        val state = content(total = 10, reserved = 6, amount = "4", removing = true)
        assertThat(state.wouldBreachReserved).isFalse()
        assertThat(state.canSubmit).isTrue()
    }

    @Test
    fun `adding stock is never blocked by the reserved line`() {
        val state = content(total = 10, reserved = 10, amount = "5")
        assertThat(state.wouldBreachReserved).isFalse()
        assertThat(state.canSubmit).isTrue()
    }

    @Test
    fun `nothing can be submitted while a save is in flight`() {
        assertThat(content(amount = "5").copy(saving = true).canSubmit).isFalse()
    }

    @Test
    fun `a reason is always carried`() {
        // There is no "unspecified". A stock movement with no stated cause is
        // unauditable, and the server keeps its own allow-list regardless.
        assertThat(content().reason).isEqualTo(StockReason.PURCHASE)
        assertThat(StockReason.entries.map { it.wire })
            .containsExactly("purchase", "damage", "theft", "recount", "correction")
    }

    @Test
    fun `every reason code matches the server allow-list exactly`() {
        // The Kotlin enum cannot import the Go one, so the wire strings are
        // asserted here. A rename on either side that is not made on both is a
        // 400 for the seller, which is exactly how net_banking came to be
        // offered by one side and refused by the other.
        for (reason in StockReason.entries) {
            assertThat(reason.wire).isEqualTo(reason.wire.lowercase())
            assertThat(reason.wire).doesNotContain(" ")
            assertThat(reason.label).isNotEmpty()
        }
    }
}
