package com.us.android.feature.echo.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.shape.RoundedCornerShape
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
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
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
import com.us.android.core.designsystem.theme.UsEchoType
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.echo.data.EchoWords
import com.us.android.feature.echo.data.Note
import com.us.android.feature.echo.data.ReplyPolicy
import com.us.android.feature.echo.data.compactCount
import com.us.android.feature.echo.data.fullDate
import com.us.android.feature.echo.data.shortAge
import java.time.Instant

/**
 * One note's page (echo-look-v3): the thread or conversation above as serif
 * cards (a thread's parts joined by a thin ember line), the note in a larger
 * card with an ember border (its meta line, signature with Follow, the count
 * pills), then "Conversation · N" and the replies as bubbles, indented one
 * step per level up to two, and the rounded reply box at the bottom.
 */
@Composable
@Suppress("LongMethod", "CyclomaticComplexMethod") // conversation, the note, replies and the reply box in one page
fun NoteDetailScreen(destinations: EchoDestinations, viewModel: NoteDetailViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val replies by viewModel.replyFeed.collectAsStateWithLifecycle()
    val children by viewModel.children.collectAsStateWithLifecycle()
    val urls by viewModel.mediaUrls.collectAsStateWithLifecycle()
    val now = remember(state.note, replies.items) { Instant.now() }
    val actions = viewModel.actions
    val follows by actions.follows.collectAsStateWithLifecycle()

    LifecycleEventEffect(Lifecycle.Event.ON_RESUME) { viewModel.onResume() }
    LaunchedEffect(state.openComposer) {
        state.openComposer?.let {
            viewModel.composerOpened()
            destinations.onCompose(it)
        }
    }
    LaunchedEffect(state.deleted) { if (state.deleted) destinations.onBack() }
    LaunchedEffect(state.note?.authorId) { state.note?.authorId?.let(actions::readFollow) }

    // The ··· menu's Edit comes back here: the note is read again on return.
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
            topBar = { UsTopBar(title = NOTE_TITLE, onBack = destinations.onBack) },
            bottomBar = {
                state.note?.let { note ->
                    ReplyBox(
                        note = note,
                        checking = state.checking,
                        onReply = { viewModel.reply(note) }
                    )
                }
            },
        ) { padding ->
            Box(modifier = Modifier.padding(padding).fillMaxSize().background(UsTheme.extended.bgCanvas)) {
                val note = state.note
                when {
                    state.loading && note == null -> UsLoadingState()
                    note == null -> UsErrorState(
                        message = state.error ?: EchoWords.NOTE_NOT_FOUND,
                        onRetry = viewModel::load
                    )
                    else -> LazyColumn(modifier = Modifier.fillMaxSize().testTag("echo_detail")) {
                        item(key = "top") { Spacer(Modifier.height(CARD_GAP)) }
                        val conversation = state.conversation
                        itemsIndexed(conversation, key = { _, c -> "c:" + c.id }) { index, parent ->
                            NoteCard(
                                note = parent,
                                mediaUrls = urls,
                                callbacks = callbacks,
                                now = now,
                                modifier = Modifier.padding(horizontal = UsTheme.spacing.l),
                            )
                            ThreadJoin(joined = continuesThread(parent, conversation.getOrNull(index + 1) ?: note))
                        }
                        item(key = "focus") {
                            val parent = conversation.lastOrNull()?.takeIf { it.id == note.replyToId }
                            FocusedNote(
                                note = note,
                                urls = urls,
                                callbacks = callbacks,
                                now = now,
                                threadParts = threadPartsEndingAt(conversation, note),
                                replyToHandle = parent?.author?.handle,
                                follows = follows[note.authorId]?.takeIf { note.authorId != actions.viewerId },
                                onFollow = { actions.toggleFollowAuthor(note) },
                            )
                        }
                        // The author's thread carrying on under the note, part 3 and on included.
                        val after = state.threadAfter
                        itemsIndexed(after, key = { _, p -> "t:" + p.id }) { _, part ->
                            ThreadJoin(joined = true)
                            NoteCard(
                                note = part,
                                mediaUrls = urls,
                                callbacks = callbacks,
                                now = now,
                                modifier = Modifier.padding(horizontal = UsTheme.spacing.l).testTag("echo_thread_part"),
                            )
                        }
                        item(key = "label") {
                            ConversationLabel(
                                count = note.replyCount,
                                onOpenQuotes = { destinations.onOpenQuotes(note.id) },
                            )
                        }
                        val partIds = after.mapTo(HashSet()) { it.id }
                        val lines = replyItems(note.id, replies.items.filterNot { it.id in partIds }, children)
                        itemsIndexed(lines, key = { _, line -> line.key }) { _, line ->
                            when (line) {
                                is ReplyItem.Reply -> ReplyBubbleRow(
                                    row = line.row,
                                    urls = urls,
                                    callbacks = callbacks,
                                    now = now,
                                )
                                is ReplyItem.ViewReplies -> ChildRepliesLink(
                                    depth = line.depth,
                                    text = viewRepliesLabel(line.parent.replyCount),
                                    tag = "echo_view_replies:${line.parent.id}",
                                    onClick = { viewModel.openReplies(line.parent.id) },
                                )
                                is ReplyItem.ChildStatus -> ChildRepliesStatus(
                                    line = line,
                                    onMore = { viewModel.moreChildReplies(line.parentId) },
                                )
                            }
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

/** The gap under a card: a thin ember line when the next card is the same author's next thread part. */
@Composable
private fun ThreadJoin(joined: Boolean) {
    Box(modifier = Modifier.fillMaxWidth().height(CARD_GAP)) {
        if (joined) {
            Box(
                modifier = Modifier
                    .padding(start = THREAD_LINE_START)
                    .width(THREAD_LINE)
                    .height(CARD_GAP)
                    .background(UsTheme.extended.echo.border)
                    .testTag("echo_thread_line"),
            )
        }
    }
}

/** The note itself: a larger serif card with an ember border. */
@Composable
@Suppress("LongParameterList", "LongMethod") // the meta line, text, signature with Follow and the count pills
private fun FocusedNote(
    note: Note,
    urls: Map<String, String>,
    callbacks: NoteCallbacks,
    now: Instant,
    threadParts: Int,
    replyToHandle: String?,
    /** Null while unknown, or for the viewer's own note: no Follow pill. */
    follows: Boolean?,
    onFollow: () -> Unit,
) {
    EchoCard(modifier = Modifier.padding(horizontal = UsTheme.spacing.l).testTag("echo_focus"), accent = true) {
        val topic = topicLine(
            note = note,
            age = fullDate(note.createdAt),
            thread = threadParts > 1,
            threadParts = threadParts,
            replyToHandle = replyToHandle,
        )
        Signature(
            author = note.author,
            authorId = note.authorId,
            onOpenAuthor = callbacks.onOpenAuthor,
            trailing = if (follows == null) {
                null
            } else {
                {
                    EchoPill(
                        icon = null,
                        label = if (follows) "Following" else "Follow",
                        description = if (follows) "Unfollow" else "Follow",
                        lit = !follows,
                        tag = "echo_follow",
                        onClick = onFollow,
                    )
                }
            },
        )
        // The tag and the date only: the views are the Eye pill below.
        TopicText(
            line = topic,
            modifier = Modifier.padding(top = UsTheme.spacing.s).testTag("echo_focus_meta"),
        )
        NoteText(note = note, style = UsEchoType.notePage)
        NoteAttachments(note = note, mediaUrls = urls, callbacks = callbacks, now = now, controls = true)
        CountPills(note = note, callbacks = callbacks)
    }
}

/**
 * The counts as an even grid of labelled pills ([FOCUS_PILL_ROWS]): 3 equal
 * columns by 2 rows, the same gap across and down. `42 hearts` (the heart),
 * `3 echoes` (the Echo it menu), `120 views` (the Eye); Save (the
 * bookmark), Share and More. A cell the host has no action for stays empty,
 * so the grid keeps its shape.
 */
@Composable
private fun CountPills(note: Note, callbacks: NoteCallbacks) {
    Column(
        modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.l).testTag("echo_counts"),
        verticalArrangement = Arrangement.spacedBy(GRID_GAP),
    ) {
        FOCUS_PILL_ROWS.forEach { row ->
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(GRID_GAP),
            ) {
                row.forEach { pill ->
                    Box(modifier = Modifier.weight(1f)) {
                        FocusPillCell(pill = pill, note = note, callbacks = callbacks)
                    }
                }
            }
        }
    }
}

@Composable
@Suppress("CyclomaticComplexMethod") // one branch per grid cell
private fun FocusPillCell(pill: FocusPill, note: Note, callbacks: NoteCallbacks) {
    val cell = Modifier.fillMaxWidth()
    when (pill) {
        FocusPill.HEARTS -> EchoPill(
            icon = if (note.liked) UsIcons.HeartFilled else UsIcons.HeartOutline,
            label = compactCountWords(note.likeCount, "heart", "hearts"),
            description = if (note.liked) "Unlike" else "Like",
            lit = note.liked,
            tag = "echo_action:like",
            modifier = cell,
            compact = true,
        ) { callbacks.onLike(note) }
        FocusPill.ECHOES -> EchoPill(
            icon = UsIcons.Repost,
            label = compactCountWords(note.repostCount, "echo", "echoes"),
            description = if (note.reposted) UNDO_RE_ECHO else ECHO_IT,
            lit = note.reposted,
            tag = "echo_action:repost",
            modifier = cell,
            compact = true,
        ) { callbacks.onRepost(note) }
        FocusPill.VIEWS -> EchoPill(
            icon = UsIcons.Eye,
            label = viewsWords(note.viewCount),
            description = viewsWords(note.viewCount),
            tag = "echo_action:views",
            modifier = cell,
            compact = true,
            onClick = null,
        )
        FocusPill.SAVE -> callbacks.onBookmark?.let { onBookmark ->
            EchoPill(
                icon = if (note.bookmarked) UsIcons.BookmarkFilled else UsIcons.BookmarkOutline,
                label = if (note.bookmarked) "Saved" else "Save",
                description = if (note.bookmarked) "Remove from saved" else "Save",
                lit = note.bookmarked,
                tag = "echo_action:bookmark",
                modifier = cell,
                compact = true,
            ) { onBookmark(note) }
        }
        FocusPill.SHARE -> EchoPill(
            icon = UsIcons.Share,
            label = "Share",
            description = "Share",
            tag = "echo_action:share",
            modifier = cell,
            compact = true,
        ) { callbacks.onShare(note) }
        FocusPill.MORE -> callbacks.onMore?.let { onMore ->
            EchoPill(
                icon = UsIcons.More,
                label = null,
                description = "More",
                tag = "echo_note_more",
                modifier = cell,
                compact = true,
            ) { onMore(note) }
        }
    }
}

/** "Conversation · 12" and, at the right, the accent "View quotes" link (as the web has it). */
@Composable
private fun ConversationLabel(count: Long, onOpenQuotes: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.xxl, vertical = UsTheme.spacing.l),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = if (count > 0) "$CONVERSATION · ${compactCount(count)}" else CONVERSATION,
            style = MaterialTheme.typography.labelLarge,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.weight(1f).testTag("echo_relevant"),
        )
        Text(
            text = VIEW_QUOTES,
            style = MaterialTheme.typography.labelLarge,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.echo.text,
            modifier = Modifier
                .clip(RoundedCornerShape(UsTheme.radii.full))
                .clickable(onClick = onOpenQuotes)
                .semantics { role = Role.Button }
                .padding(horizontal = UsTheme.spacing.s, vertical = UsTheme.spacing.xs)
                .testTag("echo_view_quotes"),
        )
    }
}

