package com.us.android.feature.feed.ui.reels.explore

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.grid.rememberLazyGridState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import coil3.compose.AsyncImage
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.FeedItem
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsFilterChipRow
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.formatCount

/**
 * Reels Explore (web parity, 2026-10-24): "All" and the short-form
 * categories as chips over a grid of reel covers — still pictures, nothing
 * plays here. A tile opens that reel in the Reels tab, where it starts
 * muted like every reel; leaving goes through `:app`, which knows the tab.
 */
@Composable
fun ReelsExploreScreen(
    onBack: () -> Unit,
    /** A tile was tapped; its id is already in `ReelsEntry`. `:app` switches to the Reels tab. */
    onOpenReels: () -> Unit,
    viewModel: ReelsExploreViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    UsScaffold(
        topBar = { UsTopBar(title = "Explore", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        ReelsExploreContent(
            state = state,
            actions = ExploreActions(
                onSelect = viewModel::selectCategory,
                onShowAll = viewModel::showAll,
                onRetry = viewModel::load,
                onRefresh = viewModel::refresh,
                onLoadMore = viewModel::loadMore,
                onOpenReel = { postId ->
                    viewModel.openReel(postId)
                    onOpenReels()
                },
            ),
            posterFor = viewModel::posterUrl,
            modifier = Modifier.padding(padding),
        )
    }
}

/** Everything the page can ask for, hoisted once. */
internal class ExploreActions(
    val onSelect: (String) -> Unit,
    val onShowAll: () -> Unit,
    val onRetry: () -> Unit,
    val onRefresh: () -> Unit,
    val onLoadMore: () -> Unit,
    val onOpenReel: (String) -> Unit,
)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun ReelsExploreContent(
    state: ReelsExploreState,
    actions: ExploreActions,
    posterFor: (FeedItem) -> String?,
    modifier: Modifier = Modifier,
) {
    Column(modifier = modifier.fillMaxSize().testTag("reels_explore")) {
        UsFilterChipRow(
            chips = state.chips,
            selectedKey = if (exploreBody(state) == ExploreBody.INVALID) "" else state.category,
            onSelect = actions.onSelect,
            tagPrefix = "explore_chip",
        )
        when (exploreBody(state)) {
            ExploreBody.LOADING -> UsLoadingState(label = "Loading reels")
            ExploreBody.FAILED -> UsErrorState(
                message = (state.phase as? ExplorePhase.Failed)?.message ?: "Couldn't load reels.",
                onRetry = actions.onRetry,
            )
            ExploreBody.INVALID -> ExploreNote(
                title = "This category isn't available",
                detail = "Pick another one above, or see everything.",
                action = "Show all",
                onAction = actions.onShowAll,
                tag = "explore_invalid",
            )
            ExploreBody.KEEP_LOOKING -> ExploreNote(
                title = "Nothing here yet",
                detail = "There may be more further back.",
                action = if (state.loadingMore) "Looking…" else "Keep looking",
                onAction = actions.onLoadMore,
                enabled = !state.loadingMore,
                tag = "explore_keep_looking",
            )
            ExploreBody.EMPTY -> ExploreNote(
                title = if (state.category.isEmpty()) "No reels yet" else "No reels in ${state.categoryLabel} yet",
                detail = null,
                action = if (state.category.isEmpty()) null else "Show all",
                onAction = actions.onShowAll,
                tag = "explore_empty",
            )
            ExploreBody.GRID -> PullToRefreshBox(
                isRefreshing = state.refreshing,
                onRefresh = actions.onRefresh,
                modifier = Modifier.fillMaxSize(),
            ) {
                ExploreGrid(state = state, actions = actions, posterFor = posterFor)
            }
        }
    }
}

/** The covers, three across, more as the end comes into view while pages bring reels. */
@Composable
private fun ExploreGrid(state: ReelsExploreState, actions: ExploreActions, posterFor: (FeedItem) -> String?) {
    val grid = rememberLazyGridState()
    val nearEnd by remember {
        derivedStateOf {
            val last = grid.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: 0
            last >= grid.layoutInfo.totalItemsCount - LOAD_MORE_WITHIN
        }
    }
    LaunchedEffect(nearEnd, state.wantsMore, state.tiles.size) {
        if (nearEnd && state.wantsMore) actions.onLoadMore()
    }
    LazyVerticalGrid(
        columns = GridCells.Fixed(GRID_COLUMNS),
        state = grid,
        modifier = Modifier.fillMaxSize().testTag("explore_grid"),
        contentPadding = PaddingValues(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.s),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        items(items = state.tiles, key = { it.id }) { reel ->
            ExploreTile(reel = reel, posterUrl = posterFor(reel), onClick = { actions.onOpenReel(reel.id) })
        }
        // A page that came back empty with a cursor stops the automatic
        // loading; the viewer asks for the next walk.
        if (state.canLoadMore && (state.lastPageEmpty || state.loadingMore || state.moreFailed)) {
            item(key = "more", span = { GridItemSpan(maxLineSpan) }) {
                MoreRow(loading = state.loadingMore, onMore = actions.onLoadMore)
            }
        }
    }
}

/** One reel: its cover, 9:16, with the creator and the like count along the bottom. */
@Composable
private fun ExploreTile(reel: FeedItem, posterUrl: String?, onClick: () -> Unit) {
    val name = reel.author.username?.takeIf { it.isNotBlank() }?.let { "@$it" } ?: reel.author.nameForDisplay
    val title = reel.title.ifBlank { reel.text.take(TITLE_CHARS) }.ifBlank { "Reel by $name" }
    Box(
        modifier = Modifier
            .aspectRatio(PORTRAIT_TILE)
            .clip(RoundedCornerShape(UsTheme.radii.small))
            .background(UsTheme.extended.bgCard)
            .clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                role = Role.Button,
                onClick = onClick,
            )
            .semantics { contentDescription = "$title, by $name, ${formatCount(reel.counts.likes)} likes" }
            .testTag("explore_tile:${reel.id}"),
    ) {
        if (posterUrl != null) {
            AsyncImage(model = posterUrl, contentDescription = null, contentScale = ContentScale.Crop, modifier = Modifier.fillMaxSize())
        } else {
            Icon(
                imageVector = UsIcons.Film,
                contentDescription = null,
                tint = UsTheme.extended.textDim,
                modifier = Modifier.align(Alignment.Center).size(TILE_GLYPH),
            )
        }
        Row(
            modifier = Modifier
                .align(Alignment.BottomStart)
                .fillMaxWidth()
                .background(UsTheme.extended.stage.copy(alpha = META_SCRIM))
                .padding(horizontal = UsTheme.spacing.s, vertical = UsTheme.spacing.xs),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        ) {
            Text(
                text = name,
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.onMedia,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f),
            )
            Icon(imageVector = UsIcons.HeartFilled, contentDescription = null, tint = UsTheme.extended.onMedia, modifier = Modifier.size(META_GLYPH))
            Text(text = formatCount(reel.counts.likes), style = MaterialTheme.typography.labelSmall, color = UsTheme.extended.onMedia)
        }
    }
}

