package com.us.android.feature.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.model.Variant
import com.us.android.feature.commerce.tryon.TryOnBagTarget
import com.us.android.feature.commerce.tryon.blockedReason
import com.us.android.feature.commerce.tryon.tryOnBagTarget
import org.junit.Test

/**
 * The join between "the shade on your face" and "the thing in your bag".
 *
 * A try-on that cannot buy is a toy, so this screen sells — and the moment it
 * does, this join becomes the thing that must not be wrong. The server
 * contract says a try-on variant id IS a catalogue variant id, but a contract
 * is a claim about the server: a seller who configured shades that match no
 * variant, or a shade that sold out while the camera was open, must produce a
 * disabled button and one sentence, never a cart row for something that cannot
 * ship.
 */
class TryOnBagTest {

    private fun variant(
        id: String,
        price: Long = 49900,
        inStock: Boolean = true,
        qty: Int = 5,
    ) = Variant(
        id = id,
        sku = "SKU-$id",
        options = emptyList(),
        mrp = Paise(price),
        sellingPrice = Paise(price),
        inStock = inStock,
        availableQty = qty,
    )

    @Test
    fun `a shade that is a stocked variant is purchasable, at that variant's price`() {
        val target = tryOnBagTarget("v-42", listOf(variant("v-1"), variant("v-42", price = 129900)))

        assertThat(target).isEqualTo(TryOnBagTarget.Purchasable("v-42", Paise(129900)))
        assertThat(target.blockedReason()).isNull()
    }

    @Test
    fun `the price comes from the matched variant, never from the first one`() {
        // A price from one variant beside a bag target from another is exactly
        // the bug that charges someone for the wrong shade.
        val target = tryOnBagTarget(
            "v-2",
            listOf(variant("v-1", price = 10000), variant("v-2", price = 99900)),
        )

        assertThat((target as TryOnBagTarget.Purchasable).price).isEqualTo(Paise(99900))
    }

    @Test
    fun `no shade chosen is not a failure, it is nothing to add`() {
        listOf(null, "", "   ").forEach { nothing ->
            val target = tryOnBagTarget(nothing, listOf(variant("v-1")))

            assertThat(target).isEqualTo(TryOnBagTarget.NoShade)
            assertThat(target.blockedReason()).isEqualTo("Choose a shade to add it to your bag.")
        }
    }

    @Test
    fun `a shade that matches no catalogue variant cannot be bought`() {
        val target = tryOnBagTarget("shade-only", listOf(variant("v-1")))

        assertThat(target).isEqualTo(TryOnBagTarget.NotPurchasable)
        assertThat(target.blockedReason()).isEqualTo("This shade is not on sale yet.")
    }

    @Test
    fun `a product with no variants at all is not purchasable rather than crashing`() {
        assertThat(tryOnBagTarget("v-1", emptyList())).isEqualTo(TryOnBagTarget.NotPurchasable)
    }

    @Test
    fun `out of stock is still worth trying on and is not worth a cart row`() {
        val flagged = tryOnBagTarget("v-1", listOf(variant("v-1", inStock = false)))
        // Both conditions are checked: a server may say in_stock with a zero
        // quantity during a race with another buyer's checkout, and the
        // quantity is what decides whether the add would actually succeed.
        val exhausted = tryOnBagTarget("v-1", listOf(variant("v-1", qty = 0)))

        assertThat(flagged).isEqualTo(TryOnBagTarget.OutOfStock)
        assertThat(exhausted).isEqualTo(TryOnBagTarget.OutOfStock)
        assertThat(flagged.blockedReason()).isEqualTo("This shade is out of stock.")
    }

    @Test
    fun `ids are matched exactly, because an id is an opaque server key`() {
        // No trimming, no case-folding, no "close enough": a fuzzy match here
        // puts the wrong lipstick in someone's cart.
        listOf(" v-1", "v-1 ", "V-1", "v-1\n").forEach { near ->
            assertThat(tryOnBagTarget(near, listOf(variant("v-1"))))
                .isEqualTo(TryOnBagTarget.NotPurchasable)
        }
    }

    @Test
    fun `no blocked reason names a variant id, a descriptor or a seller`() {
        val reasons = listOf(
            TryOnBagTarget.NoShade,
            TryOnBagTarget.NotPurchasable,
            TryOnBagTarget.OutOfStock,
        ).mapNotNull { it.blockedReason() }

        assertThat(reasons).hasSize(3)
        reasons.forEach { reason ->
            assertThat(reason).doesNotContain("variant")
            assertThat(reason).doesNotContain("descriptor")
            assertThat(reason).doesNotContain("seller")
        }
    }
}
