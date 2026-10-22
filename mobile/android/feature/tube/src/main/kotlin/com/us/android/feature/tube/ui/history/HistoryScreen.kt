package com.us.android.feature.tube.ui.history

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
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
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.common.time.formatDuration
import com.us.android.core.common.time.formatRelativeTime
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.feed.data.WatchHistoryEntry
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsSettingsSwitchRow
import com.us.android.feature.tube.navigation.TubeDestinations
import com.us.android.feature.tube.ui.TubePage
import com.us.android.feature.tube.ui.TubeTab
import com.us.android.feature.tube.ui.home.TubeListSkeleton
import com.us.android.feature.tube.ui.home.VideoRow
import com.us.android.feature.tube.ui.home.displayTitle
import com.us.android.feature.tube.ui.pressScale

/**
 * History (web parity, 2026-10-22): the web's Recent page on the phone.
 * The title, a search over what is loaded, "Pause recording" and "Clear
 * all", then every watched video with its progress, "Watched" or "4:10 of
 * 12:00", and when; an × on each row takes it out. Under Tube's chrome with
 * a back glyph and nothing lit on the bar, like the other library pages.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HistoryScreen(
    destinations: TubeDestinations,
    viewModel: HistoryViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val message by viewModel.message.collectAsStateWithLifecycle()
    val listState = rememberLazyListState()
    LoadMoreAtEnd(listState, state, viewModel::loadMore)

    TubePage(selected = null, destinations = destinations, onBack = destinations.onBack) { padding ->
        // Pull to refresh (UI audit 2026-10-21): the rows stay while History is read again.
        PullToRefreshBox(
            isRefreshing = state.refreshing,
            onRefresh = viewModel::refresh,
            modifier = Modifier.fillMaxSize(),
        ) {
            LazyColumn(
                state = listState,
                modifier = Modifier
                    .fillMaxSize()
                    .testTag("tube_history"),
                contentPadding = PaddingValues(
                    top = UsTheme.spacing.l,
                    bottom = padding.calculateBottomPadding() + UsTheme.spacing.xxl,
                ),
            ) {
                item(key = "header") {
                    HistoryHeader(
                        state = state,
                        onQuery = viewModel::setQuery,
                        onPaused = viewModel::setPaused,
                        onClear = viewModel::askClear,
                    )
                }
                historyBody(
                    state = state,
                    thumbFor = viewModel::thumb,
                    onOpen = { entry ->
                        viewModel.onOpen(entry)
                        destinations.onOpenVideo(entry.postId)
                    },
                    actions = HistoryRowActions(
                        remove = viewModel::remove,
                        retry = viewModel::retry,
                        loadMore = viewModel::loadMore,
                        watch = { destinations.onOpenTab(TubeTab.HOME) },
                    ),
                )
            }
            UsMessageHost(message = message, onDismiss = viewModel::dismissMessage)
        }
    }

    if (state.confirmingClear) {
        ClearHistoryDialog(onConfirm = viewModel::clearAll, onDismiss = viewModel::cancelClear)
    }
}

/** The next page once the last rows come into view. */
@Composable
private fun LoadMoreAtEnd(listState: LazyListState, state: HistoryUiState, loadMore: () -> Unit) {
    val nearEnd by remember(listState) {
        derivedStateOf {
            val last = listState.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: return@derivedStateOf false
            last >= listState.layoutInfo.totalItemsCount - PREFETCH_ROWS
        }
    }
    // Only while nothing is being searched: a filtered list's end is not the history's end.
    LaunchedEffect(nearEnd, state.canLoadMore, state.query) {
        if (nearEnd && state.canLoadMore && state.query.isBlank()) loadMore()
    }
}

/** What each row and footer can do, so the body's builder stays short. */
private class HistoryRowActions(
    val remove: (WatchHistoryEntry) -> Unit,
    val retry: () -> Unit,
    val loadMore: () -> Unit,
    val watch: () -> Unit,
)