/**
 * One reply on the page: a small avatar beside a bubble (name, the text and
 * anything it carries), and "Heart · Reply · 1h" under it. Indented one step
 * per level; deeper than two steps it stays flat with "Replying to @x".
 */
@Composable
@Suppress("LongMethod")
private fun ReplyBubbleRow(row: ReplyRow, urls: Map<String, String>, callbacks: NoteCallbacks, now: Instant) {
    val reply = row.note
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(
                start = UsTheme.spacing.l + REPLY_INDENT * row.depth,
                end = UsTheme.spacing.l,
                bottom = UsTheme.spacing.l,
            )
            .testTag("echo_note:${reply.id}"),
    ) {
        UsAvatar(
            name = reply.author?.name.orEmpty(),
            seed = reply.authorId,
            imageUrl = reply.author?.avatarUrl,
            size = if (row.depth == 0) UsAvatarSize.Small else UsAvatarSize.Tiny,
            modifier = Modifier.clip(RoundedCornerShape(UsTheme.radii.full)).clickable {
                callbacks.onOpenAuthor(reply.authorId)
            },
        )
        Column(modifier = Modifier.weight(1f).padding(start = UsTheme.spacing.m)) {
            val shape = RoundedCornerShape(UsTheme.radii.medium)
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(shape)
                    .background(UsTheme.extended.bgRaised)
                    .clickable { callbacks.onOpen(reply) }
                    .padding(UsTheme.spacing.l)
                    .testTag("echo_reply_bubble:${reply.id}"),
            ) {
                row.replyingTo?.let {
                    Text(
                        text = "Replying to $it",
                        style = MaterialTheme.typography.labelSmall,
                        color = UsTheme.extended.textTertiary,
                        modifier = Modifier.testTag("echo_replying_to"),
                    )
                }
                Row {
                    Text(
                        text = reply.author?.name.orEmpty(),
                        style = MaterialTheme.typography.labelLarge,
                        fontWeight = FontWeight.Bold,
                        color = UsTheme.extended.textPrimary,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.weight(1f, fill = false),
                    )
                    reply.author?.handle?.takeIf { it.isNotBlank() }?.let {
                        Text(
                            text = " $it",
                            style = MaterialTheme.typography.bodySmall,
                            color = UsTheme.extended.textTertiary,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                            modifier = Modifier.weight(1f, fill = false),
                        )
                    }
                }
                if (reply.text.isNotBlank()) {
                    Text(
                        text = rememberEchoText(reply.text),
                        style = MaterialTheme.typography.bodyMedium,
                        color = UsTheme.extended.textPrimary,
                        modifier = Modifier.padding(top = UsTheme.spacing.xs).testTag("echo_note_text"),
                    )
                }
                NoteAttachments(note = reply, mediaUrls = urls, callbacks = callbacks, now = now)
            }
            ReplyMeta(reply = reply, callbacks = callbacks, now = now)
        }
    }
}

