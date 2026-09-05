package com.us.android.feature.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.Category
import com.us.android.core.commerce.model.HomeBanner
import com.us.android.core.commerce.model.HomeSection
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.model.ProductSummary
import com.us.android.feature.commerce.home.StoreHomeUiState
import com.us.android.feature.commerce.home.filledSections
import com.us.android.feature.commerce.home.showsBanners
import com.us.android.feature.commerce.home.topLevel
import com.us.android.feature.commerce.orders.OrderScope
import org.junit.Test

/**
 * MStore's landing page hides what the server has nothing for.
 *
 * The founder's rule, stated once: "every section hides itself when the server
 * has nothing for it — no empty shelves". A titled row over an empty scroller
 * is the single thing that most makes a shop look broken, and it is exactly
 * what a partly-seeded server produces, so the rule is a pure function with a
 * test rather than an `if` buried in a composable.
 */
class StoreHomeSectionsTest {

    private fun product(id: String) = ProductSummary(
        id = id,
        title = "Product $id",
        brandName = null,
        primaryImageMediaId = null,
        fromPrice = Paise(10_000),
        mrp = Paise(10_000),
        avgRating = 0f,
        reviewCount = 0,
        inStock = true,
    )

    private fun category(id: String, parent: String? = null) = Category(
        id = id,
        name = "Category $id",
        slug = id,
        parentId = parent,
        imageUrl = null,
        featured = false,
    )

    @Test
    fun `a shelf with no products is not drawn`() {
        val sections = listOf(
            HomeSection("deals", "Deals of the day", listOf(product("p1"))),
            HomeSection("best_sellers", "Best sellers", emptyList()),
            HomeSection("new_arrivals", "New arrivals", listOf(product("p2"))),
        )
        assertThat(filledSections(sections).map { it.key })
            .containsExactly("deals", "new_arrivals").inOrder()
    }

    @Test
    fun `the strip shows top-level categories only`() {
        val categories = listOf(
            category("root-a"),
            category("child-a", parent = "root-a"),
            category("root-b"),
        )
        assertThat(topLevel(categories).map { it.id })
            .containsExactly("root-a", "root-b").inOrder()
    }

    @Test
    fun `there is no house banner`() {
        assertThat(showsBanners(emptyList())).isFalse()
        assertThat(showsBanners(listOf(banner()))).isTrue()
    }

    /**
     * The degrade path: a server without `/home` answers 404, which becomes an
     * empty [com.us.android.core.commerce.model.StoreHome]. The page must
     * still be a working shop — the taxonomy and the grid carry it — rather
     * than an error or a wall of empty rows.
     */
    @Test
    fun `a shop with no home payload still has a grid`() {
        val state = StoreHomeUiState.Content(
            categories = listOf(category("root-a")),
            banners = emptyList(),
            sections = emptyList(),
            products = listOf(product("p1")),
            nextCursor = null,
        )
        assertThat(state.visibleSections).isEmpty()
        assertThat(state.visibleCategories).hasSize(1)
        assertThat(state.products).hasSize(1)
        assertThat(state.hasShelves).isTrue()
    }

    @Test
    fun `a completely bare landing page reports no shelves at all`() {
        val state = StoreHomeUiState.Content(products = listOf(product("p1")))
        assertThat(state.hasShelves).isFalse()
        assertThat(state.canLoadMore).isFalse()
    }

    @Test
    fun `paging stops when the server stops issuing cursors`() {
        assertThat(StoreHomeUiState.Content(nextCursor = "abc").canLoadMore).isTrue()
        assertThat(StoreHomeUiState.Content(nextCursor = "abc", appending = true).canLoadMore)
            .isFalse()
        assertThat(StoreHomeUiState.Content(nextCursor = null).canLoadMore).isFalse()
    }

    /**
     * "My orders" and "Purchase history" are different questions, and only
     * terminal states are history. A parcel in transit is not a past purchase.
     */
    @Test
    fun `purchase history is terminal orders only`() {
        assertThat(OrderScope.from("past")).isEqualTo(OrderScope.PAST)
        assertThat(OrderScope.from("all")).isEqualTo(OrderScope.ALL)

        // An unrecognised scope shows everything rather than nothing: the
        // safe failure for a list is too much, never a page that looks empty.
        assertThat(OrderScope.from(null)).isEqualTo(OrderScope.ALL)
        assertThat(OrderScope.from("nonsense")).isEqualTo(OrderScope.ALL)
    }

    private fun banner() = HomeBanner(
        id = "b1",
        title = "Sale",
        subtitle = null,
        imageUrl = null,
        target = com.us.android.core.commerce.model.BannerTarget.None,
    )
}
