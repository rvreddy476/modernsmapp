package com.us.android.feature.echo.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import java.time.Instant

/**
 * A paged list of notes with its states: loading, the first page's error,
 * empty, and the foot (more loading, a later page's error with Try again).
 * The next page is asked for as the last few rows come into view.
 */
@Composable
fun NoteList(
    feed: NoteFeed,
    mediaUrls: Map<String, String>,
    callbacks: NoteCallbacks,
    emptyTitle: String,
    onRetry: () -> Unit,
    onLoadMore: () -> Unit,
    modifier: Modifier = Modifier,
    emptyDetail: String? = null,
    contentPadding: PaddingValues = PaddingValues(),
    header: (LazyListScope.() -> Unit)? = null,
) {
    val listState = rememberLazyListState()
    val nearEnd by remember {
        derivedStateOf {
            val last = listState.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: 0
            last >= listState.layoutInfo.totalItemsCount - LOAD_AHEAD
        }
    }
    LaunchedEffect(nearEnd, feed.cursor, feed.loaded) { if (nearEnd && feed.canLoadMore) onLoadMore() }
    val now = remember(feed.items) { Instant.now() }

    LazyColumn(
        state = listState,
        modifier = modifier.fillMaxSize().testTag("echo_list"),
        contentPadding = contentPadding
    ) {
        header?.invoke(this)
        when {
            feed.loading && !feed.loaded -> item {
                UsLoadingState(modifier = Modifier.fillParentMaxHeight(STATE_HEIGHT))
            }
            feed.error != null -> item {
                UsErrorState(
                    message = feed.error,
                    onRetry = onRetry,
                    modifier = Modifier.fillParentMaxHeight(STATE_HEIGHT)
                )
            }
            feed.empty -> item {
                UsEmptyState(
                    title = emptyTitle,
                    detail = emptyDetail,
                    modifier = Modifier.fillParentMaxHeight(STATE_HEIGHT)
                )
            }
            else -> {
                items(feed.items, key = { it.id }) { note ->
                    NoteCard(note = note, mediaUrls = mediaUrls, callbacks = callbacks, now = now)
                }
                item(key = "echo_foot") { ListFoot(feed = feed, onRetry = onLoadMore) }
            }
        }
    }
}

/** A list section's foot: a spinner while the next page loads, a later page's error with Try again. */
@Composable
internal fun ListFoot(feed: NoteFeed, onRetry: () -> Unit) {
    Box(modifier = Modifier.fillMaxWidth().padding(UsTheme.spacing.l), contentAlignment = Alignment.Center) {
        when {
            feed.loadingMore -> CircularProgressIndicator(color = UsTheme.extended.accent)
            feed.moreError != null -> TextButton(onClick = onRetry) {
                Text(
                    text = "${feed.moreError} Try again",
                    style = MaterialTheme.typography.labelLarge,
                    color = UsTheme.extended.accent
                )
            }
        }
    }
}

private const val LOAD_AHEAD = 4
private const val STATE_HEIGHT = 0.7f
