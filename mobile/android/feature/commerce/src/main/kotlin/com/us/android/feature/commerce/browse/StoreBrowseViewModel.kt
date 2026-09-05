package com.us.android.feature.commerce.browse

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.commerce.model.ProductSummary
import com.us.android.core.commerce.repository.CommerceError
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.feature.commerce.ui.describe
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.FlowPreview
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.debounce
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.drop
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The results page: one paged grid, filtered by a category, a search, or
 * neither.
 *
 * One screen for all three because they are the same list with a different
 * `where` — a separate "search results" and "category" screen would drift on
 * paging, on the favourite heart, and on what an empty answer says.
 */
sealed interface StoreBrowseUiState {
    data object Loading : StoreBrowseUiState

    data class Content(
        val items: List<ProductSummary>,
        /** Null once the server stops issuing cursors — see [canLoadMore]. */
        val nextCursor: String?,
        val appending: Boolean = false,
        /** Set when an append failed; the list keeps what it already has. */
        val appendError: String? = null,
        val message: String? = null,
    ) : StoreBrowseUiState {
        val canLoadMore: Boolean get() = nextCursor != null && !appending
    }

    /** No results here. Distinct from an empty shop — the copy differs. */
    data class Empty(val query: String, val filtered: Boolean) : StoreBrowseUiState

    data class Failed(val message: String, val retryable: Boolean) : StoreBrowseUiState
}

@OptIn(FlowPreview::class)
@HiltViewModel
class StoreBrowseViewModel @Inject constructor(
    private val repo: CommerceRepository,
    savedState: SavedStateHandle,
) : ViewModel() {

    /** Blank when this is a plain search rather than a category browse. */
    val categoryId: String? = savedState.get<String>("categoryId")?.takeIf { it.isNotBlank() }

    /** What the bar says. The category's name, or "Search". */
    val title: String = savedState.get<String>("title")?.takeIf { it.isNotBlank() } ?: "Search"

    private val _state = MutableStateFlow<StoreBrowseUiState>(StoreBrowseUiState.Loading)
    val state: StateFlow<StoreBrowseUiState> = _state.asStateFlow()

    private val _query = MutableStateFlow(savedState.get<String>("query").orEmpty())
    val query: StateFlow<String> = _query.asStateFlow()

    private val _bagCount = MutableStateFlow(0)
    val bagCount: StateFlow<Int> = _bagCount.asStateFlow()

    init {
        load()
        refreshBagCount()
        viewModelScope.launch {
            _query
                // drop(1): the initial value is already covered by the load()
                // above, and re-issuing it here would double every cold start.
                .drop(1)
                .debounce(SEARCH_DEBOUNCE_MS)
                .distinctUntilChanged()
                .collect { load() }
        }
    }

    fun onQueryChange(value: String) {
        _query.value = value
    }

    fun retry() = load()

    fun refreshBagCount() {
        viewModelScope.launch {
            val result = repo.cart()
            if (result is CommerceResult.Success) {
                _bagCount.value = result.value.items.sumOf { it.quantity }
            }
        }
    }

    private fun load() {
        _state.value = StoreBrowseUiState.Loading
        viewModelScope.launch {
            when (val r = repo.products(query = _query.value, categoryId = categoryId)) {
                is CommerceResult.Failure -> _state.value = StoreBrowseUiState.Failed(
                    message = r.error.describe(),
                    retryable = r.error is CommerceError.Network || r.error is CommerceError.TryAgain,
                )

                is CommerceResult.Success -> {
                    val page = r.value
                    _state.value = if (page.items.isEmpty()) {
                        StoreBrowseUiState.Empty(_query.value, filtered = categoryId != null)
                    } else {
                        StoreBrowseUiState.Content(page.items, page.nextCursor)
                    }
                }
            }
        }
    }

    fun loadMore() {
        val current = _state.value as? StoreBrowseUiState.Content ?: return
        val cursor = current.nextCursor ?: return
        if (current.appending) return

        _state.value = current.copy(appending = true, appendError = null)
        viewModelScope.launch {
            when (
                val r = repo.products(
                    query = _query.value,
                    categoryId = categoryId,
                    cursor = cursor,
                )
            ) {
                is CommerceResult.Failure -> _state.value = current.copy(
                    appending = false,
                    appendError = r.error.describe(),
                )

                is CommerceResult.Success -> _state.value = current.copy(
                    items = (current.items + r.value.items).distinctBy { it.id },
                    nextCursor = r.value.nextCursor,
                    appending = false,
                )
            }
        }
    }

    /** Same contract as the landing page's: optimistic, then server-authoritative. */
    fun toggleFavourite(productId: String) {
        val current = _state.value as? StoreBrowseUiState.Content ?: return
        val wasFavourite = current.items.firstOrNull { it.id == productId }?.favourite ?: return

        _state.value = current.mapProduct(productId) { it.copy(favourite = !wasFavourite) }
            .copy(message = null)

        viewModelScope.launch {
            val result =
                if (wasFavourite) repo.removeFavourite(productId) else repo.addFavourite(productId)
            if (result is CommerceResult.Failure) {
                val reverted = _state.value as? StoreBrowseUiState.Content ?: return@launch
                _state.value = reverted.mapProduct(productId) { it.copy(favourite = wasFavourite) }
                    .copy(message = result.error.describe())
            }
        }
    }

    fun dismissMessage() {
        val current = _state.value as? StoreBrowseUiState.Content ?: return
        _state.value = current.copy(message = null)
    }
}

private fun StoreBrowseUiState.Content.mapProduct(
    productId: String,
    transform: (ProductSummary) -> ProductSummary,
): StoreBrowseUiState.Content =
    copy(items = items.map { if (it.id == productId) transform(it) else it })

private const val SEARCH_DEBOUNCE_MS = 300L
