package com.us.android.feature.commerce.home

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyGridScope
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.grid.rememberLazyGridState
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import coil3.compose.AsyncImage
import com.us.android.core.commerce.model.BannerTarget
import com.us.android.core.commerce.model.Category
import com.us.android.core.commerce.model.HomeBanner
import com.us.android.core.commerce.model.HomeSection
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.commerce.ui.CommercePerson
import com.us.android.feature.commerce.ui.CommerceProgressLine
import com.us.android.feature.commerce.ui.MStoreSearchPill
import com.us.android.feature.commerce.ui.MStoreTopBar
import com.us.android.feature.commerce.ui.ProductCard
import com.us.android.feature.commerce.ui.ShelfProductCard
import com.us.android.feature.commerce.ui.pressScale

/**
 * MStore's home (founder, 2026-09-05).
 *
 * Top to bottom, the way a large marketplace is built: the search pill, the
 * category strip, the offers rail, "Deals of the day" and the other shelves,
 * "Shop by category", then everything else as a paged grid.
 *
 * It is ONE lazy grid, not a scrolling column with a grid inside it: a lazy
 * grid nested in a lazy column has no bounded height and the shop would
 * measure forever. The shelves are full-span items in the same grid, so the
 * whole page recycles as one list.
 */
@Composable
fun StoreHomeScreen(
    person: CommercePerson,
    destinations: StoreHomeDestinations,
    modifier: Modifier = Modifier,
    viewModel: StoreHomeViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val bagCount by viewModel.bagCount.collectAsStateWithLifecycle()

    // Re-read on every arrival, not only the first: the badge is wrong the
    // moment something is added on the product screen and the buyer comes
    // back here.
    LaunchedEffect(Unit) { viewModel.refreshBagCount() }

    UsScaffold(
        modifier = modifier,
        topBar = {
            MStoreTopBar(
                bagCount = bagCount,
                person = person,
                onOpenFavourites = destinations.onOpenFavourites,
                onOpenBag = destinations.onOpenBag,
                onOpenProfile = destinations.onOpenProfile,
            )
        },
        applyPageGutter = false,
    ) { padding ->
        Box(modifier = Modifier.fillMaxSize().padding(padding)) {
            when (val s = state) {
                StoreHomeUiState.Loading -> UsLoadingState(label = "Loading MStore")

                is StoreHomeUiState.Failed -> UsErrorState(
                    message = s.message,
                    onRetry = viewModel::refresh.takeIf { s.retryable },
                )

                is StoreHomeUiState.Content -> StoreHomeContent(
                    state = s,
                    destinations = destinations,
                    onToggleFavourite = viewModel::toggleFavourite,
                    onLoadMore = viewModel::loadMore,
                )
            }
            val message = (state as? StoreHomeUiState.Content)?.message
            UsMessageHost(
                message = message?.let { UsMessage(it, UsMessageType.Error) },
                onDismiss = viewModel::dismissMessage,
            )
        }
    }
}

