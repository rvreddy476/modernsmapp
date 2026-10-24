package com.us.android.feature.tube.ui.discovery

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.Topic
import com.us.android.core.feed.data.TopicSort
import com.us.android.core.feed.data.TrendingPeriod
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.feed.ui.more.PostMoreViewModel
import com.us.android.core.model.FeedItem
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsFilterChip
import com.us.android.core.ui.UsFilterChipRow
import com.us.android.feature.tube.navigation.TubeDestinations
import com.us.android.feature.tube.ui.TubeMoreHost
import com.us.android.feature.tube.ui.TubePage
import com.us.android.feature.tube.ui.home.TubeListSkeleton
import com.us.android.feature.tube.ui.home.VideoCard
import com.us.android.feature.tube.ui.pressScale
import com.us.android.feature.tube.ui.rememberTubeMoreState

/** "What people are watching …", under the Trending title, by period — the web's words. */
internal fun trendingSubtitle(period: TrendingPeriod): String = when (period) {
    TrendingPeriod.TODAY -> "What people are watching today"
    TrendingPeriod.WEEK -> "What people are watching this week"
    TrendingPeriod.MONTH -> "What people are watching this month"
}

/** The empty Trending page: narrowed out by the period, or nothing ranked at all. */
internal fun trendingEmptyTitle(period: TrendingPeriod, narrowedOut: Boolean): String = when {
    !narrowedOut -> "Nothing is trending yet"
    period == TrendingPeriod.TODAY -> "Nothing new today"
    else -> "Nothing new this ${period.label.lowercase()}"
}

/**
 * Trending (web parity, 2026-10-24): the period pills over the ranked long
 * videos. A card opens the watch screen with the list as its "Up next".
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TrendingScreen(
    destinations: TubeDestinations,
    viewModel: TrendingViewModel = hiltViewModel(),
    more: PostMoreViewModel = hiltViewModel(),
) {
    val state by viewModel.pager.state.collectAsStateWithLifecycle()
    val period by viewModel.period.collectAsStateWithLifecycle()
    val shown by viewModel.shown.collectAsStateWithLifecycle()
    val moreState = rememberTubeMoreState()
    TubePage(selected = null, destinations = destinations, onBack = destinations.onBack) { padding ->
        PullToRefreshBox(isRefreshing = state.refreshing, onRefresh = viewModel.pager::refresh, modifier = Modifier.fillMaxSize()) {
            DiscoveryVideoList(
                state = state,
                shown = shown,
                header = {
                    item(key = "head") { PageHead(icon = UsIcons.Flame, title = "Trending", subtitle = trendingSubtitle(period)) }
                    item(key = "periods") {
                        UsFilterChipRow(
                            chips = TrendingPeriod.entries.map { UsFilterChip(it.name, it.label) },
                            selectedKey = period.name,
                            onSelect = { key -> viewModel.selectPeriod(TrendingPeriod.valueOf(key)) },
                            tagPrefix = "trending_period",
                        )
                    }
                },
                empty = {
                    val narrowedOut = state.items.isNotEmpty()
                    DiscoveryEmpty(
                        title = trendingEmptyTitle(period, narrowedOut),
                        detail = if (narrowedOut) {
                            "Widen the period to see what is moving."
                        } else {
                            "Trending fills as videos get watched. Come back soon or browse by topic."
                        },
                        action = if (narrowedOut) null else "Browse topics",
                        onAction = destinations.onOpenTopics,
                    )
                },
                thumbFor = viewModel::thumb,
                onOpen = { item ->
                    viewModel.onOpen(shown)
                    destinations.onOpenVideo(item.id)
                },
                onMore = { item -> moreState.open(item, suggested = false) },
                onLoadMore = viewModel.pager::loadMore,
                onRetry = viewModel.pager::start,
                bottomPadding = padding,
                tag = "tube_trending",
            )
            TubeMoreHost(moreState, viewModel.overlays, viewModel.followEdges, viewModel.ownUserId, more)
        }
    }
}

/** Topics (web parity, 2026-10-24): every long-video subject as a tile; a tile opens its page. */
@Composable
fun TopicsScreen(destinations: TubeDestinations, viewModel: TopicsViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    TubePage(selected = null, destinations = destinations, onBack = destinations.onBack) { padding ->
        when (val now = state) {
            TopicsState.Loading -> TubeListSkeleton()
            is TopicsState.Failed -> UsErrorState(message = now.message, onRetry = viewModel::load)
            is TopicsState.Ready -> if (now.topics.isEmpty()) {
                UsEmptyState(title = "No topics yet", detail = "Topics appear once the taxonomy is published.")
            } else {
                TopicGrid(topics = now.topics, onOpen = destinations.onOpenTopic, bottomPadding = padding)
            }
        }
    }
}

