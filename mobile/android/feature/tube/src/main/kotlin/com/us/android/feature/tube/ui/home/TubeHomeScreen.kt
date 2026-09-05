package com.us.android.feature.tube.ui.home

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.staggeredgrid.LazyStaggeredGridScope
import androidx.compose.foundation.lazy.staggeredgrid.StaggeredGridItemSpan
import androidx.compose.material3.ExperimentalMaterial3Api
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
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.ChannelState
import com.us.android.core.feed.data.VideoThumb
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
import com.us.android.feature.tube.ui.rememberTubeMoreState

/**
 * Tube home (founder, 2026-09-05 — "like YouTube"): the mark and the
 * search pill on top, then one column — the channels strip, the chip rail
 * (which sticks under the search pill once the strip has scrolled away),
 * the first two ranked videos as full-width cards, "Continue watching",
 * the "Reels" panel, then the rest of the ranked videos full width. Pull
 * down to refresh; the next page loads as the list nears its end. Tube's
 * flat bar sits under it all.
 *
 * A card opens the watch screen; the list the viewer was looking at goes
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
        ),
        refresh = {
            items.refresh()
            viewModel.refreshShelves()
        },
    )

    TubePage(selected = TubeTab.HOME, destinations = destinations) { padding ->
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
}

/** Everything a row on the page can do, grouped by what it acts on, so the list functions stay short. */
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

/** A bubble on the strip: a channel, or the viewer's own. */
internal class TubeChannelActions(
    val open: (userId: String) -> Unit,
    val openYou: () -> Unit,
)

/** What the channels strip needs beyond the bubbles: the viewer's own channel and face. */
internal data class StripState(
    val ownChannel: ChannelState,
    val viewer: TubeViewer?,
)

/**
 * The loading / error / empty states or the list. The strip and the
 * shelves are always there — the viewer can change chips while a chip
 * fails — and the states sit under the rail as rows of their own.
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
        // Only followed channels make a strip (founder, 2026-09-05: "we don't
        // have many users") — no strip at all until there is one to show.
        hasChannels = shelves.channels.isNotEmpty(),
        hasContinueWatching = shelves.continueWatching.isNotEmpty(),
        hasReels = shelves.reels.isNotEmpty(),
    )
    PullToRefreshBox(
        isRefreshing = refresh is LoadState.Loading && items.itemCount > 0,
        onRefresh = actions.refresh,
        modifier = Modifier.fillMaxSize(),
    ) {
        LazyColumn(
            modifier = Modifier
                .fillMaxSize()
                .testTag("tube_list"),
            contentPadding = PaddingValues(bottom = bottomPadding.calculateBottomPadding() + UsTheme.spacing.xxl),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxl),
        ) {
            sections(sections, items, shelves, strip, actions, rail)
            pageState(items)
        }
    }
}

/**
 * The sections as rows. The chip rail is the one sticky row: it scrolls
 * up with the strip and then stays under the search pill, on the page's
 * ground so the cards pass beneath it (founder: chips "at the top only,
 * filter like YouTube").
 */
@OptIn(ExperimentalFoundationApi::class)
private fun LazyListScope.sections(
    sections: List<TubeSection>,
    items: LazyPagingItems<FeedItem>,
    shelves: TubeShelves,
    strip: StripState,
    actions: TubeHomeActions,
    rail: @Composable () -> Unit,
) {
    sections.forEach { section ->
        when (section) {
            TubeSection.Chips -> stickyHeader(key = section.key) { _ ->
                Box(
                    modifier = Modifier
                        .fillMaxWidth()
                        .background(MaterialTheme.colorScheme.background),
                ) {
                    rail()
                }
            }
            else -> item(key = section.key) {
                TubeSectionRow(section = section, items = items, shelves = shelves, strip = strip, actions = actions)
            }
        }
    }
}

/** One section as a row of the list. A [TubeSection.Video] reads its row through Paging. */
@Composable
private fun TubeSectionRow(
    section: TubeSection,
    items: LazyPagingItems<FeedItem>,
    shelves: TubeShelves,
    strip: StripState,
    actions: TubeHomeActions,
) {
    when (section) {
        TubeSection.Channels -> ChannelsStrip(
            own = strip.ownChannel,
            viewer = strip.viewer,
            channels = shelves.channels,
            onOpenChannel = actions.channels.open,
            onOpenYou = actions.channels.openYou,
        )
        TubeSection.ContinueWatching -> ContinueRow(
            rows = shelves.continueWatching,
            thumbFor = actions.videos.thumbFor,
            onOpen = actions.videos.resume,
        )
        TubeSection.Reels -> ReelsPanel(
            reels = shelves.reels,
            thumbFor = actions.videos.thumbFor,
            onOpen = actions.reels.open,
            onMore = actions.reels.more,
        )
        is TubeSection.Video -> items[section.index]?.let { item ->
            VideoCard(
                item = item,
                thumb = actions.videos.thumbFor(item),
                onClick = { actions.videos.open(item) },
                onMore = { actions.videos.more(item) },
            )
        }
        // The rail is placed by [sections] as the sticky row; nothing else lands here.
        TubeSection.Chips -> Unit
    }
}

/**
 * The page's own state as rows under the rail: the skeleton on the first
 * load, the error with Retry, the empty message, then the next page's
 * loader or its failure — never a silent end.
 */
private fun LazyListScope.pageState(items: LazyPagingItems<FeedItem>) {
    val refresh = items.loadState.refresh
    val empty = items.itemCount == 0
    when {
        refresh is LoadState.Loading && empty -> item(key = "state:loading") { TubeListSkeleton() }
        refresh is LoadState.Error && empty -> item(key = "state:error") {
            UsErrorState(
                message = "We couldn't load videos.",
                onRetry = items::retry,
                modifier = Modifier.padding(vertical = UsTheme.spacing.xxxxl),
            )
        }
        refresh is LoadState.NotLoading && empty -> item(key = "state:empty") {
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

/** The next page's loader, or its failure with a retry — the list's version. */
private fun LazyListScope.appendFooter(items: LazyPagingItems<FeedItem>) {
    when (items.loadState.append) {
        is LoadState.Loading -> item(key = "append:loading") { AppendLoading() }
        is LoadState.Error -> item(key = "append:error") {
            // The list has no gutter of its own; the mosaic pages' grids do.
            AppendError(
                onRetry = items::retry,
                modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal),
            )
        }
        is LoadState.NotLoading -> Unit
    }
}

/** The next page's loader, or its failure with a retry — for the mosaic pages, spanning both columns. */
internal fun LazyStaggeredGridScope.appendFooter(items: LazyPagingItems<FeedItem>) {
    when (items.loadState.append) {
        is LoadState.Loading -> item(key = "append:loading", span = StaggeredGridItemSpan.FullLine) {
            AppendLoading()
        }
        is LoadState.Error -> item(key = "append:error", span = StaggeredGridItemSpan.FullLine) {
            AppendError(onRetry = items::retry)
        }
        is LoadState.NotLoading -> Unit
    }
}

@Composable
private fun AppendLoading() {
    UsLoadingState(
        label = "Loading more videos",
        modifier = Modifier
            .fillMaxWidth()
            .padding(UsTheme.spacing.xxl),
    )
}

@Composable
private fun AppendError(onRetry: () -> Unit, modifier: Modifier = Modifier) {
    Column(
        modifier = modifier
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