/** "Heart · Reply · 1h", and the reply's own menu. */
@Composable
private fun ReplyMeta(reply: Note, callbacks: NoteCallbacks, now: Instant) {
    Row(
        modifier = Modifier.padding(start = UsTheme.spacing.s, top = UsTheme.spacing.xs),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        val hearts = compactCount(reply.likeCount)
        MetaLink(
            text = if (hearts.isEmpty()) "Heart" else "Heart $hearts",
            description = (if (reply.liked) "Unlike" else "Like") + if (hearts.isEmpty()) "" else ", $hearts",
            lit = reply.liked,
            tag = "echo_action:like",
        ) { callbacks.onLike(reply) }
        MetaLink(text = REPLY, description = REPLY, tag = "echo_action:reply") { callbacks.onReply(reply) }
        Text(
            text = listOfNotNull(
                shortAge(reply.createdAt, now).takeIf { it.isNotBlank() },
                "Edited".takeIf { reply.edited },
            ).joinToString(" · "),
            style = MaterialTheme.typography.labelMedium,
            color = UsTheme.extended.textTertiary,
        )
        callbacks.onMore?.let { onMore ->
            MetaLink(text = "···", description = "More", tag = "echo_note_more") { onMore(reply) }
        }
    }
}

@Composable
private fun MetaLink(text: String, description: String, tag: String, lit: Boolean = false, onClick: () -> Unit) {
    Text(
        text = text,
        style = MaterialTheme.typography.labelMedium,
        fontWeight = FontWeight.Bold,
        color = if (lit) UsTheme.extended.echo.text else UsTheme.extended.textSecondary,
        modifier = Modifier
            .clickable(onClick = onClick)
            .semantics {
                contentDescription = description
                role = Role.Button
            }
            .testTag(tag),
    )
}

