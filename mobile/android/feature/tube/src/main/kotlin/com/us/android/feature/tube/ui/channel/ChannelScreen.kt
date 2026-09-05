package com.us.android.feature.tube.ui.channel

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.lazy.staggeredgrid.LazyStaggeredGridScope
import androidx.compose.foundation.lazy.staggeredgrid.LazyVerticalStaggeredGrid
import androidx.compose.foundation.lazy.staggeredgrid.StaggeredGridCells
import androidx.compose.foundation.lazy.staggeredgrid.StaggeredGridItemSpan
import androidx.compose.material3.Icon
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
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.feed.data.offersFollow
import com.us.android.core.feed.ui.more.PostMoreViewModel
import com.us.android.core.model.Channel
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FollowStatus
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.tube.navigation.TubeDestinations
import com.us.android.feature.tube.ui.HeaderGlyph
import com.us.android.feature.tube.ui.TubeMoreHost
import com.us.android.feature.tube.ui.TubePage
import com.us.android.feature.tube.ui.TubeTab
import com.us.android.feature.tube.ui.TubeWordmark
import com.us.android.feature.tube.ui.home.GRID_COLUMNS
import com.us.android.feature.tube.ui.home.GridCard
import com.us.android.feature.tube.ui.home.TubeGridSkeleton
import com.us.android.feature.tube.ui.home.appendFooter
import com.us.android.feature.tube.ui.rememberTubeMoreState
import com.us.android.feature.tube.ui.you.videoCountLabel

/**
 * A channel's page inside Tube (2026-09-05): a back glyph and the wordmark
 * on top, then a banner-less header — avatar, name, `@handle`, About,
 * Follow, the video count — and the channel's videos as the mosaic. The
 * floating bar stays, with nothing lit: this page is not one of its own.
 */
