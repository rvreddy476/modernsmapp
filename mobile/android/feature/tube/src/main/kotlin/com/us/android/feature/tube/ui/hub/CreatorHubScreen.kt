package com.us.android.feature.tube.ui.hub

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.common.time.formatRelativeTime
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsSurfaceCard
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.ui.comments.CommentsSheet
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.tube.data.hub.CreatorInsights
import com.us.android.feature.tube.data.hub.HubDayPoint
import com.us.android.feature.tube.data.hub.HubDraft
import com.us.android.feature.tube.data.hub.HubInboxRow
import com.us.android.feature.tube.data.hub.HubLibraryRow
import com.us.android.feature.tube.data.hub.HubOpenKind
import com.us.android.feature.tube.data.hub.HubPeriod
import com.us.android.feature.tube.data.hub.followersNote
import com.us.android.feature.tube.data.hub.formatDelta
import com.us.android.feature.tube.data.hub.formatWatchTime
import com.us.android.feature.tube.data.hub.hubCount
import com.us.android.feature.tube.navigation.TubeDestinations
import com.us.android.feature.tube.ui.TubePage
import java.time.LocalDate
import java.time.format.DateTimeFormatter
import java.time.format.DateTimeParseException

/**
 * The Creator Hub on Android (T-31, 2026-10-15) — READ-ONLY.
 *
 * Overview (the period's views, likes, comments, shares, followers and
 * watch time, and a daily views line), Library (videos, reels, drafts) and
 * Conversations (the newest comments on the creator's posts). A row opens
 * the post where it plays; a comment opens the shared comments sheet over
 * the hub, where the creator can reply, pin and heart as on any of their
 * posts. Editing, bulk edit, the end-screen editor and payouts stay on the
 * web — there is no control for them here.
 */
@Composable
fun CreatorHubScreen(
    destinations: TubeDestinations,
    viewModel: CreatorHubViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    var commentsFor by rememberSaveable { mutableStateOf<String?>(null) }

    val open: (String, String, HubOpenKind) -> Unit = { postId, contentType, fallback ->
        when (viewModel.open(postId, contentType, fallback)) {
            HubOpenKind.REEL -> destinations.onOpenReels()
            HubOpenKind.VIDEO -> destinations.onOpenVideo(postId)
            HubOpenKind.POST -> destinations.onOpenPost(postId)
        }
    }

    TubePage(selected = null, destinations = destinations, onBack = destinations.onBack) { padding ->
        CreatorHubBody(
            state = state,
            callbacks = CreatorHubCallbacks(
                onSection = viewModel::onSection,
                onPeriod = viewModel::onPeriod,
                onRetryOverview = viewModel::retryOverview,
                onLibraryTab = viewModel::onLibraryTab,
                onLoadLibrary = viewModel::loadLibrary,
                onLoadInbox = viewModel::loadInbox,
                onOpenRow = { row, tab ->
                    // No content type on the row: the list it came from decides.
                    val fallback = if (tab == HubLibraryTab.REELS) HubOpenKind.REEL else HubOpenKind.VIDEO
                    open(row.id, row.contentType, fallback)
                },
                onOpenComments = { row -> commentsFor = row.postId },
                onOpenCommentPost = { row -> open(row.postId, row.postContentType, HubOpenKind.POST) },
            ),
            bottomPadding = padding,
        )
    }

    // The 4-A1 comments sheet, over the hub: Reply, Like, Pin and Heart as on
    // any of the creator's own posts. The creator IS the post's author here.
    commentsFor?.let { postId ->
        CommentsSheet(
            postId = postId,
            onDismiss = { commentsFor = null },
            postAuthorId = viewModel.ownUserId,
        )
    }
}

