package com.us.android.feature.tube.ui.home

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.staggeredgrid.LazyStaggeredGridScope
import androidx.compose.foundation.lazy.staggeredgrid.LazyVerticalStaggeredGrid
import androidx.compose.foundation.lazy.staggeredgrid.StaggeredGridCells
import androidx.compose.foundation.lazy.staggeredgrid.StaggeredGridItemSpan
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextAlign
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.paging.LoadState
import androidx.paging.compose.LazyPagingItems
import androidx.paging.compose.collectAsLazyPagingItems
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.ChannelState
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.feed.ui.channel.CreateChannelSheet
import com.us.android.core.feed.ui.more.PostMoreViewModel
import com.us.android.core.model.FeedItem
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.tube.navigation.TubeDestinations
import com.us.android.feature.tube.ui.TubeChipRail
import com.us.android.feature.tube.ui.TubeMoreHost
import com.us.android.feature.tube.ui.TubePage
import com.us.android.feature.tube.ui.TubeTab
import com.us.android.feature.tube.ui.TubeViewer
import com.us.android.feature.tube.ui.bleed
import com.us.android.feature.tube.ui.rememberTubeMoreState

/**
 * Tube home (Momentum layout, 2026-09-05 — a look that is not YouTube):
 * the wordmark and the search pill on top, then one staggered grid —
 * the channels strip, the featured carousel, "Continue watching", the
 * chip rail, and the two-column mosaic of ranked videos with the Reels
 * panel cut in after four tiles. Pull down to refresh; the next page loads
 * as the grid nears its end. The floating bar rides over the bottom.
 *
 * A tile opens the watch screen; the list the viewer was looking at goes
 * with them ([TubeHomeViewModel.onOpen]) so "Up next" is the rows below.
 * A reel leaves its id in `ReelsEntry` and opens the app's Reels tab. A
 * channel bubble opens the channel's page inside Tube.
 */
