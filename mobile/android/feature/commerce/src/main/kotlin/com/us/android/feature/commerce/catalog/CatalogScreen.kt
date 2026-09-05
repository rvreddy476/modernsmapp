package com.us.android.feature.commerce.catalog

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.grid.rememberLazyGridState
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.model.ProductSummary
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.commerce.ui.CommerceImage
import com.us.android.feature.commerce.ui.PriceRow

/**
 * The catalogue.
 *
 * Paging is driven from the grid's own scroll position rather than from an
 * "onLastItemVisible" callback on the last row: the last row is composed
 * before it is reachable, so binding to it fetches a page the customer may
 * never scroll to. [PREFETCH_DISTANCE] rows from the end is early enough to
 * hide latency and late enough to reflect intent.
 */
@Composable
fun CatalogScreen(
    onOpenProduct: (productId: String) -> Unit,
    onOpenCart: () -> Unit,
    onOpenOrders: () -> Unit,
    onOpenSeller: () -> Unit,
    modifier: Modifier = Modifier,
    viewModel: CatalogViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val query by viewModel.query.collectAsStateWithLifecycle()

    UsScaffold(
        modifier = modifier,
        topBar = {
            // The catalogue is the one way into commerce (the Explore
            // launcher's Shop tile), so the buyer's orders and the seller hub
            // hang off its bar rather than off a second entry screen.
            UsTopBar(
                title = "Shop",
                actions = {
                    TopBarAction(text = "Orders", onClick = onOpenOrders)
                    TopBarAction(text = "Sell", onClick = onOpenSeller)
                    TopBarAction(text = "Cart", onClick = onOpenCart)
                },
            )
        },
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
                    ),
            )

            when (val s = state) {
                CatalogUiState.Loading -> UsLoadingState(label = "Loading products")

                is CatalogUiState.Empty -> UsEmptyState(
                    title = if (s.query.isBlank()) "Nothing here yet" else "No matches",
                    detail = if (s.query.isBlank()) {
                        "Products will appear here once sellers publish them."
                    } else {
                        "Nothing matched \"${s.query}\". Try a different search."
                    },
                )

                is CatalogUiState.Failed -> UsErrorState(
                    message = s.message,
                    onRetry = viewModel::retry.takeIf { s.retryable },
                )

                is CatalogUiState.Content -> CatalogGrid(
                    state = s,
                    onOpenProduct = onOpenProduct,
                    onLoadMore = viewModel::loadMore,
                )
            }
        }
    }
}

@Composable
private fun CatalogGrid(
    state: CatalogUiState.Content,
    onOpenProduct: (String) -> Unit,
    onLoadMore: () -> Unit,
) {
    val gridState = rememberLazyGridState()
    val shouldLoadMore by remember(state) {
        derivedStateOf {
            val last = gridState.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: return@derivedStateOf false
            state.canLoadMore && last >= state.items.size - PREFETCH_DISTANCE
        }
    }
    if (shouldLoadMore) onLoadMore()

    LazyVerticalGrid(
        columns = GridCells.Fixed(2),
        state = gridState,
        modifier = Modifier.fillMaxSize(),
        contentPadding = androidx.compose.foundation.layout.PaddingValues(
            horizontal = UsTheme.spacing.pageHorizontal,
            vertical = UsTheme.spacing.s,
        ),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        items(state.items, key = { it.id }) { product ->
            ProductCard(product = product, onClick = { onOpenProduct(product.id) })
        }
    }

    if (state.appending) {
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .padding(UsTheme.spacing.m),
            contentAlignment = Alignment.Center,
        ) {
            CircularProgressIndicator(modifier = Modifier.size(20.dp))
        }
    }
    state.appendError?.let { error ->
        Text(
            text = error,
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textSecondary,
            modifier = Modifier
                .fillMaxWidth()
                .padding(UsTheme.spacing.m),
        )
    }
}

@Composable
private fun ProductCard(product: ProductSummary, onClick: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(enabled = product.inStock || true, onClick = onClick),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        CommerceImage(
            url = product.thumbnailUrl ?: product.imageUrl,
            contentDescription = product.title,
            modifier = Modifier.fillMaxWidth(),
        )
        product.brandName?.takeIf { it.isNotBlank() }?.let {
            Text(
                text = it,
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.textSecondary,
            )
        }
        Text(
            text = product.title,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textPrimary,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
        )
        PriceRow(price = product.fromPrice, mrp = product.mrp)
        if (!product.inStock) {
            Text(
                text = "Out of stock",
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.textSecondary,
            )
        }
    }
}

private const val PREFETCH_DISTANCE = 4

@Preview(showBackground = true)
@Composable
@Suppress("MagicNumber")
private fun ProductCardPreview() {
    UsTheme {
        ProductCard(
            product = ProductSummary(
                id = "p1",
                title = "A product with a title long enough to wrap onto two lines",
                brandName = "Brand",
                primaryImageMediaId = null,
                fromPrice = Paise(118000),
                mrp = Paise(149000),
                avgRating = 4.3f,
                reviewCount = 27,
                inStock = true,
            ),
            onClick = {},
        )
    }
}

/** One text action on the catalogue's bar, in the design system's label style. */
@Composable
private fun TopBarAction(text: String, onClick: () -> Unit) {
    Text(
        text = text,
        style = MaterialTheme.typography.labelLarge,
        color = UsTheme.extended.textPrimary,
        modifier = Modifier
            .clickable(onClick = onClick)
            .padding(horizontal = UsTheme.spacing.s),
    )
}
