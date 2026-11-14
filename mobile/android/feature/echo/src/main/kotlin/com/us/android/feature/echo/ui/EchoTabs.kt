package com.us.android.feature.echo.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.material3.pulltorefresh.PullToRefreshDefaults
import androidx.compose.material3.pulltorefresh.rememberPullToRefreshState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsEchoType
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.Notification
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.echo.data.Timeline
import com.us.android.feature.echo.data.Trend
import com.us.android.feature.echo.data.compactCount
import com.us.android.feature.echo.data.shortAge
import kotlinx.coroutines.launch
import java.time.Instant

/**
 * One of Echo's tabbed pages under the shell's hide-on-scroll top bar: [top]
 * is the bar's first row (avatar and title, mark or search), [tabs] the
 * swipeable pages under it. Each page gets the padding that keeps its first
 * and last rows clear of both bars.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun TabbedPage(
    bars: EchoBarsState,
    tabs: List<String>,
    initialPage: Int,
    onPage: (Int) -> Unit,
    tagPrefix: String,
    top: @Composable RowScope.() -> Unit,
    page: @Composable (index: Int, padding: PaddingValues) -> Unit,
) {
    val pager = rememberPagerState(initialPage = initialPage) { tabs.size }
    val scope = rememberCoroutineScope()
    val density = LocalDensity.current
    LaunchedEffect(pager.currentPage) { onPage(pager.currentPage) }
    val padding = with(density) { PaddingValues(top = bars.headerHeight.toDp(), bottom = bars.bottomHeight.toDp()) }
    // Clipped, so the header slides away behind the page's top edge instead of
    // over the status bar above it.
    Box(modifier = Modifier.fillMaxSize().clipToBounds()) {
        HorizontalPager(state = pager, modifier = Modifier.fillMaxSize(), beyondViewportPageCount = 1) { index ->
            page(index, padding)
        }
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .onSizeChanged { bars.headerHeight = it.height }
                .graphicsLayer { translationY = -bars.hidden * size.height }
                .background(UsTheme.extended.bgCanvas),
        ) {
            Row(
                modifier = Modifier.fillMaxWidth().height(TOP_ROW).padding(horizontal = UsTheme.spacing.xxl),
                verticalAlignment = Alignment.CenterVertically,
                content = top,
            )
            PillTabs(
                labels = tabs,
                selected = pager.currentPage,
                tagPrefix = tagPrefix,
                onSelect = { index -> scope.launch { pager.animateScrollToPage(index) } },
                modifier = Modifier.padding(bottom = UsTheme.spacing.m),
            )
        }
    }
}

/**
 * Echo's tabs as pills (echo-look-v3): the selected one on the ember tint,
 * the rest outlined. Scrolls sideways when there are more than fit.
 */
@Composable
internal fun PillTabs(
    labels: List<String>,
    selected: Int,
    tagPrefix: String,
    onSelect: (Int) -> Unit,
    modifier: Modifier = Modifier,
    /** Each tab's test tag; by default `<tagPrefix>_tab:<index>`. */
    tagOf: (Int) -> String = { "${tagPrefix}_tab:$it" },
) {
    val echo = UsTheme.extended.echo
    Row(
        modifier = modifier
            .fillMaxWidth()
            .horizontalScroll(rememberScrollState())
            .padding(horizontal = UsTheme.spacing.xxl, vertical = UsTheme.spacing.xs),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        labels.forEachIndexed { index, label ->
            val lit = index == selected
            val shape = RoundedCornerShape(UsTheme.radii.full)
            Text(
                text = label,
                style = MaterialTheme.typography.labelLarge,
                fontWeight = if (lit) FontWeight.Bold else FontWeight.Medium,
                color = if (lit) echo.text else UsTheme.extended.textSecondary,
                modifier = Modifier
                    .clip(shape)
                    .then(if (lit) Modifier.background(echo.tint) else Modifier)
                    .border(1.dp, if (lit) echo.border else UsTheme.extended.borderMedium, shape)
                    .selectable(selected = lit, role = Role.Tab, onClick = { onSelect(index) })
                    .padding(horizontal = UsTheme.spacing.xxl, vertical = UsTheme.spacing.s)
                    .testTag(tagOf(index)),
            )
        }
    }
}

