package com.us.android.feature.tube.ui.channel

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.lazy.staggeredgrid.items
import androidx.compose.ui.layout.ContentScale
import coil3.compose.AsyncImage
import com.us.android.core.ui.formatCount
import com.us.android.feature.tube.ui.collections.CollectionRow
import com.us.android.feature.tube.ui.home.TubeListSkeleton
import androidx.compose.foundation.lazy.staggeredgrid.LazyStaggeredGridScope
import androidx.compose.foundation.lazy.staggeredgrid.LazyVerticalStaggeredGrid
import androidx.compose.foundation.lazy.staggeredgrid.StaggeredGridCells
import androidx.compose.foundation.lazy.staggeredgrid.StaggeredGridItemSpan
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
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
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.ui.action.ProfileActionButton
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.feed.data.offersSubscribe
import com.us.android.core.feed.ui.more.PostMoreViewModel
import com.us.android.core.model.Channel
import com.us.android.core.model.ChannelSubscription
import com.us.android.core.model.FeedItem
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.tube.navigation.TubeDestinations
import com.us.android.feature.tube.ui.TubeMoreHost
import com.us.android.feature.tube.ui.TubePage
import com.us.android.feature.tube.ui.home.GRID_COLUMNS
import com.us.android.feature.tube.ui.home.GridCard
import com.us.android.feature.tube.ui.home.TubeGridSkeleton
import com.us.android.feature.tube.ui.home.appendFooter
import com.us.android.feature.tube.ui.rememberTubeMoreState
import com.us.android.feature.tube.ui.subscriberCountLabel
import com.us.android.feature.tube.ui.you.videoCountLabel

