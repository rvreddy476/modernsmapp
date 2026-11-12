package com.us.android.feature.echo.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.echo.data.EchoWords
import com.us.android.feature.echo.data.Note
import java.time.Instant

/**
 * One note: the conversation above it, the note, the reply bar, then its
 * replies (paged). The reply bar says so when the author has limited
 * replies; otherwise it asks eligibility before opening the composer.
 */
@Composable
@Suppress("LongMethod", "CyclomaticComplexMethod") // conversation, note, reply bar and replies in one list
fun NoteDetailScreen(destinations: EchoDestinations, viewModel: NoteDetailViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val replies by viewModel.replyFeed.collectAsStateWithLifecycle()
    val urls by viewModel.mediaUrls.collectAsStateWithLifecycle()
    val share = rememberNoteSharer()
    var menuFor by remember { mutableStateOf<Note?>(null) }
    val now = remember(state.note, replies.items) { Instant.now() }

    LifecycleEventEffect(Lifecycle.Event.ON_RESUME) { viewModel.onResume() }
    LaunchedEffect(state.openComposer) {
        state.openComposer?.let {
            viewModel.composerOpened()
            destinations.onCompose(it)
        }
    }
    LaunchedEffect(state.deleted) { if (state.deleted) destinations.onBack() }

    val callbacks = NoteCallbacks(
        onOpen = { if (it.id != viewModel.noteId) destinations.onOpenNote(it.id) },
        onOpenAuthor = destinations.onOpenProfile,
        onReply = viewModel::reply,
        onRepost = viewModel::toggleRepost,
        onQuote = viewModel::quote,
        onLike = viewModel::toggleLike,
        onShare = share,
        onMore = { menuFor = it },
    )

    UsScaffold(
        applyPageGutter = false,
        topBar = { UsTopBar(title = "Note", onBack = destinations.onBack) }
    ) { padding ->
        Box(modifier = Modifier.padding(padding).fillMaxSize()) {
            val note = state.note
            when {
                state.loading && note == null -> UsLoadingState()
                note == null -> UsErrorState(
                    message = state.error ?: EchoWords.NOTE_NOT_FOUND,
                    onRetry = viewModel::load
                )
                else -> LazyColumn(modifier = Modifier.fillMaxSize().testTag("echo_detail")) {
                    items(state.conversation, key = { "c:" + it.id }) { parent ->
                        NoteCard(note = parent, mediaUrls = urls, callbacks = callbacks, now = now)
                    }
                    item(key = "focus") {
                        NoteCard(note = note, mediaUrls = urls, callbacks = callbacks, now = now, focused = true)
                    }
                    item(
                        key = "reply_bar"
                    ) { ReplyBar(note = note, checking = state.checking, onReply = { viewModel.reply(note) }) }
                    items(replies.items, key = { "r:" + it.id }) { reply ->
                        NoteCard(note = reply, mediaUrls = urls, callbacks = callbacks, now = now)
                    }
                    item(key = "foot") {
                        when {
                            replies.loading && !replies.loaded -> UsLoadingState(modifier = Modifier.fillMaxWidth())
                            replies.error != null -> UsErrorState(
                                message = replies.error.orEmpty(),
                                onRetry = viewModel::retryReplies
                            )
                            replies.empty -> Text(
                                text = "No replies yet",
                                style = MaterialTheme.typography.bodyMedium,
                                color = UsTheme.extended.textTertiary,
                                modifier = Modifier.padding(UsTheme.spacing.pageHorizontal),
                            )
                            else -> {
                                ListFoot(feed = replies, onRetry = viewModel::loadMoreReplies)
                                LaunchedEffect(replies.cursor) { if (replies.canLoadMore) viewModel.loadMoreReplies() }
                            }
                        }
                    }
                }
            }
            val message = remember(state.message) { state.message?.let { UsMessage(it) } }
            UsMessageHost(message = message, onDismiss = viewModel::messageShown)
        }
    }

    state.blocked?.let { EligibilitySheet(blocked = it, onDismiss = viewModel::dismissBlocked) }
    menuFor?.let { target ->
        NoteMoreSheet(
            menu = viewModel.menuFor(target),
            report = state.report,
            onEdit = {
                menuFor = null
                viewModel.edit(target)
            },
            onDelete = {
                menuFor = null
                viewModel.delete(target)
            },
            onReport = { reason, details -> viewModel.report(target, reason, details) },
            onDismiss = {
                menuFor = null
                viewModel.resetReport()
            },
        )
    }
}

/**
 * Under the note: "Post your reply", or why replies are closed to this
 * viewer (`viewer_can_reply: false`) in the author's terms.
 */
@Composable
private fun ReplyBar(note: Note, checking: Boolean, onReply: () -> Unit) {
    Column(modifier = Modifier.fillMaxWidth().testTag("echo_reply_bar")) {
        if (note.viewerCanReply) {
            Text(
                text = if (checking) "Checking…" else "Post your reply",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textTertiary,
                modifier = Modifier
                    .fillMaxWidth()
                    .clickable(enabled = !checking, onClick = onReply)
                    .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.xxl)
                    .testTag("echo_reply_open"),
            )
        } else {
            Text(
                text = replyClosedText(note),
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textTertiary,
                modifier = Modifier
                    .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l)
                    .testTag("echo_reply_closed"),
            )
        }
        HorizontalDivider(color = UsTheme.extended.borderSubtle)
    }
}

/** Why this viewer cannot reply, from the note's policy. */
internal fun replyClosedText(note: Note): String = when (note.replyPolicy) {
    com.us.android.feature.echo.data.ReplyPolicy.FOLLOWING -> "Only people the author follows can reply."
    com.us.android.feature.echo.data.ReplyPolicy.MENTIONED -> "Only people mentioned can reply."
    com.us.android.feature.echo.data.ReplyPolicy.EVERYONE -> "You can't reply to this note."
}
