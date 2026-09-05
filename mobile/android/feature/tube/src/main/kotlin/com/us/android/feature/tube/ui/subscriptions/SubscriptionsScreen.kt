package com.us.android.feature.tube.ui.subscriptions

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.lazy.staggeredgrid.LazyVerticalStaggeredGrid
import androidx.compose.foundation.lazy.staggeredgrid.StaggeredGridCells
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
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
import com.us.android.feature.tube.ui.TubeTab
import com.us.android.feature.tube.ui.home.GRID_COLUMNS
import com.us.android.feature.tube.ui.home.GridCard
import com.us.android.feature.tube.ui.home.TubeGridSkeleton
import com.us.android.feature.tube.ui.home.appendFooter
import com.us.android.feature.tube.ui.rememberTubeMoreState

/**
 * Subscriptions (Momentum look, 2026-09-05): videos from the channels the
 * viewer follows, as the same two-column mosaic as home under the same
 * chrome. Empty says what to do about it.
 */
@Composable
fun SubscriptionsScreen(
    destinations: TubeDestinations,
    viewModel: SubscriptionsViewModel = hiltViewModel(),
    more: PostMoreViewModel = hiltViewModel(),
) {
    val items = viewModel.items.collectAsLazyPagingItems()
    val moreState = rememberTubeMoreState()
    val open: (FeedItem) -> Unit = { item ->
        viewModel.onOpen(items.itemSnapshotList.items)
        destinations.onOpenVideo(item.id)
    }

    TubePage(selected = TubeTab.SUBSCRIPTIONS, destinations = destinations) { padding ->
        Box(modifier = Modifier.fillMaxSize()) {
            SubscriptionsBody(
                items = items,
                thumbFor = viewModel::thumb,
                onOpen = open,
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

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun SubscriptionsBody(
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
            message = "We couldn't load your subscriptions.",
            onRetry = items::retry,
        )

        refresh is LoadState.NotLoading && empty -> UsEmptyState(
            title = "Follow creators to see their videos here",
            detail = "Videos from the channels you follow show up on this page as they post them.",
            modifier = Modifier.testTag("tube_subs_empty"),
        )

        else -> PullToRefreshBox(
            isRefreshing = refresh is LoadState.Loading,
            onRefresh = items::refresh,
            modifier = Modifier.fillMaxSize(),
        ) {
            LazyVerticalStaggeredGrid(
                columns = StaggeredGridCells.Fixed(GRID_COLUMNS),
                modifier = Modifier
                    .fillMaxSize()
                    .testTag("tube_subs_list"),
                contentPadding = PaddingValues(
                    start = UsTheme.spacing.pageHorizontal,
                    end = UsTheme.spacing.pageHorizontal,
                    top = UsTheme.spacing.l,
                    bottom = bottomPadding.calculateBottomPadding() + UsTheme.spacing.xxl,
                ),
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
                verticalItemSpacing = UsTheme.spacing.l,
            ) {
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
}
