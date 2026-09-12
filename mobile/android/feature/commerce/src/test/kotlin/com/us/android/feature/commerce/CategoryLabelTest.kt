package com.us.android.feature.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.Category
import com.us.android.feature.commerce.home.categoryCountLabel
import org.junit.Test

/**
 * The line under a category tile.
 *
 * The count exists so the strip can dim a category that opens onto nothing.
 * The label is a pure function so "hidden at zero, singular at one" is a
 * test rather than something only a screenshot can catch.
 */
class CategoryLabelTest {

    @Test
    fun `a count reads as products, singular at one`() {
        assertThat(categoryCountLabel(12)).isEqualTo("12 products")
        assertThat(categoryCountLabel(1)).isEqualTo("1 product")
        assertThat(categoryCountLabel(2)).isEqualTo("2 products")
    }

    /** The dimmed tile already says it; a "0 products" line would say it twice. */
    @Test
    fun `zero draws no label`() {
        assertThat(categoryCountLabel(0)).isNull()
    }

    /** A negative count is a server bug, and is not something to print. */
    @Test
    fun `a negative count is treated as empty`() {
        assertThat(categoryCountLabel(-3)).isNull()
    }

    @Test
    fun `a category is empty at zero and only at zero`() {
        assertThat(category(productCount = 0).isEmpty).isTrue()
        assertThat(category(productCount = -1).isEmpty).isTrue()
        assertThat(category(productCount = 1).isEmpty).isFalse()
    }

    /** The field is additive: fixtures that predate it build a category that reads as empty. */
    @Test
    fun `the count defaults to zero`() {
        assertThat(
            Category(id = "c", name = "C", slug = "c", parentId = null, imageUrl = null, featured = false)
                .productCount,
        ).isEqualTo(0)
    }

    private fun category(productCount: Int) = Category(
        id = "c1",
        name = "Phones",
        slug = "phones",
        parentId = null,
        imageUrl = null,
        featured = false,
        productCount = productCount,
    )
}