/** Pull to refresh with its spinner under the top bar, not behind it. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun EchoRefresh(refreshing: Boolean, onRefresh: () -> Unit, top: Dp, content: @Composable () -> Unit) {
    val state = rememberPullToRefreshState()
    PullToRefreshBox(
        isRefreshing = refreshing,
        onRefresh = onRefresh,
        state = state,
        modifier = Modifier.fillMaxSize(),
        indicator = {
            PullToRefreshDefaults.Indicator(
                state = state,
                isRefreshing = refreshing,
                modifier = Modifier.align(Alignment.TopCenter).padding(top = top),
            )
        },
    ) { content() }
}

/** Home: For you and Following, swipeable, X's order; Following selected on first open. */
@Composable
internal fun HomeTab(
    viewModel: EchoHomeViewModel,
    destinations: EchoDestinations,
    bars: EchoBarsState,
    avatar: @Composable () -> Unit,
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val urls by viewModel.mediaUrls.collectAsStateWithLifecycle()
    val actions = viewModel.actions
    val callbacks = rememberNoteCallbacks(
        actions = actions,
        destinations = destinations,
        onReply = { destinations.onOpenNote(it.id) },
        onQuote = viewModel::quote,
    )
    TabbedPage(
        bars = bars,
        tabs = Timeline.entries.map { it.label },
        initialPage = state.timeline.ordinal,
        onPage = { viewModel.select(Timeline.entries[it]) },
        tagPrefix = "echo_home",
        top = {
            avatar()
            Box(modifier = Modifier.weight(1f), contentAlignment = Alignment.Center) { EchoMark() }
            Box(modifier = Modifier.size(UsAvatarSize.Small.diameter))
        },
    ) { index, padding ->
        val timeline = Timeline.entries[index]
        val feed by viewModel.feed(timeline).collectAsStateWithLifecycle()
        EchoRefresh(refreshing = feed.refreshing, onRefresh = viewModel::refresh, top = padding.calculateTopPadding()) {
            NoteList(
                feed = feed,
                mediaUrls = urls,
                callbacks = callbacks,
                emptyTitle = if (timeline == Timeline.FOLLOWING) "Welcome to Echo" else "Nothing here yet",
                emptyDetail = if (timeline == Timeline.FOLLOWING) {
                    "Notes from accounts you follow show up here."
                } else {
                    "Notes picked for you show up here."
                },
                onRetry = viewModel::retry,
                onLoadMore = viewModel::loadMore,
                // Room under the last card for the "Write a note" pill above the bottom bar.
                contentPadding = PaddingValues(
                    top = padding.calculateTopPadding(),
                    bottom = padding.calculateBottomPadding() + WRITE_PILL_ROOM,
                ),
                onSeen = actions::seen,
                modifier = Modifier.testTag("echo_timeline:${timeline.name.lowercase()}"),
            )
        }
    }
    NoteActionsHost(actions = actions, destinations = destinations, onQuote = viewModel::quote)
    val message = remember(state.message) { state.message?.let { UsMessage(it) } }
    EchoMessageHost(message = message, onDismiss = viewModel::messageShown)
}

/** Explore: the search box, then Trending and Latest. */
@Composable
internal fun ExploreTab(
    destinations: EchoDestinations,
    bars: EchoBarsState,
    avatar: @Composable () -> Unit,
    viewModel: ExploreViewModel = hiltViewModel(),
) {
    val trends by viewModel.trends.collectAsStateWithLifecycle()
    val latest by viewModel.latest.collectAsStateWithLifecycle()
    val urls by viewModel.mediaUrls.collectAsStateWithLifecycle()
    val callbacks = rememberNoteCallbacks(
        actions = viewModel.actions,
        destinations = destinations,
        onReply = { destinations.onOpenNote(it.id) },
        onQuote = viewModel.gate::quote,
    )
    TabbedPage(
        bars = bars,
        tabs = listOf(TALKED_ABOUT_NOW, "Latest"),
        initialPage = 0,
        onPage = { if (it == 1) viewModel.showLatest() },
        tagPrefix = "echo_explore",
        top = {
            avatar()
            SearchField(
                onSearch = destinations.onOpenSearch,
                modifier = Modifier.weight(1f).padding(start = UsTheme.spacing.l)
            )
        },
    ) { index, padding ->
        if (index == 0) {
            TrendList(
                trends = trends,
                padding = padding,
                onOpen = { destinations.onOpenHashtag(it.tag) },
                onRetry = viewModel::loadTrends,
            )
        } else {
            EchoRefresh(
                refreshing = latest.refreshing,
                onRefresh = viewModel::refreshLatest,
                top = padding.calculateTopPadding()
            ) {
                NoteList(
                    feed = latest,
                    mediaUrls = urls,
                    callbacks = callbacks,
                    emptyTitle = "Nothing new yet",
                    emptyDetail = "The latest notes on Echo show up here.",
                    onRetry = viewModel::retryLatest,
                    onLoadMore = viewModel::loadMoreLatest,
                    contentPadding = padding,
                    onSeen = viewModel.actions::seen,
                )
            }
        }
    }
    NoteActionsHost(actions = viewModel.actions, destinations = destinations, onQuote = viewModel.gate::quote)
    ComposeGateHost(gate = viewModel.gate, destinations = destinations)
}