/** The title, the line under it, the search, the pause switch and Clear all. */
@Composable
private fun HistoryHeader(
    state: HistoryUiState,
    onQuery: (String) -> Unit,
    onPaused: (Boolean) -> Unit,
    onClear: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal)
            .padding(bottom = UsTheme.spacing.m),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(modifier = Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
                Text(
                    text = "History",
                    style = MaterialTheme.typography.titleLarge,
                    fontWeight = FontWeight.Bold,
                    color = UsTheme.extended.textPrimary,
                )
                Text(
                    text = "Videos you watched, with where you left off.",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                )
            }
            if (state.rows.isNotEmpty()) {
                UsPillButton(
                    text = "Clear all",
                    onClick = onClear,
                    filled = false,
                    modifier = Modifier.testTag("tube_history_clear"),
                )
            }
        }
        UsTextField(
            value = state.query,
            onValueChange = onQuery,
            label = "Search History",
            modifier = Modifier.fillMaxWidth().testTag("tube_history_search"),
        )
        UsSettingsSwitchRow(
            title = "Pause recording",
            checked = state.paused,
            onCheckedChange = onPaused,
            description = "While paused, videos you watch are not added to History.",
            modifier = Modifier.testTag("tube_history_pause"),
        )
        if (state.paused) {
            PausedNote()
        }
    }
}

/** The web's note while recording is paused. */
@Composable
private fun PausedNote() {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .padding(vertical = UsTheme.spacing.xs)
            .testTag("tube_history_paused_note"),
    ) {
        Icon(
            imageVector = UsIcons.Pause,
            contentDescription = null,
            tint = UsTheme.extended.textSecondary,
            modifier = Modifier.size(NOTE_GLYPH),
        )
        Text(
            text = "Recording is paused. Videos you watch now are not added, and nothing resumes where you left off.",
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textSecondary,
        )
    }
}

/** The page under the header, by state: skeleton, error, empty, no match, or the rows and their footer. */
private fun LazyListScope.historyBody(
    state: HistoryUiState,
    thumbFor: (WatchHistoryEntry) -> VideoThumb,
    onOpen: (WatchHistoryEntry) -> Unit,
    actions: HistoryRowActions,
) {
    val shown = state.shown
    when {
        state.loading -> item(key = "loading") { TubeListSkeleton(modifier = Modifier.padding(top = UsTheme.spacing.l)) }
        state.error != null -> item(key = "error") {
            Box(modifier = Modifier.fillParentMaxHeight(STATE_HEIGHT)) {
                UsErrorState(message = state.error, onRetry = actions.retry)
            }
        }
        state.isEmpty -> item(key = "empty") {
            Column(
                modifier = Modifier.fillParentMaxHeight(STATE_HEIGHT).testTag("tube_history_empty"),
                horizontalAlignment = Alignment.CenterHorizontally,
            ) {
                UsEmptyState(
                    title = "Nothing watched yet",
                    detail = "Videos you watch will show up here so you can pick up where you left off.",
                    modifier = Modifier.weight(1f),
                )
                UsPillButton(text = "Watch", onClick = actions.watch)
            }
        }
        shown.isEmpty() -> item(key = "no_match") {
            Column(
                modifier = Modifier.fillParentMaxHeight(STATE_HEIGHT).testTag("tube_history_no_match"),
                horizontalAlignment = Alignment.CenterHorizontally,
            ) {
                UsEmptyState(
                    title = "No match in what is loaded",
                    detail = if (state.nextCursor != null) {
                        "Load more and search again, or try another word."
                    } else {
                        "Try another word."
                    },
                    modifier = Modifier.weight(1f),
                )
                if (state.nextCursor != null) {
                    UsPillButton(text = "Load more", onClick = actions.loadMore, busy = state.appending)
                }
            }
        }
        else -> {
            items(shown, key = { it.postId }) { entry ->
                HistoryRow(
                    entry = entry,
                    thumb = thumbFor(entry),
                    onOpen = { onOpen(entry) },
                    onRemove = { actions.remove(entry) },
                    modifier = if (UsTheme.reducedMotion) Modifier else Modifier.animateItem(),
                )
            }
            item(key = "footer") { HistoryFooter(state = state, onRetry = actions.loadMore) }
        }
    }
}