/**
 * A channel's page inside Tube (2026-09-05; subscriptions 2026-09-12): a
 * back glyph and the wordmark on top, then a banner-less header (avatar,
 * name, `@handle`, the subscriber and video counts, About, Subscribe) and
 * the channel's videos as the mosaic. Subscribe is the page's one
 * relationship control (founder): it is follow plus notify in one button,
 * and once subscribed it becomes "Subscribed" beside a bell that mutes or
 * unmutes this channel's uploads. The floating bar stays, with nothing
 * lit: this page is not one of its own.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ChannelScreen(
    destinations: TubeDestinations,
    viewModel: ChannelViewModel = hiltViewModel(),
    more: PostMoreViewModel = hiltViewModel(),
) {
    val header by viewModel.header.collectAsStateWithLifecycle()
    val subscription by viewModel.subscription.collectAsStateWithLifecycle()
    val subscriberCount by viewModel.subscriberCount.collectAsStateWithLifecycle()
    val subscribeBusy by viewModel.subscribeBusy.collectAsStateWithLifecycle()
    val videoItems = viewModel.items.collectAsLazyPagingItems()
    val appearances = viewModel.appearances.collectAsLazyPagingItems()
    val membersItems = viewModel.membersItems.collectAsLazyPagingItems()
    val shortsItems = viewModel.shortsItems.collectAsLazyPagingItems()
    val liveItems = viewModel.liveItems.collectAsLazyPagingItems()
    val postItems = viewModel.postItems.collectAsLazyPagingItems()
    val collections by viewModel.collections.collectAsStateWithLifecycle()
    val tab by viewModel.tab.collectAsStateWithLifecycle()
    val tabs by viewModel.tabs.collectAsStateWithLifecycle()
    val shown = tab.takeIf { it in tabs } ?: ChannelTab.VIDEOS
    val items = when (shown) {
        ChannelTab.VIDEOS, ChannelTab.COLLECTIONS -> videoItems
        ChannelTab.SHORTS -> shortsItems
        ChannelTab.LIVE -> liveItems
        ChannelTab.POSTS -> postItems
        ChannelTab.MEMBERS -> membersItems
        ChannelTab.APPEARS_IN -> appearances
    }
    val moreState = rememberTubeMoreState()

    TubePage(selected = null, destinations = destinations, onBack = destinations.onBack) { padding ->
        // Pull to refresh (UI audit 2026-10-21): the header and the videos are read again.
        PullToRefreshBox(
            isRefreshing = items.loadState.refresh is LoadState.Loading && items.itemCount > 0,
            onRefresh = {
                viewModel.load()
                if (shown == ChannelTab.COLLECTIONS) viewModel.loadCollections() else items.refresh()
            },
            modifier = Modifier.fillMaxSize(),
        ) {
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
                        subscription = subscription,
                        subscriberCount = subscriberCount,
                        offersSubscribe = offersSubscribe(viewModel.ownUserId, viewModel.userId, subscription),
                        busy = subscribeBusy,
                        onSubscribe = viewModel::subscribe,
                        onUnsubscribe = viewModel::unsubscribe,
                        onToggleNotify = viewModel::toggleNotify,
                        onRetry = viewModel::load,
                    )
                }
                // "Appears in" (video credits, 2026-10-23) and "Members" (paid
                // memberships, 2026-10-23): only when there is something behind them.
                if (tabs.size > 1) {
                    item(key = "tabs", span = StaggeredGridItemSpan.FullLine) {
                        ChannelTabs(tabs = tabs, selected = shown, onSelect = viewModel::selectTab)
                    }
                }
                when (shown) {
                    ChannelTab.COLLECTIONS -> collectionRows(
                        state = collections,
                        onOpen = destinations.onOpenCollection,
                        onRetry = viewModel::loadCollections,
                    )
                    ChannelTab.POSTS -> posts(items = items, thumbFor = viewModel::thumb, onOpen = { destinations.onOpenPost(it.id) })
                    else -> videos(
                        items = items,
                        emptyTitle = channelEmptyTitle(shown),
                        thumbFor = viewModel::thumb,
                        onOpen = { item ->
                            if (shown == ChannelTab.SHORTS) {
                                viewModel.openShort(item.id)
                                destinations.onOpenReels()
                            } else {
                                viewModel.onOpen(items.itemSnapshotList.items)
                                destinations.onOpenVideo(item.id)
                            }
                        },
                        onMore = { item -> moreState.open(item, suggested = false) },
                    )
                }
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

@Composable
@Suppress("LongParameterList") // One callback per control on the header; a holder would hide, not help.
private fun ChannelHeader(
    state: ChannelHeaderState,
    subscription: ChannelSubscription?,
    subscriberCount: Int,
    offersSubscribe: Boolean,
    busy: Boolean,
    onSubscribe: () -> Unit,
    onUnsubscribe: () -> Unit,
    onToggleNotify: () -> Unit,
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
            subscription = subscription,
            subscriberCount = subscriberCount,
            offersSubscribe = offersSubscribe,
            busy = busy,
            onSubscribe = onSubscribe,
            onUnsubscribe = onUnsubscribe,
            onToggleNotify = onToggleNotify,
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
@Suppress("LongParameterList") // The header's controls, one callback each.
private fun ChannelCard(
    channel: Channel,
    subscription: ChannelSubscription?,
    subscriberCount: Int,
    offersSubscribe: Boolean,
    busy: Boolean,
    onSubscribe: () -> Unit,
    onUnsubscribe: () -> Unit,
    onToggleNotify: () -> Unit,
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
                    text = "${subscriberCountLabel(subscriberCount)} · ${videoCountLabel(channel.videoCount)}",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textSecondary,
                    modifier = Modifier.testTag("tube_channel_counts"),
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
        // The same control the watch screen's author row draws, so the two
        // surfaces cannot disagree about what a subscription looks like.
        SubscribeControl(
            channelName = channel.name,
            subscription = subscription,
            offersSubscribe = offersSubscribe,
            busy = busy,
            onSubscribe = onSubscribe,
            onUnsubscribe = onUnsubscribe,
            onToggleNotify = onToggleNotify,
            tagPrefix = "tube_channel",
        )
        // The channel's Action button (2026-10-23), beside Subscribe: a viewer sees only a live one.
        ProfileActionButton(action = channel.action, modifier = Modifier.fillMaxWidth())
    }
}

/** What an empty tab says. */
internal fun channelEmptyTitle(tab: ChannelTab): String = when (tab) {
    ChannelTab.APPEARS_IN -> "Not credited in any videos yet"
    ChannelTab.MEMBERS -> "No members-only videos yet"
    ChannelTab.SHORTS -> "No shorts yet"
    ChannelTab.LIVE -> "No past live streams yet"
    ChannelTab.COLLECTIONS -> "No collections yet"
    ChannelTab.POSTS -> "No posts yet"
    ChannelTab.VIDEOS -> "No videos yet"
}

/** Videos · Shorts · Live · … — glass pills, the selected one white with navy text, sideways on a phone. */
@Composable
private fun ChannelTabs(tabs: List<ChannelTab>, selected: ChannelTab, onSelect: (ChannelTab) -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .horizontalScroll(rememberScrollState())
            .testTag("tube_channel_tabs"),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        tabs.forEach { tab ->
            val active = tab == selected
            val shape = RoundedCornerShape(UsTheme.radii.full)
            Box(
                contentAlignment = Alignment.Center,
                modifier = Modifier
                    .height(TAB_HEIGHT)
                    .clip(shape)
                    .background(if (active) UsTheme.extended.selectedPill else UsTheme.extended.glassBg)
                    .border(TAB_HAIRLINE, if (active) UsTheme.extended.selectedPill else UsTheme.extended.glassBorder, shape)
                    .clickable(
                        interactionSource = remember { MutableInteractionSource() },
                        indication = null,
                    ) { onSelect(tab) }
                    .semantics {
                        role = Role.Tab
                        this.selected = active
                    }
                    .padding(horizontal = UsTheme.spacing.xl)
                    .testTag("tube_channel_tab:${tab.name.lowercase()}"),
            ) {
                Text(
                    text = tab.label,
                    style = MaterialTheme.typography.labelLarge,
                    fontWeight = FontWeight.SemiBold,
                    color = if (active) UsTheme.extended.onSelectedPill else UsTheme.extended.textPrimary,
                    maxLines = 1,
                )
            }
        }
    }
}