@Composable
fun ChannelScreen(
    destinations: TubeDestinations,
    viewModel: ChannelViewModel = hiltViewModel(),
    more: PostMoreViewModel = hiltViewModel(),
) {
    val header by viewModel.header.collectAsStateWithLifecycle()
    val edges by viewModel.followEdges.collectAsStateWithLifecycle()
    val followBusy by viewModel.followBusy.collectAsStateWithLifecycle()
    val items = viewModel.items.collectAsLazyPagingItems()
    val moreState = rememberTubeMoreState()

    TubePage(
        selected = null,
        onOpenNotifications = destinations.onOpenNotifications,
        onOpenSearch = destinations.onOpenSearch,
        onOpenYou = { destinations.onOpenTab(TubeTab.YOU) },
        onBarAction = destinations::onBarAction,
        topBar = { ChannelTopBar(onBack = destinations.onBack) },
    ) { padding ->
        Box(modifier = Modifier.fillMaxSize()) {
            LazyVerticalStaggeredGrid(
                columns = StaggeredGridCells.Fixed(GRID_COLUMNS),
                modifier = Modifier
                    .fillMaxSize()
                    .testTag("tube_channel_page"),
                contentPadding = PaddingValues(
                    start = UsTheme.spacing.pageHorizontal,
                    end = UsTheme.spacing.pageHorizontal,
                    bottom = padding.calculateBottomPadding() + UsTheme.spacing.xxl,
                ),
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
                verticalItemSpacing = UsTheme.spacing.l,
            ) {
                item(key = "header", span = StaggeredGridItemSpan.FullLine) {
                    ChannelHeader(
                        state = header,
                        edge = edges[viewModel.userId],
                        offersFollow = offersFollow(viewModel.ownUserId, viewModel.userId, edges[viewModel.userId]),
                        busy = followBusy,
                        onFollow = viewModel::follow,
                        onUnfollow = viewModel::unfollow,
                        onRetry = viewModel::load,
                    )
                }
                videos(
                    items = items,
                    thumbFor = viewModel::thumb,
                    onOpen = { item ->
                        viewModel.onOpen(items.itemSnapshotList.items)
                        destinations.onOpenVideo(item.id)
                    },
                    onMore = { item -> moreState.open(item, suggested = false) },
                )
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

/** Back on the left, the wordmark beside it: a pushed page inside the mini-app. */
@Composable
private fun ChannelTopBar(onBack: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .statusBarsPadding()
            .height(TOP_BAR_HEIGHT)
            .padding(horizontal = UsTheme.spacing.m),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        HeaderGlyph(onClick = onBack, description = "Back", modifier = Modifier.testTag("tube_channel_back")) {
            Icon(imageVector = UsIcons.Back, contentDescription = null, tint = UsTheme.extended.textPrimary)
        }
        TubeWordmark()
    }
}

@Composable
private fun ChannelHeader(
    state: ChannelHeaderState,
    edge: FollowStatus?,
    offersFollow: Boolean,
    busy: Boolean,
    onFollow: () -> Unit,
    onUnfollow: () -> Unit,
    onRetry: () -> Unit,
) {
    when (state) {
        ChannelHeaderState.Loading -> UsLoadingState(
            label = "Loading channel",
            modifier = Modifier
                .fillMaxWidth()
                .height(HEADER_LOADING_HEIGHT),
        )
        is ChannelHeaderState.Loaded -> ChannelCard(
            channel = state.channel,
            following = edge == FollowStatus.FOLLOWING || edge == FollowStatus.REQUESTED,
            offersFollow = offersFollow,
            busy = busy,
            onFollow = onFollow,
            onUnfollow = onUnfollow,
        )
        ChannelHeaderState.Missing -> UsEmptyState(
            title = "No channel here",
            detail = "This person hasn't set up a channel.",
            modifier = Modifier.padding(vertical = UsTheme.spacing.xxxxl),
        )
        is ChannelHeaderState.Failed -> UsErrorState(
            message = state.message,
            onRetry = onRetry,
            modifier = Modifier.padding(vertical = UsTheme.spacing.xxxxl),
        )
    }
}

@Composable
private fun ChannelCard(
    channel: Channel,
    following: Boolean,
    offersFollow: Boolean,
    busy: Boolean,
    onFollow: () -> Unit,
    onUnfollow: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = UsTheme.spacing.xxl)
            .testTag("tube_channel_header"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xl),
        ) {
            UsAvatar(
                name = channel.name,
                seed = channel.userId,
                size = UsAvatarSize.Large,
                imageUrl = channel.avatarUrl,
                hasRing = true,
                contentDescription = "${channel.name}'s channel photo",
            )
            Column(
                modifier = Modifier.weight(1f),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
            ) {
                Text(
                    text = channel.name,
                    style = MaterialTheme.typography.titleLarge,
                    fontWeight = FontWeight.Bold,
                    color = UsTheme.extended.textPrimary,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                Text(
                    text = channel.handleForDisplay,
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textMuted,
                    maxLines = 1,
                )
                Text(
                    text = videoCountLabel(channel.videoCount),
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textSecondary,
                )
            }
        }
        if (channel.about.isNotBlank()) {
            Text(
                text = channel.about,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
            )
        }
        when {
            offersFollow -> UsFollowButton(
                onClick = onFollow,
                busy = busy,
                modifier = Modifier.testTag("tube_channel_follow"),
            )
            following -> UsPillButton(
                text = "Following",
                onClick = onUnfollow,
                filled = false,
                busy = busy,
                modifier = Modifier.testTag("tube_channel_following"),
            )
        }
    }
}

private fun LazyStaggeredGridScope.videos(
    items: LazyPagingItems<FeedItem>,
    thumbFor: (FeedItem) -> VideoThumb,
    onOpen: (FeedItem) -> Unit,
    onMore: (FeedItem) -> Unit,
) {
    val refresh = items.loadState.refresh
    val empty = items.itemCount == 0
    val full = StaggeredGridItemSpan.FullLine
    when {
        refresh is LoadState.Loading && empty -> item(key = "loading", span = full) {
            TubeGridSkeleton(modifier = Modifier.padding(vertical = UsTheme.spacing.l))
        }
        refresh is LoadState.Error && empty -> item(key = "error", span = full) {
            UsErrorState(message = "We couldn't load these videos.", onRetry = items::retry)
        }
        refresh is LoadState.NotLoading && empty -> item(key = "empty", span = full) {
            UsEmptyState(title = "No videos yet", modifier = Modifier.padding(vertical = UsTheme.spacing.xxxxl))
        }
        else -> {
            items(count = items.itemCount, key = items.itemKey { it.id }) { index ->
                val item = items[index] ?: return@items
                GridCard(item = item, thumb = thumbFor(item), onClick = { onOpen(item) }, onMore = { onMore(item) })
            }
            appendFooter(items)
        }
    }
}

private val TOP_BAR_HEIGHT = 56.dp
private val HEADER_LOADING_HEIGHT = 160.dp
