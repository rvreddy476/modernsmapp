package com.us.android.feature.tube.ui.home

import androidx.compose.foundation.ExperimentalFoundationApi
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
import com.us.android.feature.tube.ui.rememberTubeMoreState

/**
 * Tube home (redesign, 2026-09-05, from YouTube's home made Momentum):
 * Tube's own header and bar, the chip rail stuck under the header, and
 * one list — the first video large, "Continue watching", one card, the
 * "Shorts" shelf, then the rest as cards. Pull down to refresh; the next
 * page loads as the list nears its end. The viewer's own video sits on
 * top while it posts.
 *
 * A card opens the watch screen; the list the viewer was looking at goes
 * with them ([TubeHomeViewModel.onOpen]) so "Up next" is the rows below.
 * A short leaves its id in `ReelsEntry` and opens the app's Reels tab.
 */
@Composable
fun TubeHomeScreen(
    destinations: TubeDestinations,
    viewModel: TubeHomeViewModel = hiltViewModel(),
    more: PostMoreViewModel = hiltViewModel(),
) {
    val items = viewModel.items.collectAsLazyPagingItems()
    val head by viewModel.head.collectAsStateWithLifecycle()
    val chips by viewModel.chips.collectAsStateWithLifecycle()
    val selected by viewModel.selected.collectAsStateWithLifecycle()
    val shelves by viewModel.shelves.collectAsStateWithLifecycle()
    val moreState = rememberTubeMoreState()

    val actions = TubeHomeActions(
        open = { item ->
            viewModel.onOpen(items.itemSnapshotList.items)
            destinations.onOpenVideo(item.id)
        },
        resume = { item ->
            viewModel.onOpenContinue(item)
            destinations.onOpenVideo(item.id)
        },
        openShort = { item ->
            viewModel.openInReels(item)
            destinations.onOpenReels()
        },
        more = { item -> moreState.open(item, suggested = selected.isSuggested()) },
        moreShort = { item -> moreState.open(item, suggested = true) },
        refresh = {
            items.refresh()
            viewModel.refreshShelves()
        },
    )

    TubePage(
        selected = TubeTab.HOME,
        onOpenNotifications = destinations.onOpenNotifications,
        onOpenSearch = destinations.onOpenSearch,
        onBarAction = destinations::onBarAction,
    ) { padding ->
        Box(modifier = Modifier.fillMaxSize().padding(padding)) {
            TubeBody(
                items = items,
                head = head,
                shelves = shelves,
                viewModel = viewModel,
                actions = actions,
                rail = {
                    TubeChipRail(
                        chips = chips,
                        selected = selected,
                        onSelect = viewModel::select,
                        onOpenExplore = destinations.onOpenExplore,
                    )
                },
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

/** Everything a row on the page can do, in one bundle so the list functions stay short. */
internal class TubeHomeActions(
    val open: (FeedItem) -> Unit,
    val resume: (FeedItem) -> Unit,
    val openShort: (FeedItem) -> Unit,
    val more: (FeedItem) -> Unit,
    val moreShort: (FeedItem) -> Unit,
    val refresh: () -> Unit,
)

/**
 * The rail, then the loading / error / empty states or the list. The rail
 * is always there — the viewer can change chips while a chip fails — and
 * the states sit under it. A head or a shelf always shows the list.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun TubeBody(
    items: LazyPagingItems<FeedItem>,
    head: TubeHead?,
    shelves: TubeShelves,
    viewModel: TubeHomeViewModel,
    actions: TubeHomeActions,
    rail: @Composable () -> Unit,
) {
    val refresh = items.loadState.refresh
    val sections = tubeSections(
        videoCount = items.itemCount,
        hasHead = head != null,
        hasContinueWatching = shelves.continueWatching.isNotEmpty(),
        hasShorts = shelves.shorts.isNotEmpty(),
    )
    when {
        refresh is LoadState.Loading && sections.isEmpty() -> Column {
            rail()
            TubeHomeSkeleton()
        }

        refresh is LoadState.Error && sections.isEmpty() -> Column {
            rail()
            UsErrorState(message = "We couldn't load videos.", onRetry = items::retry)
        }

        refresh is LoadState.NotLoading && sections.isEmpty() -> Column {
            rail()
            UsEmptyState(
                title = "No videos yet",
                detail = "Long videos will show up here once someone posts one.",
                modifier = Modifier.testTag("tube_empty"),
            )
        }

        else -> PullToRefreshBox(
            isRefreshing = refresh is LoadState.Loading,
            onRefresh = actions.refresh,
            modifier = Modifier.fillMaxSize(),
        ) {
            TubeList(
                sections = sections,
                items = items,
                head = head,
                shelves = shelves,
                viewModel = viewModel,
                actions = actions,
                rail = rail,
            )
        }
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun TubeList(
    sections: List<TubeSection>,
    items: LazyPagingItems<FeedItem>,
    head: TubeHead?,
    shelves: TubeShelves,
    viewModel: TubeHomeViewModel,
    actions: TubeHomeActions,
    rail: @Composable () -> Unit,
) {
    LazyColumn(
        modifier = Modifier
            .fillMaxSize()
            .testTag("tube_list"),
        contentPadding = PaddingValues(bottom = UsTheme.spacing.xxl),
    ) {
        stickyHeader(key = "rail") { _ -> rail() }
        items(sections.size, key = { sections[it].key }) { index ->
            TubeSectionRow(
                section = sections[index],
                items = items,
                head = head,
                shelves = shelves,
                viewModel = viewModel,
                actions = actions,
            )
        }
        appendFooter(items)
    }
}

/** One section as a row of the list. A [TubeSection.Video] reads its row through Paging. */
@Composable
private fun TubeSectionRow(
    section: TubeSection,
    items: LazyPagingItems<FeedItem>,
    head: TubeHead?,
    shelves: TubeShelves,
    viewModel: TubeHomeViewModel,
    actions: TubeHomeActions,
) {
    when (section) {
        TubeSection.Head -> HeadRow(head = head, viewModel = viewModel, actions = actions)
        is TubeSection.Hero -> items[section.index]?.let { item ->
            HeroCard(
                item = item,
                thumb = viewModel.thumb(item),
                onClick = { actions.open(item) },
                onMore = { actions.more(item) },
            )
        }
        TubeSection.ContinueWatching -> ContinueWatchingShelf(
            rows = shelves.continueWatching,
            thumbFor = viewModel::thumb,
            onOpen = actions.resume,
        )
        TubeSection.Shorts -> ShortsShelf(
            shorts = shelves.shorts,
            thumbFor = viewModel::thumb,
            onOpen = actions.openShort,
            onMore = actions.moreShort,
        )
        is TubeSection.Video -> items[section.index]?.let { item ->
            VideoCard(
                item = item,
                thumb = viewModel.thumb(item),
                onClick = { actions.open(item) },
                onMore = { actions.more(item) },
            )
        }
    }
}

@Composable
private fun HeadRow(head: TubeHead?, viewModel: TubeHomeViewModel, actions: TubeHomeActions) {
    when (head) {
        is TubeHead.Pending -> PendingVideoCard(
            head = head,
            onRetry = viewModel::retryPublish,
            onDiscard = viewModel::discardPublish,
        )
        is TubeHead.Live -> VideoCard(
            item = head.item,
            thumb = viewModel.thumb(head.item),
            onClick = { actions.open(head.item) },
            onMore = { actions.more(head.item) },
        )
        null -> Unit
    }
}

/** The next page's loader, or its failure with a retry — never a silent end. */
internal fun LazyListScope.appendFooter(items: LazyPagingItems<FeedItem>) {
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
