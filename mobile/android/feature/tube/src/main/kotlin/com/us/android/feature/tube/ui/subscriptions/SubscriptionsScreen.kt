package com.us.android.feature.tube.ui.subscriptions

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
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
import com.us.android.core.feed.ui.more.PostMoreViewModel
import com.us.android.core.model.FeedItem
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.feature.tube.navigation.TubeDestinations
import com.us.android.feature.tube.ui.TubeMoreHost
import com.us.android.feature.tube.ui.TubePage
import com.us.android.feature.tube.ui.TubeTab
import com.us.android.feature.tube.ui.home.TubeListSkeleton
import com.us.android.feature.tube.ui.home.VideoCard
import com.us.android.feature.tube.ui.home.appendFooter
import com.us.android.feature.tube.ui.rememberTubeMoreState

/**
 * Subscriptions (Tube redesign, 2026-09-05): videos from the authors the
 * viewer follows, as full-width cards under Tube's header, over Tube's
 * bar. Empty says what to do about it.
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

    TubePage(
        selected = TubeTab.SUBSCRIPTIONS,
        onOpenNotifications = destinations.onOpenNotifications,
        onOpenSearch = destinations.onOpenSearch,
        onBarAction = destinations::onBarAction,
    ) { padding ->
        Box(modifier = Modifier.fillMaxSize().padding(padding)) {
            SubscriptionsBody(
                items = items,
                thumbFor = viewModel::thumb,
                onOpen = open,
                onMore = { item -> moreState.open(item, suggested = false) },
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
    thumbFor: (FeedItem) -> com.us.android.feature.tube.ui.VideoThumb,
    onOpen: (FeedItem) -> Unit,
    onMore: (FeedItem) -> Unit,
) {
    val refresh = items.loadState.refresh
    val empty = items.itemCount == 0
    when {
        refresh is LoadState.Loading && empty -> TubeListSkeleton()

        refresh is LoadState.Error && empty -> UsErrorState(
            message = "We couldn't load your subscriptions.",
            onRetry = items::retry,
        )

        refresh is LoadState.NotLoading && empty -> UsEmptyState(
            title = "Follow creators to see their videos here",
            detail = "Videos from the people you follow show up on this page as they post them.",
            modifier = Modifier.testTag("tube_subs_empty"),
        )

        else -> PullToRefreshBox(
            isRefreshing = refresh is LoadState.Loading,
            onRefresh = items::refresh,
            modifier = Modifier.fillMaxSize(),
        ) {
            LazyColumn(
                modifier = Modifier
                    .fillMaxSize()
                    .testTag("tube_subs_list"),
                contentPadding = PaddingValues(vertical = UsTheme.spacing.s),
            ) {
                items(count = items.itemCount, key = items.itemKey { it.id }) { index ->
                    val item = items[index] ?: return@items
                    VideoCard(
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