/** X's pill search box; the keyboard's search key opens the results. */
@Composable
internal fun SearchField(onSearch: (String) -> Unit, modifier: Modifier = Modifier, initial: String = "") {
    var query by rememberSaveable { mutableStateOf(initial) }
    Row(
        modifier = modifier
            .height(SEARCH_HEIGHT)
            .background(UsTheme.extended.fillSubtle, RoundedCornerShape(UsTheme.radii.full))
            .padding(horizontal = UsTheme.spacing.l),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(
            UsIcons.Search,
            contentDescription = null,
            tint = UsTheme.extended.textTertiary,
            modifier = Modifier.size(SEARCH_ICON)
        )
        Box(modifier = Modifier.weight(1f).padding(start = UsTheme.spacing.m)) {
            if (query.isEmpty()) {
                Text("Search", style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textTertiary)
            }
            BasicTextField(
                value = query,
                onValueChange = { query = it },
                singleLine = true,
                textStyle = MaterialTheme.typography.bodyMedium.copy(color = UsTheme.extended.textPrimary),
                cursorBrush = SolidColor(UsTheme.extended.accent),
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
                keyboardActions = KeyboardActions(onSearch = { if (query.isNotBlank()) onSearch(query.trim()) }),
                modifier = Modifier.fillMaxWidth().testTag("echo_search_field"),
            )
        }
    }
}

@Composable
private fun TrendList(
    trends: ExploreViewModel.TrendsState,
    padding: PaddingValues,
    onOpen: (Trend) -> Unit,
    onRetry: () -> Unit
) {
    LazyColumn(
        modifier = Modifier.fillMaxSize().testTag("echo_trends"),
        contentPadding = padding,
        verticalArrangement = Arrangement.spacedBy(CARD_GAP),
    ) {
        when {
            trends.loading && trends.items.isEmpty() -> item {
                UsLoadingState(
                    modifier = Modifier.fillParentMaxHeight(STATE_HEIGHT)
                )
            }
            trends.error != null -> item {
                UsErrorState(
                    message = trends.error,
                    onRetry = onRetry,
                    modifier = Modifier.fillParentMaxHeight(STATE_HEIGHT)
                )
            }
            trends.items.isEmpty() -> item {
                UsEmptyState(
                    title = "No trends yet",
                    detail = "Hashtags many people post about today show up here.",
                    modifier = Modifier.fillParentMaxHeight(STATE_HEIGHT),
                )
            }
            else -> items(trends.items, key = { it.tag }) { trend ->
                EchoCard(
                    modifier = Modifier.padding(horizontal = UsTheme.spacing.l).testTag("echo_trend:${trend.tag}"),
                    onClick = { onOpen(trend) },
                ) {
                    Text(
                        text = "$TALKED_ABOUT_NOW · ${trend.rank}",
                        style = MaterialTheme.typography.labelMedium,
                        color = UsTheme.extended.echo.text,
                    )
                    Text(
                        text = "#${trend.tag}",
                        style = UsEchoType.note,
                        color = UsTheme.extended.textPrimary,
                        modifier = Modifier.padding(top = UsTheme.spacing.xs),
                    )
                    Text(
                        text = if (trend.noteCount == 1L) {
                            "1 note"
                        } else {
                            "${compactCount(trend.noteCount).ifEmpty { "0" }} notes"
                        },
                        style = MaterialTheme.typography.bodySmall,
                        color = UsTheme.extended.textTertiary,
                    )
                }
            }
        }
    }
}