@Composable
private fun StoreHomeContent(
    state: StoreHomeUiState.Content,
    destinations: StoreHomeDestinations,
    onToggleFavourite: (String) -> Unit,
    onLoadMore: () -> Unit,
) {
    val gridState = rememberLazyGridState()
    val shouldLoadMore by remember(state) {
        derivedStateOf {
            val last = gridState.layoutInfo.visibleItemsInfo.lastOrNull()?.index
                ?: return@derivedStateOf false
            state.canLoadMore && last >= state.products.size - PREFETCH_DISTANCE
        }
    }
    if (shouldLoadMore) onLoadMore()

    LazyVerticalGrid(
        columns = GridCells.Fixed(GRID_COLUMNS),
        state = gridState,
        modifier = Modifier.fillMaxSize().testTag("mstore_home"),
        contentPadding = PaddingValues(bottom = UsTheme.spacing.xxl),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        fullWidth {
            MStoreSearchPill(
                query = "",
                onClick = destinations.onOpenSearch,
                modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal),
            )
        }

        storefront(state, destinations, onToggleFavourite)

        fullWidth {
            SectionHeading(
                text = "All products",
                modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal),
            )
        }

        items(state.products, key = { it.id }) { product ->
            ProductCard(
                product = product,
                onClick = { destinations.onOpenProduct(product.id) },
                onToggleFavourite = { onToggleFavourite(product.id) },
                modifier = Modifier.padding(horizontal = UsTheme.spacing.s),
            )
        }

        // The append indicator and its failure line live INSIDE the grid,
        // spanning the row. As siblings below a grid that fills the screen
        // they would be permanently below the fold.
        if (state.appending) {
            fullWidth {
                Box(
                    modifier = Modifier.fillMaxWidth().padding(vertical = UsTheme.spacing.l),
                    contentAlignment = Alignment.Center,
                ) {
                    CommerceProgressLine(contentDescription = "Loading more products")
                }
            }
        }
        state.appendError?.let { error ->
            fullWidth {
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

/**
 * Everything above the grid: the category strip, the offers rail, the named
 * shelves, and the "Shop by category" block.
 *
 * Every block draws only when the server gave it something. The rules are in
 * [filledSections] / [topLevel] / [showsBanners] — pure functions with a unit
 * test — so "no empty shelves" is checkable without a screenshot.
 */
private fun LazyGridScope.storefront(
    state: StoreHomeUiState.Content,
    destinations: StoreHomeDestinations,
    onToggleFavourite: (String) -> Unit,
) {
    if (state.visibleCategories.isNotEmpty()) {
        fullWidth { CategoryStrip(state.visibleCategories, destinations.onOpenCategory) }
    }

    if (showsBanners(state.banners)) {
        fullWidth { BannerRail(state.banners, destinations) }
    }

    state.visibleSections.forEach { section ->
        fullWidth {
            ProductShelf(
                section = section,
                onOpenProduct = destinations.onOpenProduct,
                onToggleFavourite = onToggleFavourite,
            )
        }
    }

    if (state.visibleCategories.isNotEmpty()) {
        fullWidth { CategoryGrid(state.visibleCategories, destinations.onOpenCategory) }
    }
}

/** A full-row block inside the product grid. */
private fun LazyGridScope.fullWidth(
    content: @Composable () -> Unit,
) = item(span = { GridItemSpan(maxLineSpan) }) { content() }

// ── Categories ──────────────────────────────────────────────────────────

/** The strip: a navy square with the category's picture or a glyph, its name under it. */
@Composable
private fun CategoryStrip(categories: List<Category>, onOpen: (String, String) -> Unit) {
    LazyRow(
        modifier = Modifier.fillMaxWidth().testTag("mstore_category_strip"),
        contentPadding = PaddingValues(horizontal = UsTheme.spacing.pageHorizontal),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxl),
    ) {
        items(categories, key = { it.id }) { category ->
            CategoryTile(category = category, onClick = { onOpen(category.id, category.name) })
        }
    }
}

/** The same tiles as a grid, further down: "Shop by category". */
@Composable
private fun CategoryGrid(categories: List<Category>, onOpen: (String, String) -> Unit) {
    Column(
        modifier = Modifier.fillMaxWidth().padding(horizontal = UsTheme.spacing.pageHorizontal),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        SectionHeading("Shop by category")
        categories.chunked(CATEGORY_COLUMNS).forEach { row ->
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                row.forEach { category ->
                    Box(modifier = Modifier.weight(1f), contentAlignment = Alignment.TopCenter) {
                        CategoryTile(
                            category = category,
                            onClick = { onOpen(category.id, category.name) },
                        )
                    }
                }
                repeat(CATEGORY_COLUMNS - row.size) { Spacer(Modifier.weight(1f)) }
            }
        }
    }
}

/**
 * One category: the launcher's flat navy square, with the category's own
 * picture when the server has one and a Lucide tag glyph when it does not —
 * so an unillustrated taxonomy still reads as a strip of categories rather
 * than a row of broken frames.
 */
