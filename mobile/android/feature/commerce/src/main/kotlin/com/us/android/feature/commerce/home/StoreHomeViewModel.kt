package com.us.android.feature.commerce.home

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.commerce.model.Category
import com.us.android.core.commerce.model.HomeBanner
import com.us.android.core.commerce.model.HomeSection
import com.us.android.core.commerce.model.ProductSummary
import com.us.android.core.commerce.model.StoreHome
import com.us.android.core.commerce.repository.CommerceError
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.feature.commerce.ui.describe
import com.us.android.feature.commerce.ui.isRetryable
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * MStore's landing page.
 *
 * The old catalogue was a bare grid of product images and looked nothing like
 * a shop. This is the marketplace shape, in Momentum's clothes: a search pill,
 * a category strip, a banner rail, named shelves — deals, best sellers, new
 * arrivals — a "Shop by category" grid, and only then the paged grid of
 * everything.
 *
 * ## Every section hides itself
 *
 * The rules are pure functions below rather than `if` statements buried in
 * composables, so "no empty shelves" is a unit test rather than something only
 * a screenshot can catch. A server without `/home` answers 404, which the
 * repository reports as [CommerceError.NotAvailable]; that becomes
 * [StoreHome.EMPTY] and the page degrades to the taxonomy plus the grid rather
 * than to a shop full of empty rows.
 */
sealed interface StoreHomeUiState {
    data object Loading : StoreHomeUiState

    data class Content(
        val categories: List<Category> = emptyList(),
        val banners: List<HomeBanner> = emptyList(),
        val sections: List<HomeSection> = emptyList(),
        val products: List<ProductSummary> = emptyList(),
        val nextCursor: String? = null,
        val appending: Boolean = false,
        val appendError: String? = null,
        /** Set when a favourite toggle was refused. Cleared on the next tap. */
        val message: String? = null,
    ) : StoreHomeUiState {
        val canLoadMore: Boolean get() = nextCursor != null && !appending

        /** The strip and the "Shop by category" grid show top-level nodes only. */
        val visibleCategories: List<Category> get() = topLevel(categories)

        /** A shelf with nothing on it is not drawn. */
        val visibleSections: List<HomeSection> get() = filledSections(sections)

        /**
         * Whether the page has anything at all besides the grid.
         *
         * A completely bare landing page is still a working shop — the grid
         * carries it — so this is used for spacing, never to fail.
         */
        val hasShelves: Boolean
            get() = visibleCategories.isNotEmpty() || banners.isNotEmpty() || visibleSections.isNotEmpty()
    }

    data class Failed(val message: String, val retryable: Boolean) : StoreHomeUiState
}

/** Top-level nodes, in the server's own order. A child category is reached by browsing into it. */
fun topLevel(categories: List<Category>): List<Category> = categories.filter { it.isTopLevel }

/**
 * Shelves that actually have products.
 *
 * The founder's rule, stated once: "every section hides itself when the server
 * has nothing for it — no empty shelves". A titled row over an empty scroller
 * is the single thing that most makes a shop look broken.
 */
fun filledSections(sections: List<HomeSection>): List<HomeSection> =
    sections.filter { it.products.isNotEmpty() }

/** Banners are drawn only when there are some; there is no house banner. */
fun showsBanners(banners: List<HomeBanner>): Boolean = banners.isNotEmpty()