/** One watched video: the row, the × at its end, and the progress line under it. */
@Composable
private fun HistoryRow(
    entry: WatchHistoryEntry,
    thumb: VideoThumb,
    onOpen: () -> Unit,
    onRemove: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(modifier = modifier.fillMaxWidth().testTag("tube_history_row:${entry.postId}")) {
        Row(modifier = Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            VideoRow(item = entry.item, thumb = thumb, onClick = onOpen, modifier = Modifier.weight(1f))
            Box(
                contentAlignment = Alignment.Center,
                modifier = Modifier
                    .padding(end = UsTheme.spacing.m)
                    .size(REMOVE_TARGET)
                    .pressScale(onRemove)
                    .semantics {
                        role = Role.Button
                        contentDescription = "Remove from History: ${entry.item.displayTitle}"
                    }
                    .testTag("tube_history_remove:${entry.postId}"),
            ) {
                Icon(
                    imageVector = UsIcons.Close,
                    contentDescription = null,
                    tint = UsTheme.extended.textMuted,
                    modifier = Modifier.size(REMOVE_GLYPH),
                )
            }
        }
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(bottom = UsTheme.spacing.s),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            LinearProgressIndicator(
                progress = { entry.fraction },
                color = if (entry.completed) UsTheme.extended.textMuted else UsTheme.extended.accentSolid,
                trackColor = UsTheme.extended.fillSubtle,
                modifier = Modifier.weight(PROGRESS_WEIGHT).height(PROGRESS_HEIGHT),
            )
            Text(
                text = historyMeta(entry),
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                maxLines = 1,
                modifier = Modifier.weight(1f),
            )
        }
    }
}

/** The list's end: a spinner while the next page loads, Try again when it failed. */
@Composable
private fun HistoryFooter(state: HistoryUiState, onRetry: () -> Unit) {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = UsTheme.spacing.l),
        contentAlignment = Alignment.Center,
    ) {
        when {
            state.appending -> CircularProgressIndicator(
                strokeWidth = SPINNER_STROKE,
                color = UsTheme.extended.accentSolid,
                modifier = Modifier.size(SPINNER),
            )
            state.appendFailed -> UsPillButton(text = "Try again", onClick = onRetry, filled = false)
        }
    }
}

/** "Clear all of History?": every row goes and nothing resumes, so it is asked first. */
@Composable
private fun ClearHistoryDialog(onConfirm: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        containerColor = UsTheme.extended.bgSheet,
        titleContentColor = UsTheme.extended.textPrimary,
        textContentColor = UsTheme.extended.textSecondary,
        title = { Text(text = "Clear all of History?", style = MaterialTheme.typography.titleMedium) },
        text = {
            Text(
                text = "Every video is removed from History and nothing will resume where you left off. " +
                    "This cannot be undone.",
                style = MaterialTheme.typography.bodyMedium,
            )
        },
        confirmButton = {
            UsPillButton(text = "Clear", onClick = onConfirm, modifier = Modifier.testTag("tube_history_clear_confirm"))
        },
        dismissButton = { UsPillButton(text = "Cancel", onClick = onDismiss, filled = false) },
    )
}

/** "Watched · 2d", "4:10 of 12:00 · 3h": how far, then when. */
internal fun historyMeta(entry: WatchHistoryEntry): String = listOfNotNull(
    when {
        entry.completed -> "Watched"
        entry.durationMs > 0L -> "${formatDuration(entry.positionMs)} of ${formatDuration(entry.durationMs)}"
        else -> null
    },
    formatRelativeTime(entry.watchedAt).takeIf { it.isNotBlank() },
).joinToString(" · ")

private const val PREFETCH_ROWS = 4
private const val STATE_HEIGHT = 0.7f
private const val PROGRESS_WEIGHT = 0.4f
private val PROGRESS_HEIGHT = 3.dp
private val REMOVE_TARGET = 40.dp
private val REMOVE_GLYPH = 18.dp
private val NOTE_GLYPH = 16.dp
private val SPINNER = 24.dp
private val SPINNER_STROKE = 2.dp