/** A state with a line of help and, when there is one, the way out. */
@Composable
private fun ExploreNote(
    title: String,
    detail: String?,
    action: String?,
    onAction: () -> Unit,
    tag: String,
    enabled: Boolean = true,
) {
    Column(
        modifier = Modifier.fillMaxSize().padding(UsTheme.spacing.pageHorizontal).testTag(tag),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m, Alignment.CenterVertically),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(
            text = title,
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
            textAlign = TextAlign.Center,
        )
        if (detail != null) {
            Text(
                text = detail,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
                textAlign = TextAlign.Center,
            )
        }
        if (action != null) UsButton(text = action, onClick = onAction, enabled = enabled, modifier = Modifier.testTag("$tag:action"))
    }
}

@Composable
private fun MoreRow(loading: Boolean, onMore: () -> Unit) {
    Box(modifier = Modifier.fillMaxWidth().padding(vertical = UsTheme.spacing.l), contentAlignment = Alignment.Center) {
        if (loading) {
            CircularProgressIndicator(color = MaterialTheme.colorScheme.primary, strokeWidth = RING_STROKE, modifier = Modifier.size(RING))
        } else {
            UsSecondaryButton(text = "Show more", onClick = onMore, modifier = Modifier.testTag("explore_more"))
        }
    }
}

@Preview
@Composable
private fun ExploreEmptyPreview() {
    UsTheme {
        ReelsExploreContent(
            state = ReelsExploreState(phase = ExplorePhase.Ready),
            actions = ExploreActions({}, {}, {}, {}, {}, {}),
            posterFor = { null },
        )
    }
}

private const val GRID_COLUMNS = 3
private const val LOAD_MORE_WITHIN = GRID_COLUMNS * 2
private const val PORTRAIT_TILE = 9f / 16f
private const val TITLE_CHARS = 60
private const val META_SCRIM = 0.45f
private val TILE_GLYPH = 22.dp
private val META_GLYPH = 12.dp
private val RING = 24.dp
private val RING_STROKE = 2.dp