/** Everything the body can ask for; the screen resolves each. */
data class CreatorHubCallbacks(
    val onSection: (HubSection) -> Unit,
    val onPeriod: (HubPeriod) -> Unit,
    val onRetryOverview: () -> Unit,
    val onLibraryTab: (HubLibraryTab) -> Unit,
    val onLoadLibrary: (tab: HubLibraryTab, more: Boolean) -> Unit,
    val onLoadInbox: (more: Boolean) -> Unit,
    val onOpenRow: (HubLibraryRow, HubLibraryTab) -> Unit,
    val onOpenComments: (HubInboxRow) -> Unit,
    val onOpenCommentPost: (HubInboxRow) -> Unit,
)

@Composable
internal fun CreatorHubBody(
    state: CreatorHubUiState,
    callbacks: CreatorHubCallbacks,
    bottomPadding: PaddingValues,
) {
    Column(modifier = Modifier.fillMaxSize().testTag("creator_hub")) {
        Text(
            text = "Creator Hub",
            style = MaterialTheme.typography.titleLarge,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier
                .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m)
                .semantics { heading() },
        )
        PillRow(
            options = HubSection.entries,
            selected = state.section,
            label = { it.label },
            onSelect = callbacks.onSection,
            tag = "creator_hub_section",
        )
        Box(modifier = Modifier.fillMaxSize()) {
            when (state.section) {
                HubSection.OVERVIEW -> OverviewSection(state, callbacks, bottomPadding)
                HubSection.LIBRARY -> LibrarySection(state, callbacks, bottomPadding)
                HubSection.CONVERSATIONS -> ConversationsSection(state.inbox, callbacks, bottomPadding)
            }
        }
    }
}

// ── Overview ───────────────────────────────────────────────────────────

@Composable
private fun OverviewSection(state: CreatorHubUiState, callbacks: CreatorHubCallbacks, bottomPadding: PaddingValues) {
    Column(modifier = Modifier.fillMaxSize()) {
        PillRow(
            options = HubPeriod.entries,
            selected = state.period,
            label = { it.label },
            onSelect = callbacks.onPeriod,
            tag = "creator_hub_period",
        )
        when (val overview = state.overview) {
            OverviewState.Loading -> UsLoadingState(label = "Loading channel performance")
            is OverviewState.Failed -> UsErrorState(message = overview.message, onRetry = callbacks.onRetryOverview)
            is OverviewState.Ready -> OverviewContent(overview.insights, state.period, bottomPadding)
        }
    }
}

@Composable
private fun OverviewContent(d: CreatorInsights, period: HubPeriod, bottomPadding: PaddingValues) {
    val metrics = overviewMetrics(d, period)
    Column(
        modifier = Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = UsTheme.spacing.pageHorizontal)
            .padding(top = UsTheme.spacing.l, bottom = bottomPadding.calculateBottomPadding() + UsTheme.spacing.xxl)
            .testTag("creator_hub_overview"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        metrics.chunked(2).forEach { pair ->
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
                pair.forEach { MetricTile(it, Modifier.weight(1f)) }
                if (pair.size == 1) Spacer(Modifier.weight(1f))
            }
        }
        if (d.viewsByDay.isNotEmpty()) {
            UsSurfaceCard(modifier = Modifier.fillMaxWidth()) {
                Text(
                    text = "Views by day",
                    style = MaterialTheme.typography.titleSmall,
                    fontWeight = FontWeight.SemiBold,
                    color = UsTheme.extended.textPrimary,
                )
                ViewsSparkline(
                    points = d.viewsByDay,
                    description = "Channel views by day, ${period.since.lowercase()}",
                )
                Row(modifier = Modifier.fillMaxWidth()) {
                    AxisLabel(dayLabel(d.viewsByDay.first().day))
                    Spacer(Modifier.weight(1f))
                    AxisLabel(dayLabel(d.viewsByDay.last().day))
                }
            }
        }
    }
}

@Composable
private fun AxisLabel(text: String) {
    Text(text, style = MaterialTheme.typography.labelSmall, color = UsTheme.extended.textMuted)
}