@Composable
fun TubeHomeScreen(
    destinations: TubeDestinations,
    viewModel: TubeHomeViewModel = hiltViewModel(),
    more: PostMoreViewModel = hiltViewModel(),
) {
    val items = viewModel.items.collectAsLazyPagingItems()
    val chips by viewModel.chips.collectAsStateWithLifecycle()
    val selected by viewModel.selected.collectAsStateWithLifecycle()
    val shelves by viewModel.shelves.collectAsStateWithLifecycle()
    val ownChannel by viewModel.ownChannel.collectAsStateWithLifecycle()
    val viewer by viewModel.viewer.collectAsStateWithLifecycle()
    val moreState = rememberTubeMoreState()
    var createChannel by rememberSaveable { mutableStateOf(false) }

    val actions = TubeHomeActions(
        videos = TubeVideoActions(
            open = { item ->
                viewModel.onOpen(items.itemSnapshotList.items)
                destinations.onOpenVideo(item.id)
            },
            resume = { item ->
                viewModel.onOpenContinue(item)
                destinations.onOpenVideo(item.id)
            },
            more = { item -> moreState.open(item, suggested = selected.isSuggested()) },
            thumbFor = viewModel::thumb,
        ),
        reels = TubeReelActions(
            open = { item ->
                viewModel.openInReels(item)
                destinations.onOpenReels()
            },
            more = { item -> moreState.open(item, suggested = true) },
        ),
        channels = TubeChannelActions(
            open = destinations.onOpenChannel,
            openYou = { destinations.onOpenTab(TubeTab.YOU) },
            create = { createChannel = true },
        ),
        refresh = {
            items.refresh()
            viewModel.refreshShelves()
        },
    )

    TubePage(
        selected = TubeTab.HOME,
        onOpenNotifications = destinations.onOpenNotifications,
        onOpenSearch = destinations.onOpenSearch,
        onOpenYou = { destinations.onOpenTab(TubeTab.YOU) },
        onBarAction = destinations::onBarAction,
    ) { padding ->
        Box(modifier = Modifier.fillMaxSize()) {
            TubeBody(
                items = items,
                shelves = shelves,
                strip = StripState(ownChannel = ownChannel, viewer = viewer),
                actions = actions,
                bottomPadding = padding,
                rail = { TubeChipRail(chips = chips, selected = selected, onSelect = viewModel::select) },
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

    if (createChannel) {
        CreateChannelSheet(
            onCreated = {
                createChannel = false
                destinations.onOpenTab(TubeTab.YOU)
            },
            onDismiss = { createChannel = false },
        )
    }
}

/** Everything a row on the page can do, grouped by what it acts on, so the grid functions stay short. */
internal class TubeHomeActions(
    val videos: TubeVideoActions,
    val reels: TubeReelActions,
    val channels: TubeChannelActions,
    val refresh: () -> Unit,
)

/** A ranked or half-watched video: open it, resume it, its "more" sheet, its still. */
internal class TubeVideoActions(
    val open: (FeedItem) -> Unit,
    val resume: (FeedItem) -> Unit,
    val more: (FeedItem) -> Unit,
    val thumbFor: (FeedItem) -> VideoThumb,
)

/** A reel in the panel: open it in the app's Reels, or its "more" sheet. */
internal class TubeReelActions(
    val open: (FeedItem) -> Unit,
    val more: (FeedItem) -> Unit,
)

/** A bubble on the strip: a channel, the viewer's own, or the invitation to make one. */
internal class TubeChannelActions(
    val open: (userId: String) -> Unit,
    val openYou: () -> Unit,
    val create: () -> Unit,
)

/** What the channels strip needs beyond the bubbles: the viewer's own channel and face. */
internal data class StripState(
    val ownChannel: ChannelState,
    val viewer: TubeViewer?,
)

/**
 * The loading / error / empty states or the grid. The strip and the
 * shelves are always there — the viewer can change chips while a chip
 * fails — and the states sit under the rail as full-span rows.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun TubeBody(
    items: LazyPagingItems<FeedItem>,
    shelves: TubeShelves,
    strip: StripState,
    actions: TubeHomeActions,
    bottomPadding: PaddingValues,
    rail: @Composable () -> Unit,
) {
    val refresh = items.loadState.refresh
    val sections = tubeSections(
        videoCount = items.itemCount,
        hasChannels = true,
        hasContinueWatching = shelves.continueWatching.isNotEmpty(),
        hasReels = shelves.reels.isNotEmpty(),
    )
    PullToRefreshBox(
        isRefreshing = refresh is LoadState.Loading && items.itemCount > 0,
        onRefresh = actions.refresh,
        modifier = Modifier.fillMaxSize(),
    ) {
        LazyVerticalStaggeredGrid(
            columns = StaggeredGridCells.Fixed(GRID_COLUMNS),
            modifier = Modifier
                .fillMaxSize()
                .testTag("tube_list"),
            contentPadding = PaddingValues(
                start = UsTheme.spacing.pageHorizontal,
                end = UsTheme.spacing.pageHorizontal,
                bottom = bottomPadding.calculateBottomPadding() + UsTheme.spacing.xxl,
            ),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            verticalItemSpacing = UsTheme.spacing.l,
        ) {
            items(
                count = sections.size,
                key = { sections[it].key },
                span = { index ->
                    if (sections[index].fullSpan) StaggeredGridItemSpan.FullLine else StaggeredGridItemSpan.SingleLane
                },
            ) { index ->
                TubeSectionRow(
                    section = sections[index],
                    items = items,
                    shelves = shelves,
                    strip = strip,
                    actions = actions,
                    rail = rail,
                )
            }
            pageState(items)
        }
    }
}

/** One section as a row of the grid. A [TubeSection.Video] reads its row through Paging. */
@Composable
private fun TubeSectionRow(
    section: TubeSection,
    items: LazyPagingItems<FeedItem>,
    shelves: TubeShelves,
    strip: StripState,
    actions: TubeHomeActions,
    rail: @Composable () -> Unit,
) {
    when (section) {
        TubeSection.Channels -> ChannelsStrip(
            own = strip.ownChannel,
            viewer = strip.viewer,
            channels = shelves.channels,
            onOpenChannel = actions.channels.open,
            onOpenYou = actions.channels.openYou,
            onCreateChannel = actions.channels.create,
            modifier = Modifier.bleed(UsTheme.spacing.pageHorizontal),
        )
        is TubeSection.Featured -> FeaturedCarousel(
            items = (0 until section.count).mapNotNull { items[it] },
            thumbFor = actions.videos.thumbFor,
            onOpen = actions.videos.open,
            onMore = actions.videos.more,
            modifier = Modifier.bleed(UsTheme.spacing.pageHorizontal),
        )
        TubeSection.ContinueWatching -> ContinueRow(
            rows = shelves.continueWatching,
            thumbFor = actions.videos.thumbFor,
            onOpen = actions.videos.resume,
            modifier = Modifier.bleed(UsTheme.spacing.pageHorizontal),
        )
        TubeSection.Chips -> Box(modifier = Modifier.bleed(UsTheme.spacing.pageHorizontal)) { rail() }
        TubeSection.Reels -> ReelsPanel(
            reels = shelves.reels,
            thumbFor = actions.videos.thumbFor,
            onOpen = actions.reels.open,
            onMore = actions.reels.more,
            modifier = Modifier.bleed(UsTheme.spacing.pageHorizontal),
        )
        is TubeSection.Video -> items[section.index]?.let { item ->
            GridCard(
                item = item,
                thumb = actions.videos.thumbFor(item),
                onClick = { actions.videos.open(item) },
                onMore = { actions.videos.more(item) },
            )
        }
    }
}

/**
 * The page's own state as full-span rows under the rail: the skeleton on
 * the first load, the error with Retry, the empty message, then the next
 * page's loader or its failure — never a silent end.
 */
internal fun LazyStaggeredGridScope.pageState(items: LazyPagingItems<FeedItem>) {
    val refresh = items.loadState.refresh
    val empty = items.itemCount == 0
    when {
        refresh is LoadState.Loading && empty -> item(key = "state:loading", span = StaggeredGridItemSpan.FullLine) {
            TubeGridSkeleton(modifier = Modifier.bleed(UsTheme.spacing.pageHorizontal))
        }
        refresh is LoadState.Error && empty -> item(key = "state:error", span = StaggeredGridItemSpan.FullLine) {
            UsErrorState(
                message = "We couldn't load videos.",
                onRetry = items::retry,
                modifier = Modifier.padding(vertical = UsTheme.spacing.xxxxl),
            )
        }
        refresh is LoadState.NotLoading && empty -> item(key = "state:empty", span = StaggeredGridItemSpan.FullLine) {
            UsEmptyState(
                title = "No videos yet",
                detail = "Long videos will show up here once someone posts one.",
                modifier = Modifier
                    .padding(vertical = UsTheme.spacing.xxxxl)
                    .testTag("tube_empty"),
            )
        }
        else -> appendFooter(items)
    }
}

/** The next page's loader, or its failure with a retry. */
internal fun LazyStaggeredGridScope.appendFooter(items: LazyPagingItems<FeedItem>) {
    when (val append = items.loadState.append) {
        is LoadState.Loading -> item(key = "append:loading", span = StaggeredGridItemSpan.FullLine) {
            UsLoadingState(
                label = "Loading more videos",
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(UsTheme.spacing.xxl),
            )
        }
        is LoadState.Error -> item(key = "append:error", span = StaggeredGridItemSpan.FullLine) {
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
            .padding(vertical = UsTheme.spacing.l),
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

internal const val GRID_COLUMNS = 2