@Composable
private fun CategoryTile(category: Category, onClick: () -> Unit) {
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        modifier = Modifier
            .width(CATEGORY_TILE)
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                contentDescription = "Browse ${category.name}"
            }
            .testTag("mstore_category:${category.id}"),
    ) {
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .size(CATEGORY_TILE)
                .clip(RoundedCornerShape(CATEGORY_TILE / TILE_CORNER_DIVISOR))
                .background(UsTheme.extended.brandNavy),
        ) {
            if (category.imageUrl.isNullOrBlank()) {
                Icon(
                    imageVector = UsIcons.Tag,
                    contentDescription = null,
                    tint = Color.White,
                    modifier = Modifier.size(CATEGORY_GLYPH),
                )
            } else {
                AsyncImage(
                    model = category.imageUrl,
                    contentDescription = null,
                    contentScale = ContentScale.Crop,
                    modifier = Modifier.fillMaxSize(),
                )
            }
        }
        Spacer(Modifier.height(UsTheme.spacing.s))
        Text(
            text = category.name,
            style = MaterialTheme.typography.labelMedium,
            color = UsTheme.extended.textPrimary,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

// ── Banners ─────────────────────────────────────────────────────────────

/**
 * The offers rail.
 *
 * A banner whose target this build cannot open is drawn as a picture rather
 * than as a control that does nothing — [HomeBanner.tappable] decides, and it
 * is the same rule the notification deep links follow.
 */
@Composable
private fun BannerRail(banners: List<HomeBanner>, destinations: StoreHomeDestinations) {
    LazyRow(
        modifier = Modifier.fillMaxWidth().testTag("mstore_banners"),
        contentPadding = PaddingValues(horizontal = UsTheme.spacing.pageHorizontal),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        items(banners, key = { it.id }) { banner ->
            BannerCard(banner = banner, onClick = { openBanner(banner, destinations) })
        }
    }
}

private fun openBanner(banner: HomeBanner, destinations: StoreHomeDestinations) {
    when (val target = banner.target) {
        is BannerTarget.OfProduct -> destinations.onOpenProduct(target.productId)
        is BannerTarget.OfCategory -> destinations.onOpenCategory(target.categoryId, banner.title)
        is BannerTarget.OfSearch -> destinations.onOpenSearch()
        BannerTarget.None -> Unit
    }
}

@Composable
private fun BannerCard(banner: HomeBanner, onClick: () -> Unit) {
    val shape = RoundedCornerShape(UsTheme.radii.card)
    val base = Modifier
        .width(BANNER_WIDTH)
        .height(BANNER_HEIGHT)
        .clip(shape)
        .background(UsTheme.extended.bgCard)
    Box(
        modifier = if (banner.tappable) {
            base
                .pressScale(onClick)
                .semantics {
                    role = Role.Button
                    contentDescription = listOfNotNull(banner.title, banner.subtitle).joinToString(". ")
                }
        } else {
            base.semantics {
                contentDescription = listOfNotNull(banner.title, banner.subtitle).joinToString(". ")
            }
        }.testTag("mstore_banner:${banner.id}"),
    ) {
        if (!banner.imageUrl.isNullOrBlank()) {
            AsyncImage(
                model = banner.imageUrl,
                contentDescription = null,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        }
        Column(
            modifier = Modifier
                .align(Alignment.BottomStart)
                .fillMaxWidth()
                .background(BannerScrim)
                .padding(UsTheme.spacing.xxl),
        ) {
            Text(
                text = banner.title,
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = Color.White,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            banner.subtitle?.let {
                Text(
                    text = it,
                    style = MaterialTheme.typography.bodySmall,
                    color = Color.White,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}

// ── Shelves ─────────────────────────────────────────────────────────────

/** One named shelf: its title, then its products in a horizontal rail. */
@Composable
private fun ProductShelf(
    section: HomeSection,
    onOpenProduct: (String) -> Unit,
    onToggleFavourite: (String) -> Unit,
) {
    Column(
        modifier = Modifier.fillMaxWidth().testTag("mstore_shelf:${section.key}"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        SectionHeading(
            text = section.title.ifBlank { section.key },
            modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal),
        )
        LazyRow(
            contentPadding = PaddingValues(horizontal = UsTheme.spacing.pageHorizontal),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            items(section.products, key = { it.id }) { product ->
                ShelfProductCard(
                    product = product,
                    onClick = { onOpenProduct(product.id) },
                    onToggleFavourite = { onToggleFavourite(product.id) },
                )
            }
        }
    }
}

@Composable
private fun SectionHeading(text: String, modifier: Modifier = Modifier) {
    Text(
        text = text,
        style = MaterialTheme.typography.titleMedium,
        fontWeight = FontWeight.SemiBold,
        color = UsTheme.extended.textPrimary,
        modifier = modifier.semantics { heading() },
    )
}

private const val GRID_COLUMNS = 2
private const val CATEGORY_COLUMNS = 4
private const val PREFETCH_DISTANCE = 4
private const val TILE_CORNER_DIVISOR = 3
private val CATEGORY_TILE = 64.dp
private val CATEGORY_GLYPH = 26.dp
private val BANNER_WIDTH = 300.dp
private val BANNER_HEIGHT = 150.dp

/** Transparent at the top of the caption block, black at 55% under the text. */
private val BannerScrim = androidx.compose.ui.graphics.Brush.verticalGradient(
    listOf(Color.Transparent, Color.Black.copy(alpha = 0.55f)),
)
