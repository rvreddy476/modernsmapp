package com.us.android.core.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.model.SellerProduct
import com.us.android.core.commerce.model.Variant
import com.us.android.core.commerce.model.VariantOption
import com.us.android.core.commerce.model.chooserLabel
import com.us.android.core.commerce.model.variantsForEdit
import org.junit.Test

/**
 * Which variants a stock or price edit may address, as a table.
 *
 * Protects the 2026-09-30 rule that the PRODUCT id never stands in for a
 * variant id: the row's own variants first, then what the server sent for
 * the product, then the row's `default_variant_id` shorthand, and otherwise
 * nothing — an empty list the screen says out loud rather than a guess.
 */
class VariantsForEditTest {

    private fun product(
        variants: List<Variant> = emptyList(),
        defaultVariantId: String? = null,
    ) = SellerProduct(
        id = "p-1",
        title = "Kettle",
        status = "active",
        approvalStatus = "approved",
        rejectionReason = null,
        imageUrl = null,
        variants = variants,
        defaultVariantId = defaultVariantId,
    )

    private fun variant(id: String, vararg options: Pair<String, String>, sku: String = "") = Variant(
        id = id,
        sku = sku,
        options = options.map { (n, v) -> VariantOption(n, v) },
        mrp = Paise.ZERO,
        sellingPrice = Paise.ZERO,
        inStock = true,
        availableQty = 1,
    )

    @Test
    fun `the row's own variants win`() {
        val own = listOf(variant("v-1"))
        assertThat(variantsForEdit(product(variants = own), fetched = listOf(variant("v-9")))).isEqualTo(own)
    }

    @Test
    fun `a bare row uses what the server sent for the product`() {
        val fetched = listOf(variant("v-1"), variant("v-2"))
        assertThat(variantsForEdit(product(), fetched)).isEqualTo(fetched)
    }

    @Test
    fun `a bare row with only the default_variant_id shorthand is that one variant`() {
        val variants = variantsForEdit(product(defaultVariantId = "v-7"), fetched = emptyList())
        assertThat(variants.map { it.id }).containsExactly("v-7")
    }

    @Test
    fun `nothing anywhere is an empty list, never the product id`() {
        val variants = variantsForEdit(product(), fetched = emptyList())
        assertThat(variants).isEmpty()
        assertThat(variants.map { it.id }).doesNotContain("p-1")
    }

    @Test
    fun `no answer ever carries the product id`() {
        val cases = listOf(
            variantsForEdit(product(variants = listOf(variant("v-1"))), listOf(variant("v-2"))),
            variantsForEdit(product(), listOf(variant("v-2"))),
            variantsForEdit(product(defaultVariantId = "v-3"), emptyList()),
            variantsForEdit(product(), emptyList()),
        )
        for (answer in cases) {
            assertThat(answer.map { it.id }).doesNotContain("p-1")
        }
    }

    @Test
    fun `a chooser names a variant by its options, then its SKU, then its position`() {
        assertThat(variant("v-1", "Size" to "M", "Colour" to "Blue").chooserLabel(0)).isEqualTo("M · Blue")
        assertThat(variant("v-2", sku = "SKU-2").chooserLabel(1)).isEqualTo("SKU-2")
        assertThat(variant("v-3").chooserLabel(2)).isEqualTo("Variant 3")
    }
}