/**
 * Pinned under the page: the rounded "Reply to <name>…" box, or why replies
 * are closed to this viewer (`viewer_can_reply: false`) in the author's terms.
 */
@Composable
private fun ReplyBox(note: Note, checking: Boolean, onReply: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .background(UsTheme.extended.bgCanvas)
            .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.m)
            .testTag("echo_reply_bar"),
    ) {
        if (note.viewerCanReply) {
            val shape = RoundedCornerShape(UsTheme.radii.full)
            Text(
                text = if (checking) "Checking…" else replyPrompt(note),
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textTertiary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(shape)
                    .background(UsTheme.extended.bgCardSolid)
                    .border(1.dp, UsTheme.extended.borderMedium, shape)
                    .clickable(enabled = !checking, onClick = onReply)
                    .padding(horizontal = UsTheme.spacing.xxl, vertical = UsTheme.spacing.l)
                    .testTag("echo_reply_open"),
            )
        } else {
            Text(
                text = replyClosedText(note),
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textTertiary,
                modifier = Modifier
                    .padding(horizontal = UsTheme.spacing.s, vertical = UsTheme.spacing.s)
                    .testTag("echo_reply_closed"),
            )
        }
    }
}

/** "Reply to Ada…", or "Reply…" when the author has no name to show. */
internal fun replyPrompt(note: Note): String =
    note.author?.name?.takeIf { it.isNotBlank() }?.let { "Reply to $it…" } ?: "Reply…"

