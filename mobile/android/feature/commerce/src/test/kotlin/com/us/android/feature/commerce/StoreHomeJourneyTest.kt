package com.us.android.feature.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.BannerTarget
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.network.CartDto
import com.us.android.core.commerce.network.CartLineDto
import com.us.android.core.commerce.network.CategoryDto
import com.us.android.core.commerce.network.HomeBannerDto
import com.us.android.core.commerce.network.HomeDto
import com.us.android.core.commerce.network.HomeSectionDto
import com.us.android.core.commerce.network.ProductListDto
import com.us.android.core.commerce.network.ProductSummaryDto
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.commerce.home.StoreHomeUiState
import com.us.android.feature.commerce.home.StoreHomeViewModel
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Response

/**
 * MStore's landing page, through the REAL repository and the REAL ViewModel.
 *
 * The seam is [com.us.android.core.commerce.network.CommerceApi], the last
 * thing before the wire, so everything under test is production code:
 * envelope unwrapping, DTO decoding, the domain mapping, the degrade rules and
 * the favourite contract. A fake repository would skip precisely the mapping
 * these cases are about.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class StoreHomeJourneyTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() = Dispatchers.setMain(dispatcher)

    @After
    fun tearDown() = Dispatchers.resetMain()

    // ─── The fake wire ───────────────────────────────────────────────

    private class HomeApi : FakeCommerceApi() {
        var homeResponse: Response<ApiEnvelope<HomeDto>> = envelope(
            HomeDto(
                banners = listOf(
                    HomeBannerDto(
                        id = "b1",
                        title = "Festive sale",
                        subtitle = "Up to 40% off",
                        targetType = "category",
                        targetId = "cat-1",
                    ),
                ),
                sections = listOf(
                    HomeSectionDto("deals", "Deals of the day", listOf(summary("p1"))),
                    HomeSectionDto("best_sellers", "Best sellers", emptyList()),
                ),
            ),
        )

        var categoriesResponse: Response<ApiEnvelope<List<CategoryDto>>> = envelope(
            listOf(
                CategoryDto(id = "cat-1", name = "Phones", slug = "phones"),
                CategoryDto(id = "cat-1a", name = "Cases", slug = "cases", parentId = "cat-1"),
            ),
        )

        var cartLines = listOf(
            CartLineDto(variantId = "v1", productId = "p1", quantity = 2),
            CartLineDto(variantId = "v2", productId = "p2", quantity = 3),
        )

        /** Set to make the next favourite write fail. */
        var favouriteWritesFail = false

        val favouriteAdds = mutableListOf<String>()
        val favouriteRemoves = mutableListOf<String>()

        override suspend fun home() = homeResponse

        override suspend fun categories() = categoriesResponse

        override suspend fun listProducts(
            query: String?,
            categoryId: String?,
            cursor: String?,
            limit: Int,
            sort: String?,
        ): Response<ApiEnvelope<ProductListDto>> =
            envelope(ProductListDto(items = listOf(summary("p1"), summary("p2")), nextCursor = null))

        override suspend fun getCart(): Response<ApiEnvelope<CartDto>> =
            envelope(CartDto(items = cartLines, itemCount = cartLines.size))

        override suspend fun addFavourite(productId: String): Response<ApiEnvelope<Unit>> {
            favouriteAdds += productId
            return if (favouriteWritesFail) unused() else envelope(Unit)
        }

        override suspend fun removeFavourite(productId: String): Response<ApiEnvelope<Unit>> {
            favouriteRemoves += productId
            return if (favouriteWritesFail) unused() else envelope(Unit)
        }
    }

    // ─── Cases ───────────────────────────────────────────────────────

    @Test
    fun `the page carries the shelves, the taxonomy and the grid`() = runTest(dispatcher) {
        val api = HomeApi()
        val vm = StoreHomeViewModel(CommerceRepository(api))
        advanceUntilIdle()

        val state = vm.state.value as StoreHomeUiState.Content

        // The empty shelf the server sent is not drawn; the full one is.
        assertThat(state.visibleSections.map { it.key }).containsExactly("deals")
        assertThat(state.visibleSections.single().title).isEqualTo("Deals of the day")

        // The strip is top-level only, so a child category does not appear
        // beside its parent as if it were a peer.
        assertThat(state.visibleCategories.map { it.id }).containsExactly("cat-1")

        // The banner's target is parsed into a closed type, so a target this
        // build cannot open renders as a picture rather than a dead control.
        assertThat(state.banners.single().target)
            .isEqualTo(BannerTarget.OfCategory("cat-1"))
        assertThat(state.banners.single().tappable).isTrue()

        assertThat(state.products.map { it.id }).containsExactly("p1", "p2").inOrder()
    }

    /**
     * The degrade the founder asked for: a server without `/home` must leave
     * MStore working, not show an error for a page nobody asked for.
     */
    @Test
    fun `a 404 from home leaves the categories and the grid`() = runTest(dispatcher) {
        val api = HomeApi().apply { homeResponse = notFound() }
        val vm = StoreHomeViewModel(CommerceRepository(api))
        advanceUntilIdle()

        val state = vm.state.value as StoreHomeUiState.Content
        assertThat(state.banners).isEmpty()
        assertThat(state.visibleSections).isEmpty()
        assertThat(state.visibleCategories).isNotEmpty()
        assertThat(state.products).isNotEmpty()
    }

    @Test
    fun `a taxonomy that fails does not take the shop down`() = runTest(dispatcher) {
        val api = HomeApi().apply { categoriesResponse = notFound() }
        val vm = StoreHomeViewModel(CommerceRepository(api))
        advanceUntilIdle()

        val state = vm.state.value as StoreHomeUiState.Content
        assertThat(state.visibleCategories).isEmpty()
        assertThat(state.products).isNotEmpty()
    }

    /** The badge counts UNITS, not lines: two of one thing and three of another is five. */
    @Test
    fun `the bag badge counts units`() = runTest(dispatcher) {
        val vm = StoreHomeViewModel(CommerceRepository(HomeApi()))
        advanceUntilIdle()

        assertThat(vm.bagCount.value).isEqualTo(FIVE)
    }

    @Test
    fun `an empty bag draws no badge`() = runTest(dispatcher) {
        val api = HomeApi().apply { cartLines = emptyList() }
        val vm = StoreHomeViewModel(CommerceRepository(api))
        advanceUntilIdle()

        assertThat(vm.bagCount.value).isEqualTo(0)
    }

    @Test
    fun `the heart fills at once and the write reaches the server`() = runTest(dispatcher) {
        val api = HomeApi()
        val vm = StoreHomeViewModel(CommerceRepository(api))
        advanceUntilIdle()

        vm.toggleFavourite("p1")

        // Optimistic: filled before the round trip, because a heart that waits
        // feels broken.
        assertThat(vm.favouriteOf("p1")).isTrue()
        advanceUntilIdle()
        assertThat(api.favouriteAdds).containsExactly("p1")
        assertThat(vm.favouriteOf("p1")).isTrue()

        // And the same product on the shelf agrees with the one in the grid.
        val shelf = (vm.state.value as StoreHomeUiState.Content)
            .sections.first().products.single { it.id == "p1" }
        assertThat(shelf.favourite).isTrue()
    }

    /**
     * Server-authoritative. A favourite the server does not have is a
     * favourite that vanishes on the next screen, so a refused write flips the
     * heart BACK and says why.
     */
    @Test
    fun `a refused favourite reverts the heart and says so`() = runTest(dispatcher) {
        val api = HomeApi().apply { favouriteWritesFail = true }
        val vm = StoreHomeViewModel(CommerceRepository(api))
        advanceUntilIdle()

        vm.toggleFavourite("p1")
        advanceUntilIdle()

        assertThat(vm.favouriteOf("p1")).isFalse()
        assertThat((vm.state.value as StoreHomeUiState.Content).message).isNotNull()
    }

    @Test
    fun `unsaving sends a delete, not a second add`() = runTest(dispatcher) {
        val api = HomeApi()
        val vm = StoreHomeViewModel(CommerceRepository(api))
        advanceUntilIdle()

        vm.toggleFavourite("p2")
        advanceUntilIdle()
        vm.toggleFavourite("p2")
        advanceUntilIdle()

        assertThat(api.favouriteAdds).containsExactly("p2")
        assertThat(api.favouriteRemoves).containsExactly("p2")
        assertThat(vm.favouriteOf("p2")).isFalse()
    }

    private fun StoreHomeViewModel.favouriteOf(productId: String): Boolean =
        (state.value as StoreHomeUiState.Content).products.single { it.id == productId }.favourite

    private companion object {
        const val FIVE = 5

        fun summary(id: String) = ProductSummaryDto(
            id = id,
            title = "Product $id",
            minPriceMinor = Paise(80_000),
            mrpMinor = Paise(100_000),
        )
    }
}
