package com.us.android.feature.tube.ui.you

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.paging.LoadState
import androidx.paging.compose.LazyPagingItems
import androidx.paging.compose.collectAsLazyPagingItems
import androidx.paging.compose.itemKey
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsFollowButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.ContinueWatching
import com.us.android.core.feed.ui.more.PostMoreViewModel
import com.us.android.core.model.FeedItem
import com.us.android.feature.tube.navigation.TubeDestinations
import com.us.android.feature.tube.ui.TubeMoreHost
import com.us.android.feature.tube.ui.TubePage
import com.us.android.feature.tube.ui.TubeTab
import com.us.android.feature.tube.ui.VideoThumb
import com.us.android.feature.tube.ui.home.ContinueCard
import com.us.android.feature.tube.ui.home.ShelfHeader
import com.us.android.feature.tube.ui.home.ShelfSkeleton
import com.us.android.feature.tube.ui.home.ShelfVideoCard
import com.us.android.feature.tube.ui.rememberTubeMoreState

/**
 * You (Tube redesign, 2026-09-05): the viewer's row, then "Your videos",
 * "Continue watching" as a two-column grid, and "Saved". A section with
 * nothing to show is not drawn — except Your videos, whose empty state is
 * the invitation to post one.
 */
@Composable
fun YouScreen(
    destinations: TubeDestinations,
    viewModel: YouViewModel = hiltViewModel(),
    more: PostMoreViewModel = hiltViewModel(),
) {
    val profile by viewModel.profile.collectAsStateWithLifecycle()
    val continueWatching by viewModel.continueWatching.collectAsStateWithLifecycle()
    val own = viewModel.ownVideos.collectAsLazyPagingItems()
    val saved = viewModel.saved.collectAsLazyPagingItems()
    val moreState = rememberTubeMoreState()

    val actions = YouActions(
        openOwn = { item ->
            viewModel.onOpen(item, own.itemSnapshotList.items)
            destinations.onOpenVideo(item.id)
        },
        openSaved = { item ->
            viewModel.onOpen(item, saved.itemSnapshotList.items)
            destinations.onOpenVideo(item.id)
        },
        resume = { item ->
            viewModel.onOpen(item, continueWatching.orEmpty().map { it.item })
            destinations.onOpenVideo(item.id)
        },
        more = { item -> moreState.open(item, suggested = false) },
        postVideo = destinations.onCreateVideo,
        thumbFor = viewModel::thumb,
    )

    TubePage(
        selected = TubeTab.YOU,
        onOpenNotifications = destinations.onOpenNotifications,
        onOpenSearch = destinations.onOpenSearch,
        onBarAction = destinations::onBarAction,
    ) { padding ->
        Box(modifier = Modifier.fillMaxSize().padding(padding)) {
            LazyColumn(
                modifier = Modifier
                    .fillMaxSize()
                    .testTag("tube_you"),
                contentPadding = PaddingValues(bottom = UsTheme.spacing.xxl),
            ) {
                item(key = "profile") { ProfileRow(profile) }
                ownVideosSection(own, actions)
                continueWatchingSection(continueWatching, actions)
                savedSection(saved, actions)
            }
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

/** Everything the page's rows can do, so the section builders stay short. */
internal class YouActions(
    val openOwn: (FeedItem) -> Unit,
    val openSaved: (FeedItem) -> Unit,
    val resume: (FeedItem) -> Unit,
    val more: (FeedItem) -> Unit,
    val postVideo: () -> Unit,
    val thumbFor: (FeedItem) -> VideoThumb,
)

@Composable
private fun ProfileRow(profile: YouProfile?) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.xxl)
            .testTag("tube_you_profile"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        UsAvatar(
            name = profile?.name ?: "You",
            size = UsAvatarSize.Medium,
            imageUrl = profile?.avatarUrl,
        )
        Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
            Text(
                text = profile?.name ?: "You",
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            profile?.handle?.let { handle ->
                Text(
                    text = handle,
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textMuted,
                    maxLines = 1,
                )
            }
        }
    }
}

/**
 * "Your videos": a shelf of the viewer's own, or — when there are none, or
 * the list cannot be read — the invitation to post one. An unreadable list
 * and an empty one look the same here on purpose: either way the useful
 * thing on the page is the button.
 */
private fun LazyListScope.ownVideosSection(own: LazyPagingItems<FeedItem>, actions: YouActions) {
    item(key = "own:header") { ShelfHeader(title = "Your videos") }
    val refresh = own.loadState.refresh
    when {
        refresh is LoadState.Loading && own.itemCount == 0 -> item(key = "own:loading") { ShelfSkeleton() }
        own.itemCount == 0 -> item(key = "own:empty") { NoVideosYet(onPost = actions.postVideo) }
        else -> item(key = "own:shelf") {
            PagedShelf(items = own, thumbFor = actions.thumbFor, onOpen = actions.openOwn, onMore = actions.more)
        }
    }
}

/** "Continue watching" as a two-column grid; absent when there is nothing unfinished. */
private fun LazyListScope.continueWatchingSection(rows: List<ContinueWatching>?, actions: YouActions) {
    when {
        rows == null -> {
            item(key = "continue:header") { ShelfHeader(title = "Continue watching") }
            item(key = "continue:loading") { ShelfSkeleton() }
        }
        rows.isEmpty() -> Unit
        else -> {
            item(key = "continue:header") { ShelfHeader(title = "Continue watching") }
            val pairs = rows.chunked(GRID_COLUMNS)
            items(pairs.size, key = { "continue:row:$it" }) { index ->
                ContinueGridRow(pair = pairs[index], thumbFor = actions.thumbFor, onOpen = actions.resume)
            }
        }
    }
}

/** "Saved": the viewer's bookmarked videos; absent when there are none or the list cannot be read. */
private fun LazyListScope.savedSection(saved: LazyPagingItems<FeedItem>, actions: YouActions) {
    val refresh = saved.loadState.refresh
    when {
        refresh is LoadState.Loading && saved.itemCount == 0 -> {
            item(key = "saved:header") { ShelfHeader(title = "Saved") }
            item(key = "saved:loading") { ShelfSkeleton() }
        }
        saved.itemCount == 0 -> Unit
        else -> {
            item(key = "saved:header") { ShelfHeader(title = "Saved") }
            item(key = "saved:shelf") {
                PagedShelf(
                    items = saved,
                    thumbFor = actions.thumbFor,
                    onOpen = actions.openSaved,
                    onMore = actions.more,
                )
            }
        }
    }
}

@Composable
private fun PagedShelf(
    items: LazyPagingItems<FeedItem>,
    thumbFor: (FeedItem) -> VideoThumb,
    onOpen: (FeedItem) -> Unit,
    onMore: (FeedItem) -> Unit,
) {
    LazyRow(
        contentPadding = PaddingValues(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        items(count = items.itemCount, key = items.itemKey { it.id }) { index ->
            val item = items[index] ?: return@items
            ShelfVideoCard(
                item = item,
                thumb = thumbFor(item),
                onClick = { onOpen(item) },
                onMore = { onMore(item) },
            )
        }
    }
}

@Composable
private fun ContinueGridRow(
    pair: List<ContinueWatching>,
    thumbFor: (FeedItem) -> VideoThumb,
    onOpen: (FeedItem) -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        pair.forEach { row ->
            ContinueCard(
                row = row,
                thumb = thumbFor(row.item),
                onClick = { onOpen(row.item) },
                modifier = Modifier.weight(1f),
            )
        }
        // A last odd card keeps its column width rather than stretching across the row.
        if (pair.size < GRID_COLUMNS) Box(modifier = Modifier.weight(1f))
    }
}

@Composable
private fun NoVideosYet(onPost: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l)
            .testTag("tube_you_empty"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Text(
            text = "You haven't posted a video yet",
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textSecondary,
        )
        UsFollowButton(text = "Post a video", onClick = onPost, modifier = Modifier.width(POST_BUTTON_WIDTH))
    }
}

private const val GRID_COLUMNS = 2
private val POST_BUTTON_WIDTH = 140.dp
