package com.us.android.feature.commerce.browse

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.grid.rememberLazyGridState
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.commerce.ui.CommerceProgressLine
import com.us.android.feature.commerce.ui.MStorePageBar
import com.us.android.feature.commerce.ui.ProductCard

/**
 * MStore's results page — a search, a category, or both.
 *
 * Reached from the home page's search pill and from every category tile. The
 * bar is MStore's, so the wordmark is still on the left five screens deep, and
 * the field is here rather than on the landing page because a pill that both
 * looks like a button and edits in place is the control people tap twice.
 */
@Composable
fun StoreBrowseScreen(
    onBack: () -> Unit,
    onOpenProduct: (productId: String) -> Unit,
    modifier: Modifier = Modifier,
    viewModel: StoreBrowseViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val query by viewModel.query.collectAsStateWithLifecycle()

    LaunchedEffect(Unit) { viewModel.refreshBagCount() }

    UsScaffold(
        modifier = modifier,
        topBar = { MStorePageBar(title = viewModel.title, onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Column(modifier = Modifier.padding(padding)) {
            UsTextField(
                value = query,
                onValueChange = viewModel::onQueryChange,
                label = "Search products",
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(
                        horizontal = UsTheme.spacing.pageHorizontal,
                        vertical = UsTheme.spacing.s,
                    )
                    .testTag("mstore_browse_search"),
            )

            Box(modifier = Modifier.fillMaxSize()) {
                when (val s = state) {
                    StoreBrowseUiState.Loading -> UsLoadingState(label = "Loading products")

                    is StoreBrowseUiState.Empty -> UsEmptyState(
                        title = if (s.query.isBlank()) "Nothing here yet" else "No matches",
                        detail = emptyDetail(s),
                    )

                    is StoreBrowseUiState.Failed -> UsErrorState(
                        message = s.message,
                        onRetry = viewModel::retry.takeIf { s.retryable },
                    )

                    is StoreBrowseUiState.Content -> BrowseGrid(
                        state = s,
                        onOpenProduct = onOpenProduct,
                        onToggleFavourite = viewModel::toggleFavourite,
                        onLoadMore = viewModel::loadMore,
                    )
                }
                val message = (state as? StoreBrowseUiState.Content)?.message
                UsMessageHost(
                    message = message?.let { UsMessage(it, UsMessageType.Error) },
                    onDismiss = viewModel::dismissMessage,
                )
            }
        }
    }
}

private fun emptyDetail(state: StoreBrowseUiState.Empty): String = when {
    state.query.isNotBlank() -> "Nothing matched \"${state.query}\". Try a different search."
    state.filtered -> "No products in this category yet."
    else -> "Products will appear here once sellers publish them."
}

@Composable
private fun BrowseGrid(
    state: StoreBrowseUiState.Content,
    onOpenProduct: (String) -> Unit,
    onToggleFavourite: (String) -> Unit,
    onLoadMore: () -> Unit,
) {
    val gridState = rememberLazyGridState()

    // Paging is driven from the grid's own scroll position rather than from an
    // "onLastItemVisible" callback on the last row: the last row is composed
    // before it is reachable, so binding to it fetches a page the buyer may
    // never scroll to.
    val shouldLoadMore by remember(state) {
        derivedStateOf {
            val last = gridState.layoutInfo.visibleItemsInfo.lastOrNull()?.index
                ?: return@derivedStateOf false
            state.canLoadMore && last >= state.items.size - PREFETCH_DISTANCE
        }
    }
    if (shouldLoadMore) onLoadMore()

    LazyVerticalGrid(
        columns = GridCells.Fixed(GRID_COLUMNS),
        state = gridState,
        modifier = Modifier.fillMaxSize().testTag("mstore_browse_grid"),
        contentPadding = PaddingValues(
            horizontal = UsTheme.spacing.pageHorizontal,
            vertical = UsTheme.spacing.s,
        ),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        items(state.items, key = { it.id }) { product ->
            ProductCard(
                product = product,
                onClick = { onOpenProduct(product.id) },
                onToggleFavourite = { onToggleFavourite(product.id) },
            )
        }

        // Both the append indicator and its failure line live INSIDE the grid,
        // spanning the row: as siblings after a grid that fills the screen they
        // would sit permanently below the fold, so a buyer whose next page
        // failed would be shown nothing at all.
        if (state.appending) {
            item(span = { GridItemSpan(maxLineSpan) }) {
                Box(
                    modifier = Modifier.fillMaxWidth().padding(vertical = UsTheme.spacing.l),
                    contentAlignment = Alignment.Center,
                ) {
                    CommerceProgressLine(contentDescription = "Loading more products")
                }
            }
        }
        state.appendError?.let { error ->
            item(span = { GridItemSpan(maxLineSpan) }) {
                Text(
                    text = error,
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textSecondary,
                    modifier = Modifier.fillMaxWidth().padding(UsTheme.spacing.m),
                )
            }
        }
    }
}

private const val GRID_COLUMNS = 2
private const val PREFETCH_DISTANCE = 4
