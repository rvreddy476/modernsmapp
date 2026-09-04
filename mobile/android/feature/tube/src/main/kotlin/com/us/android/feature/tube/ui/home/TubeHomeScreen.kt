package com.us.android.feature.tube.ui.home

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextAlign
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.paging.LoadState
import androidx.paging.compose.LazyPagingItems
import androidx.paging.compose.collectAsLazyPagingItems
import androidx.paging.compose.itemKey
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.FeedItem
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState

/**
 * Tube home (founder, 2026-09-05): a plain list of long videos, newest
 * first as the server ranks them, under a pushed screen's bar — back, the
 * title, search. Pull down to refresh; the next page loads as the list
 * nears its end. The viewer's own video sits on top while it posts.
 *
 * A card opens the watch screen; the list the viewer was looking at goes
 * with them ([TubeHomeViewModel.onOpen]) so "Up next" is the rows below.
 */
@Composable
fun TubeHomeScreen(
    onBack: () -> Unit,
    onOpenSearch: () -> Unit,
    onOpenVideo: (postId: String) -> Unit,
    viewModel: TubeHomeViewModel = hiltViewModel(),
) {
    val items = viewModel.items.collectAsLazyPagingItems()
    val head by viewModel.head.collectAsStateWithLifecycle()
    val open: (FeedItem) -> Unit = { item ->
        viewModel.onOpen(items.itemSnapshotList.items)
        onOpenVideo(item.id)
    }

    UsScaffold(
        applyPageGutter = false,
        topBar = {
            UsTopBar(
                title = "Tube",
                onBack = onBack,
                actions = {
                    IconButton(onClick = onOpenSearch, modifier = Modifier.testTag("tube_search")) {
                        Icon(
                            imageVector = UsIcons.Search,
                            contentDescription = "Search videos",
                            tint = UsTheme.extended.textPrimary,
                        )
                    }
                },
            )
        },
    ) { padding ->
        TubeBody(
            items = items,
            head = head,
            padding = padding,
            viewModel = viewModel,
            onOpen = open,
        )
    }
}

/** The loading / error / empty states, or the list. A head always shows the list. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun TubeBody(
    items: LazyPagingItems<FeedItem>,
    head: TubeHead?,
    padding: PaddingValues,
    viewModel: TubeHomeViewModel,
    onOpen: (FeedItem) -> Unit,
) {
    val refresh = items.loadState.refresh
    val empty = items.itemCount == 0 && head == null
    Box(modifier = Modifier.fillMaxSize().padding(padding)) {
        when {
            refresh is LoadState.Loading && empty -> UsLoadingState(label = "Loading videos")

            refresh is LoadState.Error && empty -> UsErrorState(
                message = "We couldn't load videos.",
                onRetry = items::retry,
            )

            refresh is LoadState.NotLoading && empty -> UsEmptyState(
                title = "No videos yet",
                detail = "Long videos will show up here once someone posts one.",
                modifier = Modifier.testTag("tube_empty"),
            )

            else -> PullToRefreshBox(
                isRefreshing = refresh is LoadState.Loading,
                onRefresh = items::refresh,
                modifier = Modifier.fillMaxSize(),
            ) {
                VideoList(items = items, head = head, viewModel = viewModel, onOpen = onOpen)
            }
        }
    }
}

@Composable
private fun VideoList(
    items: LazyPagingItems<FeedItem>,
    head: TubeHead?,
    viewModel: TubeHomeViewModel,
    onOpen: (FeedItem) -> Unit,
) {
    LazyColumn(
        modifier = Modifier
            .fillMaxSize()
            .testTag("tube_list"),
        contentPadding = PaddingValues(vertical = UsTheme.spacing.s),
    ) {
        when (head) {
            is TubeHead.Pending -> item(key = "pending:${head.creationKey}") {
                PendingVideoCard(head = head, onRetry = viewModel::retryPublish, onDiscard = viewModel::discardPublish)
            }
            is TubeHead.Live -> item(key = "live:${head.item.id}") {
                VideoCard(item = head.item, thumb = viewModel.thumb(head.item), onClick = { onOpen(head.item) })
            }
            null -> Unit
        }
        items(count = items.itemCount, key = items.itemKey { it.id }) { index ->
            val item = items[index] ?: return@items
            VideoCard(item = item, thumb = viewModel.thumb(item), onClick = { onOpen(item) })
        }
        appendFooter(items)
    }
}

/** The next page's loader, or its failure with a retry — never a silent end. */
private fun LazyListScope.appendFooter(items: LazyPagingItems<FeedItem>) {
    when (val append = items.loadState.append) {
        is LoadState.Loading -> item(key = "append:loading") {
            UsLoadingState(
                label = "Loading more videos",
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(UsTheme.spacing.xxl),
            )
        }
        is LoadState.Error -> item(key = "append:error") {
            AppendError(onRetry = items::retry)
        }
        is LoadState.NotLoading -> Unit
    }
}

@Composable
private fun AppendError(onRetry: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(UsTheme.spacing.pageHorizontal),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Text(
            text = "Couldn't load more.",
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textSecondary,
            textAlign = TextAlign.Center,
        )
        UsSecondaryButton(text = "Try again", onClick = onRetry, modifier = Modifier.fillMaxWidth())
    }
}