@Composable
private fun TopicGrid(topics: List<Topic>, onOpen: (slug: String, label: String) -> Unit, bottomPadding: PaddingValues) {
    LazyVerticalGrid(
        columns = GridCells.Fixed(TOPIC_COLUMNS),
        modifier = Modifier.fillMaxSize().testTag("tube_topics"),
        contentPadding = PaddingValues(
            start = UsTheme.spacing.pageHorizontal,
            end = UsTheme.spacing.pageHorizontal,
            top = UsTheme.spacing.l,
            bottom = bottomPadding.calculateBottomPadding() + UsTheme.spacing.xxl,
        ),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        item(key = "head", span = { GridItemSpan(maxLineSpan) }) {
            PageHead(icon = UsIcons.Compass, title = "Topics", subtitle = "Every subject on the platform, one page each", inset = false)
        }
        items(topics, key = { it.slug }) { topic -> TopicTile(topic = topic, onClick = { onOpen(topic.slug, topic.label) }) }
    }
}

@Composable
private fun TopicTile(topic: Topic, onClick: () -> Unit) {
    val shape = RoundedCornerShape(UsTheme.radii.medium)
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .heightIn(min = TILE_HEIGHT)
            .clip(shape)
            .background(UsTheme.extended.glassBg, shape)
            .border(HAIRLINE, UsTheme.extended.glassBorder, shape)
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                contentDescription = "Open ${topic.label}"
            }
            .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.m)
            .testTag("tube_topic:${topic.slug}"),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = topic.label,
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.weight(1f),
        )
        Icon(imageVector = UsIcons.ChevronRight, contentDescription = null, tint = UsTheme.extended.textMuted, modifier = Modifier.size(GLYPH))
    }
}

