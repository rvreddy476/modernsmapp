package com.us.android.feature.tube.ui.saved

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.staggeredgrid.LazyVerticalStaggeredGrid
import androidx.compose.foundation.lazy.staggeredgrid.StaggeredGridCells
import androidx.compose.foundation.lazy.staggeredgrid.StaggeredGridItemSpan
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.paging.LoadState
import androidx.paging.compose.LazyPagingItems
import androidx.paging.compose.collectAsLazyPagingItems
import androidx.paging.compose.itemKey
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.feed.ui.more.PostMoreViewModel
import com.us.android.core.model.FeedItem
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.feature.tube.navigation.TubeDestinations
import com.us.android.feature.tube.ui.TubeMoreHost
import com.us.android.feature.tube.ui.TubePage
import com.us.android.feature.tube.ui.home.GRID_COLUMNS
import com.us.android.feature.tube.ui.home.GridCard
import com.us.android.feature.tube.ui.home.TubeGridSkeleton
import com.us.android.feature.tube.ui.home.appendFooter
import com.us.android.feature.tube.ui.rememberTubeMoreState

/**
 * "Saved videos" (header More, 2026-09-05): the viewer's bookmarked long
 * videos as the two-column mosaic, under Tube's chrome with a back glyph
 * and nothing lit on the bar. Empty says where a save comes from.
 */
@Composable
fun SavedVideosScreen(
    destinations: TubeDestinations,
    viewModel: SavedVideosViewModel = hiltViewModel(),
    more: PostMoreViewModel = hiltViewModel(),
) {
    val items = viewModel.items.collectAsLazyPagingItems()
    val moreState = rememberTubeMoreState()

    TubePage(selected = null, destinations = destinations, onBack = destinations.onBack) { padding ->
        Box(modifier = Modifier.fillMaxSize()) {
            SavedBody(
                items = items,
                thumbFor = viewModel::thumb,
                onOpen = { item ->
                    viewModel.onOpen(items.itemSnapshotList.items)
                    destinations.onOpenVideo(item.id)
                },
                onMore = { item -> moreState.open(item, suggested = false) },
                bottomPadding = padding,
            )
            TubeMoreHost(
                state = moreState,
                overlays = viewModel.overlays,
                followEdges = viewModel.followEdges,
                ownUserId = viewModel.ownUserId,
                more = more,
            )
        }
    }
}

@Composable
private fun SavedBody(
    items: LazyPagingItems<FeedItem>,
    thumbFor: (FeedItem) -> VideoThumb,
    onOpen: (FeedItem) -> Unit,
    onMore: (FeedItem) -> Unit,
    bottomPadding: PaddingValues,
) {
    val refresh = items.loadState.refresh
    val empty = items.itemCount == 0
    when {
        refresh is LoadState.Loading && empty -> TubeGridSkeleton()

        refresh is LoadState.Error && empty -> UsErrorState(
            message = "We couldn't load your saved videos.",
            onRetry = items::retry,
        )

        refresh is LoadState.NotLoading && empty -> UsEmptyState(
            title = "No saved videos yet",
            detail = "Save a video from its ⋮ menu and it will be kept here.",
            modifier = Modifier.testTag("tube_saved_empty"),
        )

        else -> LazyVerticalStaggeredGrid(
            columns = StaggeredGridCells.Fixed(GRID_COLUMNS),
            modifier = Modifier
                .fillMaxSize()
                .testTag("tube_saved_list"),
            contentPadding = PaddingValues(
                start = UsTheme.spacing.pageHorizontal,
                end = UsTheme.spacing.pageHorizontal,
                top = UsTheme.spacing.l,
                bottom = bottomPadding.calculateBottomPadding() + UsTheme.spacing.xxl,
            ),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            verticalItemSpacing = UsTheme.spacing.l,
        ) {
            item(key = "title", span = StaggeredGridItemSpan.FullLine) {
                Text(
                    text = "Saved videos",
                    style = MaterialTheme.typography.titleLarge,
                    fontWeight = FontWeight.Bold,
                    color = UsTheme.extended.textPrimary,
                    modifier = Modifier.padding(bottom = TITLE_GAP),
                )
            }
            items(count = items.itemCount, key = items.itemKey { it.id }) { index ->
                val item = items[index] ?: return@items
                GridCard(
                    item = item,
                    thumb = thumbFor(item),
                    onClick = { onOpen(item) },
                    onMore = { onMore(item) },
                )
            }
            appendFooter(items)
        }
    }
}

private val TITLE_GAP = 4.dp