@HiltViewModel
class StoreHomeViewModel @Inject constructor(
    private val repo: CommerceRepository,
) : ViewModel() {

    private val _state = MutableStateFlow<StoreHomeUiState>(StoreHomeUiState.Loading)
    val state: StateFlow<StoreHomeUiState> = _state.asStateFlow()

    private val _bagCount = MutableStateFlow(0)

    /** Units in the bag, for the header badge. Zero draws no badge. */
    val bagCount: StateFlow<Int> = _bagCount.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        _state.value = StoreHomeUiState.Loading
        viewModelScope.launch { load() }
        refreshBagCount()
    }

    /**
     * Re-reads the bag for the header badge.
     *
     * Read-only and deliberately silent: a badge is not worth an error
     * screen, and a failure keeps the last known number rather than claiming
     * the bag is empty. Called on every arrival, because something added on
     * the product screen makes this number wrong the moment the buyer
     * returns.
     */
    fun refreshBagCount() {
        viewModelScope.launch {
            val result = repo.cart()
            if (result is CommerceResult.Success) {
                _bagCount.value = result.value.items.sumOf { it.quantity }
            }
        }
    }

    /**
     * Loads the three halves of the page at once.
     *
     * The grid is the only one that can fail the screen. The taxonomy and the
     * shelves are decoration over a shop that works without them, so their
     * failures degrade to "not shown" — which is also exactly what a 404 from
     * a server without `/home` has to mean.
     */
    private suspend fun load() = coroutineScope {
        val categories = async { repo.categories() }
        val home = async { repo.storeHome() }
        val page = async { repo.products() }

        when (val products = page.await()) {
            is CommerceResult.Failure -> {
                _state.value = StoreHomeUiState.Failed(
                    message = products.error.describe(),
                    retryable = products.error.isRetryable(),
                )
                return@coroutineScope
            }

            is CommerceResult.Success -> {
                val shop = home.await().orEmpty(StoreHome.EMPTY)
                _state.value = StoreHomeUiState.Content(
                    categories = categories.await().orEmpty(emptyList()),
                    banners = shop.banners,
                    sections = shop.sections,
                    products = products.value.items,
                    nextCursor = products.value.nextCursor,
                )
            }
        }
    }

    /**
     * Appends the next page of the grid.
     *
     * An append failure does not replace the page with an error screen: the
     * buyer is already reading the shop, and throwing it away loses their
     * place for a problem that affects only the rows below them.
     */
    fun loadMore() {
        val current = _state.value as? StoreHomeUiState.Content ?: return
        val cursor = current.nextCursor ?: return
        if (current.appending) return

        _state.value = current.copy(appending = true, appendError = null)
        viewModelScope.launch {
            when (val r = repo.products(cursor = cursor)) {
                is CommerceResult.Failure -> _state.value = current.copy(
                    appending = false,
                    appendError = r.error.describe(),
                )

                is CommerceResult.Success -> _state.value = current.copy(
                    // Deduplicate on id: a keyset page boundary can repeat a
                    // row when the data shifts between requests, and a
                    // duplicate key in a lazy list is a crash.
                    products = (current.products + r.value.items).distinctBy { it.id },
                    nextCursor = r.value.nextCursor,
                    appending = false,
                )
            }
        }
    }

    /**
     * Saves or unsaves a product.
     *
     * Optimistic, then server-authoritative: the heart flips at once because a
     * heart that waits for a round trip feels broken, and it flips BACK with a
     * message if the server refuses. What is never done is leaving the heart
     * on after a failed write — a favourite the server does not have is a
     * favourite that vanishes on the next screen.
     */
    fun toggleFavourite(productId: String) {
        val current = _state.value as? StoreHomeUiState.Content ?: return
        val wasFavourite = current.findFavourite(productId) ?: return

        _state.value = current.mapProduct(productId) { it.copy(favourite = !wasFavourite) }
            .copy(message = null)

        viewModelScope.launch {
            val result =
                if (wasFavourite) repo.removeFavourite(productId) else repo.addFavourite(productId)
            if (result is CommerceResult.Failure) {
                val reverted = _state.value as? StoreHomeUiState.Content ?: return@launch
                _state.value = reverted.mapProduct(productId) { it.copy(favourite = wasFavourite) }
                    .copy(message = result.error.describe())
            }
        }
    }

    fun dismissMessage() {
        val current = _state.value as? StoreHomeUiState.Content ?: return
        _state.value = current.copy(message = null)
    }
}

/** The current saved state of a product wherever it appears on the page. */
private fun StoreHomeUiState.Content.findFavourite(productId: String): Boolean? =
    (products + sections.flatMap { it.products }).firstOrNull { it.id == productId }?.favourite

/**
 * Applies a change to every copy of one product on the page.
 *
 * The same product legitimately appears on a shelf and in the grid, and a
 * heart that fills in one place and not the other is the bug this exists to
 * prevent.
 */
private fun StoreHomeUiState.Content.mapProduct(
    productId: String,
    transform: (ProductSummary) -> ProductSummary,
): StoreHomeUiState.Content = copy(
    products = products.map { if (it.id == productId) transform(it) else it },
    sections = sections.map { section ->
        section.copy(products = section.products.map { if (it.id == productId) transform(it) else it })
    },
)

/** A decorative read's failure is not the page's failure. */
private fun <T> CommerceResult<T>.orEmpty(fallback: T): T =
    (this as? CommerceResult.Success)?.value ?: fallback