/** Notifications: All and Mentions, the app's own inbox; an Echo row opens its post. */
@Composable
internal fun NotificationsTab(
    destinations: EchoDestinations,
    bars: EchoBarsState,
    avatar: @Composable () -> Unit,
    viewModel: EchoNotificationsViewModel = hiltViewModel(),
) {
    TabbedPage(
        bars = bars,
        tabs = EchoNotificationsTab.entries.map { it.label },
        initialPage = 0,
        onPage = { viewModel.select(EchoNotificationsTab.entries[it]) },
        tagPrefix = "echo_notifications",
        top = {
            avatar()
            Text(
                text = "Notifications",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier.weight(1f).padding(start = UsTheme.spacing.xxl),
            )
        },
    ) { index, padding ->
        val tab = EchoNotificationsTab.entries[index]
        val feed by viewModel.feed(tab).collectAsStateWithLifecycle()
        EchoRefresh(
            refreshing = feed.loading && feed.loaded,
            onRefresh = viewModel::refresh,
            top = padding.calculateTopPadding()
        ) {
            NotificationList(
                feed = feed,
                padding = padding,
                emptyTitle = if (tab == EchoNotificationsTab.MENTIONS) {
                    "Nothing to see here yet"
                } else {
                    "Join the conversation"
                },
                emptyDetail = if (tab == EchoNotificationsTab.MENTIONS) {
                    "When someone mentions you, you'll find it here."
                } else {
                    "Hearts, echoes, replies and follows show up here."
                },
                onOpen = { n ->
                    viewModel.opened(n)
                    destinations.onOpenNotification(n.target)
                },
                onRetry = viewModel::refresh,
                onLoadMore = viewModel::loadMore,
            )
        }
    }
}

@Composable
@Suppress("LongParameterList")
private fun NotificationList(
    feed: NotificationsFeed,
    padding: PaddingValues,
    emptyTitle: String,
    emptyDetail: String,
    onOpen: (Notification) -> Unit,
    onRetry: () -> Unit,
    onLoadMore: () -> Unit,
) {
    val state = rememberLazyListState()
    val now = remember(feed.items) { Instant.now() }
    val nearEnd by remember {
        androidx.compose.runtime.derivedStateOf {
            val last = state.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: 0
            last >= state.layoutInfo.totalItemsCount - LOAD_AHEAD
        }
    }
    LaunchedEffect(nearEnd, feed.cursor) { if (nearEnd) onLoadMore() }
    LazyColumn(
        state = state,
        modifier = Modifier.fillMaxSize().testTag("echo_notifications"),
        contentPadding = padding,
        verticalArrangement = Arrangement.spacedBy(CARD_GAP),
    ) {
        when {
            !feed.loaded && feed.loading -> item {
                UsLoadingState(
                    modifier = Modifier.fillParentMaxHeight(STATE_HEIGHT)
                )
            }
            feed.error != null && feed.items.isEmpty() -> item {
                UsErrorState(
                    message = feed.error,
                    onRetry = onRetry,
                    modifier = Modifier.fillParentMaxHeight(STATE_HEIGHT)
                )
            }
            feed.loaded && feed.items.isEmpty() -> item {
                UsEmptyState(
                    title = emptyTitle,
                    detail = emptyDetail,
                    modifier = Modifier.fillParentMaxHeight(STATE_HEIGHT)
                )
            }
            else -> items(feed.items, key = { it.id }) { n ->
                EchoCard(
                    modifier = Modifier.padding(horizontal = UsTheme.spacing.l).testTag("echo_notification:${n.id}"),
                    accent = !n.isRead,
                    onClick = { onOpen(n) },
                ) {
                    Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
                        UsAvatar(name = n.actorName, seed = n.actorUserId, size = UsAvatarSize.Small)
                        Column(modifier = Modifier.weight(1f)) {
                            Text(
                                text = n.title ?: n.actorName.ifBlank { "Something new" },
                                style = MaterialTheme.typography.bodyMedium,
                                color = UsTheme.extended.textPrimary,
                                maxLines = 3,
                                overflow = TextOverflow.Ellipsis,
                            )
                            Text(
                                text = shortAge(n.createdAt, now),
                                style = MaterialTheme.typography.labelMedium,
                                color = if (n.isRead) UsTheme.extended.textTertiary else UsTheme.extended.echo.text,
                            )
                        }
                    }
                }
            }
        }
    }
}

private const val STATE_HEIGHT = 0.7f
private const val LOAD_AHEAD = 4
private val TOP_ROW = 52.dp
private val SEARCH_HEIGHT = 38.dp
private val SEARCH_ICON = 18.dp

// The "Write a note" pill and its margins, above the bottom bar.
private val WRITE_PILL_ROOM = 76.dp
const val TALKED_ABOUT_NOW = "Talked about now"
