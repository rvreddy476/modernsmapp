package com.us.android.feature.commerce

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.network.AddFavouriteRequest
import com.us.android.core.commerce.network.CartDto
import com.us.android.core.commerce.network.FavouriteDto
import com.us.android.core.commerce.network.ProductListDto
import com.us.android.core.commerce.network.ProductSummaryDto
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.commerce.browse.StoreBrowseUiState
import com.us.android.feature.commerce.browse.StoreBrowseViewModel
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
 * The heart on the results page, through the REAL repository and ViewModel.
 *
 * The seam is the Retrofit interface, so what a tap actually sends is what is
 * asserted: exactly one `AddFavouriteRequest` carrying the product id, which
 * is the body shape commerce-service binds. The old path-addressed add went
 * to a route that did not exist, and no test noticed because the fake took
 * a bare id. A fake that takes the request type cannot make that mistake.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class StoreBrowseFavouriteTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() = Dispatchers.setMain(dispatcher)

    @After
    fun tearDown() = Dispatchers.resetMain()

    private class BrowseApi : FakeCommerceApi() {
        var favouriteWritesFail = false
        val adds = mutableListOf<AddFavouriteRequest>()
        val removes = mutableListOf<String>()

        override suspend fun listProducts(
            query: String?,
            categoryId: String?,
            cursor: String?,
            limit: Int,
            sort: String?,
        ): Response<ApiEnvelope<ProductListDto>> =
            envelope(ProductListDto(items = listOf(summary("p1"), summary("p2")), nextCursor = null))

        override suspend fun getCart(): Response<ApiEnvelope<CartDto>> =
            envelope(CartDto(items = emptyList(), itemCount = 0))

        override suspend fun addFavourite(body: AddFavouriteRequest): Response<ApiEnvelope<FavouriteDto>> {
            adds += body
            return if (favouriteWritesFail) {
                unused()
            } else {
                envelope(FavouriteDto(productId = body.productId, isFavourite = true))
            }
        }

        override suspend fun removeFavourite(productId: String): Response<ApiEnvelope<Unit>> {
            removes += productId
            return if (favouriteWritesFail) unused() else envelope(Unit)
        }
    }

    private fun viewModel(api: BrowseApi) = StoreBrowseViewModel(
        repo = CommerceRepository(api),
        savedState = SavedStateHandle(mapOf("categoryId" to "cat-1", "title" to "Phones")),
    )

    @Test
    fun `a tap on the heart sends one add with the product id in the body`() = runTest(dispatcher) {
        val api = BrowseApi()
        val vm = viewModel(api)
        advanceUntilIdle()

        vm.toggleFavourite("p1")

        // Optimistic: the heart is on before the round trip.
        assertThat(vm.favouriteOf("p1")).isTrue()
        advanceUntilIdle()

        assertThat(api.adds).containsExactly(AddFavouriteRequest(productId = "p1"))
        assertThat(api.removes).isEmpty()
        assertThat(vm.favouriteOf("p1")).isTrue()
        assertThat((vm.state.value as StoreBrowseUiState.Content).message).isNull()
    }

    /**
     * Server-authoritative. A favourite the server refused is a favourite that
     * vanishes on the next screen, so the heart flips BACK and the page says
     * why rather than leaving a lie on the card.
     */
    @Test
    fun `a refused add rolls the heart back and says so`() = runTest(dispatcher) {
        val api = BrowseApi().apply { favouriteWritesFail = true }
        val vm = viewModel(api)
        advanceUntilIdle()

        vm.toggleFavourite("p1")
        assertThat(vm.favouriteOf("p1")).isTrue()
        advanceUntilIdle()

        assertThat(api.adds).containsExactly(AddFavouriteRequest(productId = "p1"))
        assertThat(vm.favouriteOf("p1")).isFalse()
        assertThat((vm.state.value as StoreBrowseUiState.Content).message).isNotNull()
    }

    @Test
    fun `unsaving is a delete on the item, not a second add`() = runTest(dispatcher) {
        val api = BrowseApi()
        val vm = viewModel(api)
        advanceUntilIdle()

        vm.toggleFavourite("p2")
        advanceUntilIdle()
        vm.toggleFavourite("p2")
        advanceUntilIdle()

        assertThat(api.adds).containsExactly(AddFavouriteRequest(productId = "p2"))
        assertThat(api.removes).containsExactly("p2")
        assertThat(vm.favouriteOf("p2")).isFalse()
    }

    private fun StoreBrowseViewModel.favouriteOf(productId: String): Boolean =
        (state.value as StoreBrowseUiState.Content).items.single { it.id == productId }.favourite

    private companion object {
        fun summary(id: String) = ProductSummaryDto(
            id = id,
            title = "Product $id",
            minPriceMinor = Paise(80_000),
            mrpMinor = Paise(100_000),
        )
    }
}