/** One topic's long videos, Recent or Popular. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TopicScreen(
    destinations: TubeDestinations,
    viewModel: TopicViewModel = hiltViewModel(),
    more: PostMoreViewModel = hiltViewModel(),
) {
    val state by viewModel.pager.state.collectAsStateWithLifecycle()
    val sort by viewModel.sort.collectAsStateWithLifecycle()
    val moreState = rememberTubeMoreState()
    TubePage(selected = null, destinations = destinations, onBack = destinations.onBack) { padding ->
        PullToRefreshBox(isRefreshing = state.refreshing, onRefresh = viewModel.pager::refresh, modifier = Modifier.fillMaxSize()) {
            DiscoveryVideoList(
                state = state,
                shown = state.items,
                header = {
                    item(key = "head") { PageHead(icon = UsIcons.Hash, title = viewModel.label, subtitle = "Videos in this topic") }
                    item(key = "sorts") {
                        UsFilterChipRow(
                            chips = TopicSort.entries.map { UsFilterChip(it.wire, it.label) },
                            selectedKey = sort.wire,
                            onSelect = { key -> TopicSort.entries.firstOrNull { it.wire == key }?.let(viewModel::selectSort) },
                            tagPrefix = "topic_sort",
                        )
                    }
                },
                empty = {
                    DiscoveryEmpty(
                        title = "No videos in ${viewModel.label} yet",
                        detail = "New uploads in this topic will show up here.",
                        action = "All topics",
                        onAction = destinations.onOpenTopics,
                    )
                },
                thumbFor = viewModel::thumb,
                onOpen = { item ->
                    viewModel.onOpen(state.items)
                    destinations.onOpenVideo(item.id)
                },
                onMore = { item -> moreState.open(item, suggested = false) },
                onLoadMore = viewModel.pager::loadMore,
                onRetry = viewModel.pager::start,
                bottomPadding = padding,
                tag = "tube_topic_list",
            )
            TubeMoreHost(moreState, viewModel.overlays, viewModel.followEdges, viewModel.ownUserId, more)
        }
    }
}

/** The shared body: a header, then the first page's state or the cards, more as the end comes into view. */
@Suppress("LongParameterList")
@Composable
private fun DiscoveryVideoList(
    state: DiscoveryListState,
    shown: List<FeedItem>,
    header: LazyListScope.() -> Unit,
    empty: @Composable () -> Unit,
    thumbFor: (FeedItem) -> VideoThumb,
    onOpen: (FeedItem) -> Unit,
    onMore: (FeedItem) -> Unit,
    onLoadMore: () -> Unit,
    onRetry: () -> Unit,
    bottomPadding: PaddingValues,
    tag: String,
) {
    val list = rememberLazyListState()
    val nearEnd by remember {
        derivedStateOf {
            val last = list.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: 0
            last >= list.layoutInfo.totalItemsCount - LOAD_MORE_WITHIN
        }
    }
    LaunchedEffect(nearEnd, state.wantsMore, shown.size) {
        if (nearEnd && state.wantsMore && shown.isNotEmpty()) onLoadMore()
    }
    LazyColumn(
        state = list,
        modifier = Modifier.fillMaxSize().testTag(tag),
        contentPadding = PaddingValues(bottom = bottomPadding.calculateBottomPadding() + UsTheme.spacing.xxl),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        header()
        when (val phase = state.phase) {
            DiscoveryPhase.Loading -> item(key = "loading") { TubeListSkeleton() }
            is DiscoveryPhase.Failed -> item(key = "failed") {
                Box(modifier = Modifier.fillMaxWidth().heightIn(min = STATE_HEIGHT)) {
                    UsErrorState(message = phase.message, onRetry = onRetry)
                }
            }
            DiscoveryPhase.Ready -> {
                if (shown.isEmpty()) {
                    item(key = "empty") { empty() }
                }
                items(shown, key = { it.id }) { item ->
                    VideoCard(
                        item = item,
                        thumb = thumbFor(item),
                        onClick = { onOpen(item) },
                        onMore = { onMore(item) },
                        modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal),
                    )
                }
                if (state.canLoadMore && (state.loadingMore || state.moreFailed || shown.isEmpty())) {
                    item(key = "more") { MoreRow(loading = state.loadingMore, onMore = onLoadMore) }
                }
            }
        }
    }
}

@Composable
private fun PageHead(icon: ImageVector, title: String, subtitle: String, inset: Boolean = true) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = if (inset) UsTheme.spacing.pageHorizontal else 0.dp)
            .padding(top = UsTheme.spacing.l),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        Icon(imageVector = icon, contentDescription = null, tint = UsTheme.extended.textPrimary, modifier = Modifier.size(HEAD_GLYPH))
        Column {
            Text(text = title, style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold, color = UsTheme.extended.textPrimary)
            Text(text = subtitle, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textSecondary)
        }
    }
}

@Composable
private fun DiscoveryEmpty(title: String, detail: String, action: String?, onAction: () -> Unit) {
    Column(
        modifier = Modifier.fillMaxWidth().heightIn(min = STATE_HEIGHT).padding(horizontal = UsTheme.spacing.pageHorizontal),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m, Alignment.CenterVertically),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        UsEmptyState(title = title, detail = detail, modifier = Modifier.heightIn(max = STATE_HEIGHT).testTag("discovery_empty"))
        if (action != null) UsSecondaryButton(text = action, onClick = onAction, modifier = Modifier.testTag("discovery_empty:action"))
    }
}

@Composable
private fun MoreRow(loading: Boolean, onMore: () -> Unit) {
    Box(modifier = Modifier.fillMaxWidth().padding(vertical = UsTheme.spacing.l), contentAlignment = Alignment.Center) {
        if (loading) {
            CircularProgressIndicator(color = MaterialTheme.colorScheme.primary, strokeWidth = RING_STROKE, modifier = Modifier.size(RING))
        } else {
            UsSecondaryButton(text = "Load more", onClick = onMore, modifier = Modifier.testTag("discovery_more"))
        }
    }
}

private const val TOPIC_COLUMNS = 2
private const val LOAD_MORE_WITHIN = 3
private val TILE_HEIGHT = 56.dp
private val HAIRLINE = 1.dp
private val GLYPH = 18.dp
private val HEAD_GLYPH = 24.dp
private val STATE_HEIGHT = 280.dp
private val RING = 24.dp
private val RING_STROKE = 2.dp
