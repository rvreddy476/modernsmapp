package com.us.android.feature.echo.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.echo.data.EchoWords
import com.us.android.feature.echo.data.Note
import com.us.android.feature.echo.data.ReplyPolicy
import com.us.android.feature.echo.data.VerificationMark
import com.us.android.feature.echo.data.compactCount
import com.us.android.feature.echo.data.fullDate
import java.time.Instant

/**
 * One post, X's page: the conversation above it, the post large (full date
 * · views, the counts row, the action row), "Relevant" replies under it and
 * the reply bar pinned to the bottom. Quotes open on their own page.
 */
@Composable
@Suppress("LongMethod", "CyclomaticComplexMethod") // conversation, the post, replies and the reply bar in one page
fun NoteDetailScreen(destinations: EchoDestinations, viewModel: NoteDetailViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val replies by viewModel.replyFeed.collectAsStateWithLifecycle()
    val urls by viewModel.mediaUrls.collectAsStateWithLifecycle()
    val now = remember(state.note, replies.items) { Instant.now() }
    val actions = viewModel.actions

    LifecycleEventEffect(Lifecycle.Event.ON_RESUME) { viewModel.onResume() }
    LaunchedEffect(state.openComposer) {
        state.openComposer?.let {
            viewModel.composerOpened()
            destinations.onCompose(it)
        }
    }
    LaunchedEffect(state.deleted) { if (state.deleted) destinations.onBack() }

    // The ··· menu's Edit comes back here: the post is read again on return.
    val hostDestinations = remember(destinations, viewModel) {
        destinations.copy(
            onCompose = {
                viewModel.composeFromMenu(it)
                destinations.onCompose(it)
            },
        )
    }
    val callbacks = rememberNoteCallbacks(
        actions = actions,
        destinations = destinations,
        onReply = viewModel::reply,
        onQuote = viewModel::quote,
        onOpen = { if (it.id != viewModel.noteId) destinations.onOpenNote(it.id) },
    )

    EchoLinks(destinations) {
        UsScaffold(
            applyPageGutter = false,
            topBar = { UsTopBar(title = "Post", onBack = destinations.onBack) },
            bottomBar = {
                state.note?.let { note ->
                    ReplyBar(
                        note = note,
                        checking = state.checking,
                        onReply = { viewModel.reply(note) }
                    )
                }
            },
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
                            FocusedNote(
                                note = note,
                                urls = urls,
                                callbacks = callbacks,
                                now = now,
                                onOpenQuotes = { destinations.onOpenQuotes(note.id) },
                            )
                        }
                        items(replies.items, key = { "r:" + it.id }) { reply ->
                            NoteCard(note = reply, mediaUrls = urls, callbacks = callbacks, now = now)
                        }
                        item(key = "foot") {
                            when {
                                replies.loading && !replies.loaded -> UsLoadingState(modifier = Modifier.fillMaxWidth())
                                replies.error != null -> UsErrorState(
                                    message = replies.error.orEmpty(),
                                    onRetry = viewModel::retryReplies,
                                )
                                replies.empty -> Text(
                                    text = "No replies yet",
                                    style = MaterialTheme.typography.bodyMedium,
                                    color = UsTheme.extended.textTertiary,
                                    modifier = Modifier.padding(UsTheme.spacing.xxl),
                                )
                                else -> {
                                    ListFoot(feed = replies, onRetry = viewModel::loadMoreReplies)
                                    LaunchedEffect(
                                        replies.cursor
                                    ) { if (replies.canLoadMore) viewModel.loadMoreReplies() }
                                }
                            }
                        }
                    }
                }
                val message = remember(state.message) { state.message?.let { UsMessage(it) } }
                UsMessageHost(message = message, onDismiss = viewModel::messageShown)
            }
        }
    }
    LaunchedEffect(state.note?.id) { state.note?.let { actions.seen(listOf(it)) } }
    NoteActionsHost(actions = actions, destinations = hostDestinations, onQuote = viewModel::quote)

    state.blocked?.let {
        EligibilitySheet(
            blocked = it,
            onDismiss = viewModel::dismissBlocked,
            premiumEntryShown = destinations.premiumEntryShown,
            onGetPremium = destinations.onGetPremium,
        )
    }
}

