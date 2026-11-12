package com.us.android.feature.echo.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FloatingActionButton
import androidx.compose.material3.Icon
import androidx.compose.material3.PrimaryTabRow
import androidx.compose.material3.Tab
import androidx.compose.material3.Text
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.echo.data.Timeline

/**
 * Echo's home: Following (default) and For You, pull to refresh, and the
 * compose button. Echo is not a bottom-bar tab (decision 14): this is a
 * pushed screen with a back arrow.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
@Suppress("LongMethod") // tabs, refresh, list, FAB and the eligibility sheet in one scaffold
fun EchoHomeScreen(destinations: EchoDestinations, viewModel: EchoHomeViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val following by viewModel.following.collectAsStateWithLifecycle()
    val forYou by viewModel.forYou.collectAsStateWithLifecycle()
    val urls by viewModel.mediaUrls.collectAsStateWithLifecycle()
    val share = rememberNoteSharer()

    LaunchedEffect(state.openComposer) {
        state.openComposer?.let {
            viewModel.composerOpened()
            destinations.onCompose(it)
        }
    }

    val callbacks = NoteCallbacks(
        onOpen = { destinations.onOpenNote(it.id) },
        onOpenAuthor = destinations.onOpenProfile,
        // Replies are written on the note's own page, under the conversation.
        onReply = { destinations.onOpenNote(it.id) },
        onRepost = viewModel::toggleRepost,
        onQuote = viewModel::quote,
        onLike = viewModel::toggleLike,
        onShare = share,
    )

    UsScaffold(
        applyPageGutter = false,
        topBar = { UsTopBar(title = ECHO_TITLE, onBack = destinations.onBack) },
    ) { padding ->
        Box(modifier = Modifier.padding(padding).fillMaxSize()) {
            Column(modifier = Modifier.fillMaxSize()) {
                PrimaryTabRow(
                    selectedTabIndex = state.timeline.ordinal,
                    containerColor = UsTheme.extended.bgCanvas,
                    contentColor = UsTheme.extended.textPrimary,
                ) {
                    Timeline.entries.forEach { timeline ->
                        Tab(
                            selected = timeline == state.timeline,
                            onClick = { viewModel.select(timeline) },
                            text = { Text(timeline.label) },
                            selectedContentColor = UsTheme.extended.textPrimary,
                            unselectedContentColor = UsTheme.extended.textTertiary,
                            modifier = Modifier.testTag("echo_tab:${timeline.name.lowercase()}"),
                        )
                    }
                }
                val feed = if (state.timeline == Timeline.FOLLOWING) following else forYou
                PullToRefreshBox(
                    isRefreshing = feed.refreshing,
                    onRefresh = viewModel::refresh,
                    modifier = Modifier.fillMaxSize(),
                ) {
                    NoteList(
                        feed = feed,
                        mediaUrls = urls,
                        callbacks = callbacks,
                        emptyTitle = if (state.timeline == Timeline.FOLLOWING) "No notes yet" else "Nothing here yet",
                        emptyDetail = if (state.timeline == Timeline.FOLLOWING) {
                            "Notes from people you follow show up here."
                        } else {
                            "Notes picked for you show up here."
                        },
                        onRetry = viewModel::retry,
                        onLoadMore = viewModel::loadMore,
                    )
                }
            }
            FloatingActionButton(
                onClick = viewModel::compose,
                containerColor = UsTheme.extended.accent,
                contentColor = UsTheme.extended.onAccent,
                modifier = Modifier
                    .align(Alignment.BottomEnd)
                    .padding(UsTheme.spacing.pageHorizontal)
                    .testTag("echo_compose"),
            ) {
                Icon(UsIcons.SquarePen, contentDescription = "New note")
            }
            val message = remember(state.message) { state.message?.let { UsMessage(it) } }
            UsMessageHost(message = message, onDismiss = viewModel::messageShown)
        }
    }
    state.blocked?.let { EligibilitySheet(blocked = it, onDismiss = viewModel::dismissBlocked) }
}

const val ECHO_TITLE = "Echo"