private fun LazyStaggeredGridScope.videos(
    items: LazyPagingItems<FeedItem>,
    emptyTitle: String,
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
            UsEmptyState(title = emptyTitle, modifier = Modifier.padding(vertical = UsTheme.spacing.xxxxl))
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

/** The Collections tab: one row per collection, the whole width. */
private fun LazyStaggeredGridScope.collectionRows(
    state: ChannelCollectionsState,
    onOpen: (String) -> Unit,
    onRetry: () -> Unit,
) {
    val full = StaggeredGridItemSpan.FullLine
    when (state) {
        ChannelCollectionsState.Loading -> item(key = "collections_loading", span = full) {
            TubeListSkeleton(modifier = Modifier.padding(vertical = UsTheme.spacing.l))
        }
        is ChannelCollectionsState.Failed -> item(key = "collections_error", span = full) {
            UsErrorState(message = state.message, onRetry = onRetry)
        }
        is ChannelCollectionsState.Loaded -> if (state.collections.isEmpty()) {
            item(key = "collections_empty", span = full) {
                UsEmptyState(title = channelEmptyTitle(ChannelTab.COLLECTIONS), modifier = Modifier.padding(vertical = UsTheme.spacing.xxxxl))
            }
        } else {
            items(state.collections, key = { "collection:" + it.id }, span = { full }) { collection ->
                CollectionRow(collection = collection, onClick = { onOpen(collection.id) })
            }
        }
    }
}

/** The Posts tab: each post as a tile, its picture when it has one, else its words. */
private fun LazyStaggeredGridScope.posts(
    items: LazyPagingItems<FeedItem>,
    thumbFor: (FeedItem) -> VideoThumb,
    onOpen: (FeedItem) -> Unit,
) {
    val refresh = items.loadState.refresh
    val empty = items.itemCount == 0
    val full = StaggeredGridItemSpan.FullLine
    when {
        refresh is LoadState.Loading && empty -> item(key = "posts_loading", span = full) {
            TubeGridSkeleton(modifier = Modifier.padding(vertical = UsTheme.spacing.l))
        }
        refresh is LoadState.Error && empty -> item(key = "posts_error", span = full) {
            UsErrorState(message = "We couldn't load these posts.", onRetry = items::retry)
        }
        refresh is LoadState.NotLoading && empty -> item(key = "posts_empty", span = full) {
            UsEmptyState(title = channelEmptyTitle(ChannelTab.POSTS), modifier = Modifier.padding(vertical = UsTheme.spacing.xxxxl))
        }
        else -> {
            items(count = items.itemCount, key = items.itemKey { "post:" + it.id }) { index ->
                val item = items[index] ?: return@items
                PostTile(item = item, imageUrl = thumbFor(item).url, onClick = { onOpen(item) })
            }
            appendFooter(items)
        }
    }
}

@Composable
private fun PostTile(item: FeedItem, imageUrl: String?, onClick: () -> Unit) {
    val shape = RoundedCornerShape(UsTheme.radii.medium)
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clip(shape)
            .background(UsTheme.extended.glassBg, shape)
            .border(TAB_HAIRLINE, UsTheme.extended.glassBorder, shape)
            .clickable(role = Role.Button, onClick = onClick)
            .testTag("tube_channel_post:" + item.id),
    ) {
        if (imageUrl != null) {
            AsyncImage(
                model = imageUrl,
                contentDescription = null,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxWidth().aspectRatio(1f),
            )
        }
        val words = item.title.ifBlank { item.text }
        if (words.isNotBlank()) {
            Text(
                text = words,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textPrimary,
                maxLines = POST_LINES,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.padding(UsTheme.spacing.l),
            )
        }
        Text(
            text = formatCount(item.counts.likes) + " likes · " + formatCount(item.counts.comments) + " comments",
            style = MaterialTheme.typography.labelSmall,
            color = UsTheme.extended.textMuted,
            modifier = Modifier.padding(start = UsTheme.spacing.l, end = UsTheme.spacing.l, bottom = UsTheme.spacing.l),
        )
    }
}

private const val POST_LINES = 6
private val HEADER_LOADING_HEIGHT = 160.dp
private val TAB_HEIGHT = 36.dp
private val TAB_HAIRLINE = 1.dp
