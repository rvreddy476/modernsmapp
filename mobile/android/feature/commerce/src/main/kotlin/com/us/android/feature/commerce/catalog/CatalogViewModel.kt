package com.us.android.feature.commerce.catalog

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

/** Catalogue browse + search. */
sealed interface CatalogUiState {
    data object Loading : CatalogUiState

    data class Content(
        val items: List<ProductSummary>,
        /** Null once the server stops issuing cursors — see [canLoadMore]. */
        val nextCursor: String?,
        val appending: Boolean = false,
        /** Set when an append failed; the list keeps what it already has. */
        val appendError: String? = null,
    ) : CatalogUiState {
        val canLoadMore: Boolean get() = nextCursor != null && !appending
    }

    /** No results for the current query. Distinct from an empty catalogue. */
    data class Empty(val query: String) : CatalogUiState

    data class Failed(val message: String, val retryable: Boolean) : CatalogUiState
}

@OptIn(FlowPreview::class)
@HiltViewModel
class CatalogViewModel @Inject constructor(
    private val repo: CommerceRepository,
) : ViewModel() {

    private val _state = MutableStateFlow<CatalogUiState>(CatalogUiState.Loading)
    val state: StateFlow<CatalogUiState> = _state.asStateFlow()

    private val _query = MutableStateFlow("")
    val query: StateFlow<String> = _query.asStateFlow()

    init {
        load()
        viewModelScope.launch {
            _query
                // drop(1): the initial empty value is already covered by the
                // load() above, and re-issuing it here would double every
                // cold start.
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

    private fun load() {
        _state.value = CatalogUiState.Loading
        viewModelScope.launch {
            when (val r = repo.products(query = _query.value)) {
                is CommerceResult.Failure -> _state.value = r.error.toFailed()
                is CommerceResult.Success -> {
                    val page = r.value
                    _state.value = if (page.items.isEmpty()) {
                        CatalogUiState.Empty(_query.value)
                    } else {
                        CatalogUiState.Content(page.items, page.nextCursor)
                    }
                }
            }
        }
    }

    /**
     * Appends the next page.
     *
     * An append failure does NOT replace the list with an error screen: the
     * customer is already reading results, and throwing them away to show a
     * full-surface failure loses their place for a problem that affects only
     * the rows they have not reached yet.
     */
    fun loadMore() {
        val current = _state.value as? CatalogUiState.Content ?: return
        val cursor = current.nextCursor ?: return
        if (current.appending) return

        _state.value = current.copy(appending = true, appendError = null)
        viewModelScope.launch {
            when (val r = repo.products(query = _query.value, cursor = cursor)) {
                is CommerceResult.Failure ->
                    _state.value = current.copy(
                        appending = false,
                        appendError = r.error.describe(),
                    )

                is CommerceResult.Success ->
                    _state.value = CatalogUiState.Content(
                        // Deduplicate on id. A keyset page boundary can
                        // legitimately repeat a row when the underlying data
                        // shifts between requests, and a duplicate key in a
                        // LazyColumn is a crash, not a cosmetic issue.
                        items = (current.items + r.value.items).distinctBy { it.id },
                        nextCursor = r.value.nextCursor,
                    )
            }
        }
    }

    private fun CommerceError.toFailed() = CatalogUiState.Failed(
        message = describe(),
        retryable = this is CommerceError.Network || this is CommerceError.TryAgain,
    )
}

private const val SEARCH_DEBOUNCE_MS = 300L
