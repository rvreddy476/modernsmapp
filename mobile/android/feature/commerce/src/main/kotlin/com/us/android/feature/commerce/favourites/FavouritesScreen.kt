package com.us.android.feature.commerce.favourites

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.commerce.model.ProductSummary
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.commerce.ui.MStorePageBar
import com.us.android.feature.commerce.ui.ProductCard
import com.us.android.feature.commerce.ui.describe
import com.us.android.feature.commerce.ui.isRetryable
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The saved products.
 *
 * Server-owned, like the bag: this page reads
 * `GET /v1/commerce/favourites` and holds no local list, so a heart filled on
 * a phone is filled on the web. A heart tapped HERE removes the row rather
 * than leaving an unfilled card behind — a list of favourites containing a
 * thing you just unfavourited is a list that argues with itself.
 */
sealed interface FavouritesUiState {
    data object Loading : FavouritesUiState
    data object Empty : FavouritesUiState
    data class Content(val items: List<ProductSummary>, val message: String? = null) : FavouritesUiState
    data class Failed(val message: String, val retryable: Boolean) : FavouritesUiState
}

@HiltViewModel
class FavouritesViewModel @Inject constructor(
    private val repo: CommerceRepository,
) : ViewModel() {

    private val _state = MutableStateFlow<FavouritesUiState>(FavouritesUiState.Loading)
    val state: StateFlow<FavouritesUiState> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        _state.value = FavouritesUiState.Loading
        viewModelScope.launch {
            when (val r = repo.favourites()) {
                is CommerceResult.Failure -> _state.value = FavouritesUiState.Failed(
                    message = r.error.describe(),
                    retryable = r.error.isRetryable(),
                )

                is CommerceResult.Success -> {
                    _state.value = if (r.value.isEmpty()) {
                        FavouritesUiState.Empty
                    } else {
                        // The server may or may not stamp is_favourite on this
                        // list; everything IN it is saved by definition, and a
                        // page of unfilled hearts would be nonsense.
                        FavouritesUiState.Content(r.value.map { it.copy(favourite = true) })
                    }
                }
            }
        }
    }

    /** Removes one. Optimistic, then reverted with a message if the server refuses. */
    fun remove(productId: String) {
        val current = _state.value as? FavouritesUiState.Content ?: return
        val removed = current.items.firstOrNull { it.id == productId } ?: return
        val without = current.items.filterNot { it.id == productId }
        _state.value =
            if (without.isEmpty()) FavouritesUiState.Empty else FavouritesUiState.Content(without)

        viewModelScope.launch {
            if (repo.removeFavourite(productId) is CommerceResult.Failure) {
                _state.value = FavouritesUiState.Content(
                    items = current.items,
                    message = "${removed.title} could not be removed. Please try again.",
                )
            }
        }
    }

    fun dismissMessage() {
        val current = _state.value as? FavouritesUiState.Content ?: return
        _state.value = current.copy(message = null)
    }
}

@Composable
fun FavouritesScreen(
    onBack: () -> Unit,
    onOpenProduct: (productId: String) -> Unit,
    modifier: Modifier = Modifier,
    viewModel: FavouritesViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    UsScaffold(
        modifier = modifier,
        topBar = { MStorePageBar(title = "Favourites", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Box(modifier = Modifier.fillMaxSize().padding(padding)) {
            when (val s = state) {
                FavouritesUiState.Loading -> UsLoadingState(label = "Loading favourites")

                FavouritesUiState.Empty -> UsEmptyState(
                    title = "Nothing saved yet",
                    detail = "Tap the heart on a product and it will wait for you here.",
                )

                is FavouritesUiState.Failed -> UsErrorState(
                    message = s.message,
                    onRetry = viewModel::refresh.takeIf { s.retryable },
                )

                is FavouritesUiState.Content -> LazyVerticalGrid(
                    columns = GridCells.Fixed(GRID_COLUMNS),
                    modifier = Modifier.fillMaxSize().testTag("mstore_favourites"),
                    contentPadding = PaddingValues(
                        horizontal = UsTheme.spacing.pageHorizontal,
                        vertical = UsTheme.spacing.s,
                    ),
                    horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
                    verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
                ) {
                    items(s.items, key = { it.id }) { product ->
                        ProductCard(
                            product = product,
                            onClick = { onOpenProduct(product.id) },
                            onToggleFavourite = { viewModel.remove(product.id) },
                        )
                    }
                }
            }
            val message = (state as? FavouritesUiState.Content)?.message
            UsMessageHost(
                message = message?.let { UsMessage(it, UsMessageType.Error) },
                onDismiss = viewModel::dismissMessage,
            )
        }
    }
}

private const val GRID_COLUMNS = 2