/** The post itself, X's large layout. */
@Composable
private fun FocusedNote(
    note: Note,
    urls: Map<String, String>,
    callbacks: NoteCallbacks,
    now: Instant,
    onOpenQuotes: () -> Unit,
) {
    Column(modifier = Modifier.fillMaxWidth().padding(horizontal = UsTheme.spacing.xxl).testTag("echo_focus")) {
        Row(modifier = Modifier.padding(top = UsTheme.spacing.l), verticalAlignment = Alignment.CenterVertically) {
            val author = note.author
            UsAvatar(
                name = author?.name.orEmpty(),
                seed = note.authorId,
                imageUrl = author?.avatarUrl,
                size = UsAvatarSize.Medium,
                modifier = Modifier.clickable { callbacks.onOpenAuthor(note.authorId) },
            )
            Column(modifier = Modifier.weight(1f).padding(start = UsTheme.spacing.l)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        text = author?.name.orEmpty(),
                        style = MaterialTheme.typography.titleSmall,
                        fontWeight = FontWeight.Bold,
                        color = UsTheme.extended.textPrimary,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.weight(1f, fill = false),
                    )
                    author?.mark?.takeIf { it != VerificationMark.NONE }?.let { VerificationTick(it) }
                }
                Text(
                    text = author?.handle.orEmpty(),
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textTertiary,
                )
            }
            callbacks.onMore?.let { onMore ->
                Icon(
                    imageVector = UsIcons.More,
                    contentDescription = "More",
                    tint = UsTheme.extended.textTertiary,
                    modifier = Modifier
                        .clip(RoundedCornerShape(UsTheme.radii.full))
                        .clickable { onMore(note) }
                        .padding(UsTheme.spacing.xs)
                        .size(MORE_ICON)
                        .testTag("echo_note_more"),
                )
            }
        }
        NoteBody(note = note, mediaUrls = urls, callbacks = callbacks, now = now, large = true)
        val meta = listOfNotNull(
            fullDate(note.createdAt).takeIf { it.isNotBlank() },
            compactCount(note.viewCount).takeIf { it.isNotEmpty() }?.let { "$it Views" },
            "Edited".takeIf { note.edited },
        )
        Text(
            text = meta.joinToString(" · "),
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textTertiary,
            modifier = Modifier.padding(vertical = UsTheme.spacing.l).testTag("echo_focus_meta"),
        )
        HorizontalDivider(color = UsTheme.extended.borderSubtle)
        CountsRow(note = note, onOpenQuotes = onOpenQuotes)
        HorizontalDivider(color = UsTheme.extended.borderSubtle)
        NoteActionRow(note = note, callbacks = callbacks, showCounts = false)
    }
    HorizontalDivider(color = UsTheme.extended.borderSubtle)
    Text(
        text = "Relevant",
        style = MaterialTheme.typography.labelLarge,
        fontWeight = FontWeight.Bold,
        color = UsTheme.extended.textPrimary,
        modifier = Modifier.padding(
            horizontal = UsTheme.spacing.xxl,
            vertical = UsTheme.spacing.l
        ).testTag("echo_relevant"),
    )
    HorizontalDivider(color = UsTheme.extended.borderSubtle)
}

/** Reposts, Quotes (opens them), Likes, Bookmarks; a zero count is left out, as X does. */
@Composable
private fun CountsRow(note: Note, onOpenQuotes: () -> Unit) {
    val counts = listOf(
        Triple(note.repostCount, if (note.repostCount == 1L) "Repost" else "Reposts", null),
        Triple(note.quoteCount, if (note.quoteCount == 1L) "Quote" else "Quotes", onOpenQuotes),
        Triple(note.likeCount, if (note.likeCount == 1L) "Like" else "Likes", null),
        Triple(note.bookmarkCount, if (note.bookmarkCount == 1L) "Bookmark" else "Bookmarks", null),
    ).filter { it.first > 0 }
    Row(
        modifier = Modifier.fillMaxWidth().padding(vertical = UsTheme.spacing.l).testTag("echo_counts"),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xl),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        counts.forEach { (count, label, onClick) -> CountLabel(count = count, label = label, onClick = onClick) }
        if (note.quoteCount <= 0) {
            Text(
                text = "View quotes",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textTertiary,
                modifier = Modifier.clickable(onClick = onOpenQuotes).testTag("echo_view_quotes"),
            )
        }
    }
}

/**
 * Pinned under the page: "Post your reply", or why replies are closed to
 * this viewer (`viewer_can_reply: false`) in the author's terms.
 */
@Composable
private fun ReplyBar(note: Note, checking: Boolean, onReply: () -> Unit) {
    Column(modifier = Modifier.fillMaxWidth().background(UsTheme.extended.bgCanvas).testTag("echo_reply_bar")) {
        HorizontalDivider(color = UsTheme.extended.borderSubtle)
        if (note.viewerCanReply) {
            Text(
                text = if (checking) "Checking…" else "Post your reply",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textTertiary,
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = UsTheme.spacing.xxl, vertical = UsTheme.spacing.m)
                    .clip(RoundedCornerShape(UsTheme.radii.full))
                    .background(UsTheme.extended.bgCardHover)
                    .clickable(enabled = !checking, onClick = onReply)
                    .padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.l)
                    .testTag("echo_reply_open"),
            )
        } else {
            Text(
                text = replyClosedText(note),
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textTertiary,
                modifier = Modifier
                    .padding(horizontal = UsTheme.spacing.xxl, vertical = UsTheme.spacing.l)
                    .testTag("echo_reply_closed"),
            )
        }
    }
}

/** Why this viewer cannot reply, from the note's policy. */
internal fun replyClosedText(note: Note): String = when (note.replyPolicy) {
    ReplyPolicy.FOLLOWING -> "Only people the author follows can reply."
    ReplyPolicy.MENTIONED -> "Only people mentioned can reply."
    ReplyPolicy.EVERYONE -> "You can't reply to this post."
}

private val MORE_ICON = 18.dp