/** One overview figure: what it is, its value as the web prints it, and the line under it. */
data class HubMetric(val label: String, val value: String, val note: String)

/** The overview's six tiles, in the web's order and words, with the web's formatting. */
fun overviewMetrics(d: CreatorInsights, period: HubPeriod): List<HubMetric> = listOf(
    HubMetric("Views", hubCount(d.views), period.since),
    HubMetric("Likes", hubCount(d.likes), period.since),
    HubMetric("Comments", hubCount(d.comments), period.since),
    HubMetric("Shares", hubCount(d.shares), period.since),
    HubMetric("Followers", formatDelta(d.followersDelta), followersNote(d)),
    HubMetric("Watch time", formatWatchTime(d.watchTimeMs), "Time spent watching"),
)

@Composable
private fun MetricTile(metric: HubMetric, modifier: Modifier = Modifier) {
    UsSurfaceCard(modifier = modifier.testTag("creator_hub_metric:${metric.label}")) {
        Text(metric.label, style = MaterialTheme.typography.labelMedium, color = UsTheme.extended.textSecondary)
        Text(
            metric.value,
            style = MaterialTheme.typography.headlineSmall,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textPrimary,
            maxLines = 1,
        )
        Text(metric.note, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
    }
}

/**
 * A minimal daily views line: no axis but the baseline, the day range
 * printed under it. Drawn with the theme's accent and border tokens; the
 * geometry is [sparklinePoints], a pure function.
 */
@Composable
private fun ViewsSparkline(points: List<HubDayPoint>, description: String) {
    val lineColor = UsTheme.extended.accent
    val baseColor = UsTheme.extended.borderSubtle
    Canvas(
        modifier = Modifier
            .fillMaxWidth()
            .height(CHART_HEIGHT)
            .semantics { contentDescription = description }
            .testTag("creator_hub_chart"),
    ) {
        val base = size.height
        drawLine(baseColor, Offset(0f, base), Offset(size.width, base), strokeWidth = BASE_STROKE.toPx())
        val xy = sparklinePoints(points.map { it.views }, size.width, size.height)
        if (xy.size == 1) {
            drawCircle(lineColor, radius = DOT_RADIUS.toPx(), center = Offset(xy[0].first, xy[0].second))
            return@Canvas
        }
        val path = Path()
        xy.forEachIndexed { i, (x, y) -> if (i == 0) path.moveTo(x, y) else path.lineTo(x, y) }
        val stroke = Stroke(width = LINE_STROKE.toPx(), cap = StrokeCap.Round, join = StrokeJoin.Round)
        drawPath(path, lineColor, style = stroke)
    }
}

/** Headroom above the busiest day, so its peak is not clipped by the stroke. */
private const val CHART_HEADROOM = 0.9f

/**
 * The sparkline's points in a [width]×[height] box: evenly spaced, oldest
 * at the left; the busiest day at 90% of the height, a zero day on the
 * baseline. A single day sits in the middle. All-zero days lie flat.
 */
fun sparklinePoints(values: List<Long>, width: Float, height: Float): List<Pair<Float, Float>> {
    if (values.isEmpty()) return emptyList()
    val max = values.max().coerceAtLeast(1L).toFloat()
    val step = if (values.size > 1) width / (values.size - 1) else 0f
    return values.mapIndexed { i, v ->
        val x = if (values.size == 1) width / 2f else i * step
        x to (height - (v.coerceAtLeast(0L) / max) * height * CHART_HEADROOM)
    }
}

private val DAY_FORMAT: DateTimeFormatter = DateTimeFormatter.ofPattern("MMM d")

/** "Oct 1" for a calendar day; anything else as it came. */
internal fun dayLabel(day: String): String = try {
    LocalDate.parse(day).format(DAY_FORMAT)
} catch (_: DateTimeParseException) {
    day
}

// ── Library ────────────────────────────────────────────────────────────

@Composable
private fun LibrarySection(state: CreatorHubUiState, callbacks: CreatorHubCallbacks, bottomPadding: PaddingValues) {
    Column(modifier = Modifier.fillMaxSize()) {
        PillRow(
            options = HubLibraryTab.entries,
            selected = state.libraryTab,
            label = { it.label },
            onSelect = callbacks.onLibraryTab,
            tag = "creator_hub_library_tab",
        )
        val tab = state.libraryTab
        when (tab) {
            HubLibraryTab.VIDEOS, HubLibraryTab.REELS -> {
                val page = if (tab == HubLibraryTab.VIDEOS) state.videos else state.reels
                PagedList(
                    page = page,
                    empty = if (tab == HubLibraryTab.VIDEOS) "No videos yet" else "No reels yet",
                    emptyDetail = "What you post shows up here with its views, likes and comments.",
                    onRetry = { callbacks.onLoadLibrary(tab, false) },
                    onMore = { callbacks.onLoadLibrary(tab, true) },
                    bottomPadding = bottomPadding,
                    key = { it.id },
                ) { row -> LibraryRowCard(row, onClick = { callbacks.onOpenRow(row, tab) }) }
            }
            HubLibraryTab.DRAFTS -> PagedList(
                page = state.drafts,
                empty = "No drafts",
                emptyDetail = "Drafts you save while posting wait here.",
                onRetry = { callbacks.onLoadLibrary(tab, false) },
                onMore = { callbacks.onLoadLibrary(tab, true) },
                bottomPadding = bottomPadding,
                key = { it.id },
            ) { draft -> DraftCard(draft) }
        }
    }
}

/** "Public · 3d", the row's second line. */
internal fun libraryMeta(row: HubLibraryRow): String =
    listOf(row.visibility.label, formatRelativeTime(row.date)).filter { it.isNotEmpty() }.joinToString(" · ")

/** "12 views · 4 likes · 2 comments", with the web's count formatting. */
internal fun libraryCounts(row: HubLibraryRow): String =
    "${hubCount(row.viewCount)} views · ${hubCount(row.likeCount)} likes · ${hubCount(row.commentCount)} comments"

@Composable
private fun LibraryRowCard(row: HubLibraryRow, onClick: () -> Unit) {
    UsSurfaceCard(modifier = Modifier.fillMaxWidth().testTag("creator_hub_row:${row.id}"), onClick = onClick) {
        Text(
            row.title,
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
        )
        Text(libraryMeta(row), style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textSecondary)
        Text(libraryCounts(row), style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
        if (row.processingStatus == PROCESSING_FAILED) {
            Text(
                "Processing failed",
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.statusDanger,
            )
        }
    }
}

/** "Held · Last saved 2d", a draft's second line. */
internal fun draftMeta(draft: HubDraft): String {
    val saved = formatRelativeTime(draft.updatedAt)
    return if (saved.isEmpty()) draft.statusLabel else "${draft.statusLabel} · Last saved $saved"
}

@Composable
private fun DraftCard(draft: HubDraft) {
    UsSurfaceCard(modifier = Modifier.fillMaxWidth().testTag("creator_hub_draft:${draft.id}")) {
        Text(
            draft.title,
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
        )
        Text(draftMeta(draft), style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textSecondary)
        draft.blockedReason?.let {
            Text(it, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.statusWarning)
        }
    }
}

// ── Conversations ──────────────────────────────────────────────────────

@Composable
private fun ConversationsSection(
    page: HubPage<HubInboxRow>,
    callbacks: CreatorHubCallbacks,
    bottomPadding: PaddingValues,
) {
    PagedList(
        page = page,
        empty = "No comments yet",
        emptyDetail = "New comments on your videos show up here.",
        onRetry = { callbacks.onLoadInbox(false) },
        onMore = { callbacks.onLoadInbox(true) },
        bottomPadding = bottomPadding,
        key = { it.commentId },
    ) { row ->
        InboxCard(
            row = row,
            onReply = { callbacks.onOpenComments(row) },
            onViewPost = { callbacks.onOpenCommentPost(row) },
        )
    }
}

/** "Asha · My first video · 2h", the comment's second line (the web overview's). */
internal fun inboxMeta(row: HubInboxRow): String =
    listOf(row.authorName, row.postTitle, formatRelativeTime(row.createdAt))
        .filter { it.isNotEmpty() }
        .joinToString(" · ")

@Composable
private fun InboxCard(row: HubInboxRow, onReply: () -> Unit, onViewPost: () -> Unit) {
    UsSurfaceCard(
        modifier = Modifier.fillMaxWidth().testTag("creator_hub_comment:${row.commentId}"),
        onClick = onReply,
    ) {
        Text(
            row.body.ifEmpty { "—" },
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textPrimary,
            maxLines = 4,
            overflow = TextOverflow.Ellipsis,
        )
        Text(
            inboxMeta(row),
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textSecondary,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            modifier = Modifier.fillMaxWidth(),
        ) {
            if (row.authorReplied) {
                Text("Replied", style = MaterialTheme.typography.labelSmall, color = UsTheme.extended.statusSuccess)
            }
            Spacer(Modifier.weight(1f))
            UsPillButton(text = "View post", onClick = onViewPost, filled = false)
            UsPillButton(text = "Reply", onClick = onReply)
        }
    }
}

// ── Shared pieces ──────────────────────────────────────────────────────

@Composable
private fun <T> PillRow(
    options: List<T>,
    selected: T,
    label: (T) -> String,
    onSelect: (T) -> Unit,
    tag: String,
) {
    Row(
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        modifier = Modifier
            .fillMaxWidth()
            .horizontalScroll(rememberScrollState())
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.xs),
    ) {
        options.forEach { option ->
            UsPillButton(
                text = label(option),
                onClick = { onSelect(option) },
                filled = option == selected,
                modifier = Modifier.testTag("$tag:${label(option)}"),
            )
        }
    }
}

/** A paged list with the three states, a footer that loads the next page, and a retry when only that page failed. */
@Suppress("LongParameterList") // the states and the row, each one the caller's
@Composable
private fun <T> PagedList(
    page: HubPage<T>,
    empty: String,
    emptyDetail: String,
    onRetry: () -> Unit,
    onMore: () -> Unit,
    bottomPadding: PaddingValues,
    key: (T) -> String,
    row: @Composable (T) -> Unit,
) {
    when {
        !page.loaded || (page.loading && page.items.isEmpty()) -> UsLoadingState()
        page.error != null -> UsErrorState(message = page.error, onRetry = onRetry)
        page.items.isEmpty() -> UsEmptyState(title = empty, detail = emptyDetail)
        else -> LazyColumn(
            modifier = Modifier.fillMaxSize(),
            contentPadding = PaddingValues(
                start = UsTheme.spacing.pageHorizontal,
                end = UsTheme.spacing.pageHorizontal,
                top = UsTheme.spacing.l,
                bottom = bottomPadding.calculateBottomPadding() + UsTheme.spacing.xxl,
            ),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            items(page.items, key = key) { row(it) }
            pageFooter(page, onMore)
        }
    }
}

private fun <T> LazyListScope.pageFooter(page: HubPage<T>, onMore: () -> Unit) {
    if (!page.hasMore && !page.moreFailed) return
    item(key = "footer") {
        Box(modifier = Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) {
            when {
                page.loadingMore -> CircularProgressIndicator(color = UsTheme.extended.accent)
                page.moreFailed -> UsSecondaryButton(text = "Couldn't load more. Try again", onClick = onMore)
                else -> UsSecondaryButton(text = "Load more", onClick = onMore)
            }
        }
    }
}

private const val PROCESSING_FAILED = "failed"
private val CHART_HEIGHT = 120.dp
private val LINE_STROKE = 2.dp
private val BASE_STROKE = 1.dp
private val DOT_RADIUS = 3.dp