/** Why this viewer cannot reply, from the note's policy. */
internal fun replyClosedText(note: Note): String = when (note.replyPolicy) {
    ReplyPolicy.FOLLOWING -> "Only people the author follows can reply."
    ReplyPolicy.MENTIONED -> "Only people mentioned can reply."
    ReplyPolicy.EVERYONE -> "You can't reply to this note."
}

private const val NOTE_TITLE = "Note"
private const val CONVERSATION = "Conversation"
private val REPLY_INDENT = 20.dp

/** The note page's pill grid: the same gap across and down. */
private val GRID_GAP = 8.dp
private val THREAD_LINE = 2.dp

// Under the signature avatar's centre: the card's inset (12) + its padding (14) + half the avatar (10) - 1.
private val THREAD_LINE_START = 35.dp

/** "View N replies" or "Show more replies", lined up with the bubbles at [depth]. */
@Composable
private fun ChildRepliesLink(depth: Int, text: String, tag: String, onClick: () -> Unit) {
    Text(
        text = text,
        style = MaterialTheme.typography.labelMedium,
        color = UsTheme.extended.echo.text,
        modifier = Modifier
            .padding(start = UsTheme.spacing.l + REPLY_INDENT * depth + CHILD_LINK_OFFSET, bottom = UsTheme.spacing.l)
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .clickable(onClick = onClick)
            .padding(vertical = UsTheme.spacing.xs)
            .testTag(tag),
    )
}

/** Under a reply's opened replies: loading, a retry when the page failed, or "Show more replies". */
@Composable
private fun ChildRepliesStatus(line: ReplyItem.ChildStatus, onMore: () -> Unit) {
    val state = line.state
    when {
        state.loading -> Text(
            text = LOADING_REPLIES,
            style = MaterialTheme.typography.labelMedium,
            color = UsTheme.extended.textTertiary,
            modifier = Modifier
                .padding(
                    start = UsTheme.spacing.l + REPLY_INDENT * line.depth + CHILD_LINK_OFFSET,
                    bottom = UsTheme.spacing.l,
                )
                .testTag("echo_child_loading:${line.parentId}"),
        )
        state.error -> ChildRepliesLink(line.depth, RETRY_REPLIES, "echo_child_retry:${line.parentId}", onMore)
        else -> ChildRepliesLink(line.depth, MORE_REPLIES, "echo_more_replies:${line.parentId}", onMore)
    }
}

/** The avatar and its gap, so the link starts under the bubble's left edge. */
private val CHILD_LINK_OFFSET = 40.dp
private const val LOADING_REPLIES = "Loading replies…"
private const val RETRY_REPLIES = "Couldn't load replies. Try again"
private const val MORE_REPLIES = "Show more replies"
