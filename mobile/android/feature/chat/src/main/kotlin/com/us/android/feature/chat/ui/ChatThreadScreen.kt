package com.us.android.feature.chat.ui

import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import coil3.compose.AsyncImage
import com.us.android.core.chat.data.MAX_MESSAGE_LENGTH
import com.us.android.core.chat.data.Message
import com.us.android.core.chat.data.PendingSend
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.network.ApiConfig
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import kotlin.math.abs

/**
 * One conversation, over the DURABLE boundary (directive §5.4/§5.5).
 *
 * Newest-first, reversed so the newest message sits at the bottom. Below the
 * server-confirmed thread render the OUTBOX rows: queued sends (clock), and
 * parked-failed sends with Retry/Discard — those rows come from Room, so they
 * survive process death exactly as the user left them.
 */
@Composable
@Suppress("LongMethod", "CyclomaticComplexMethod")
fun ChatThreadScreen(
    title: String,
    onOpenGroupInfo: () -> Unit,
    isGroup: Boolean,
    onBack: () -> Unit,
    onStartCall: (peerUserId: String, peerName: String, video: Boolean) -> Unit = { _, _, _ -> },
    viewModel: ChatThreadViewModel = hiltViewModel(),
) {
    val render by viewModel.state.collectAsStateWithLifecycle()
    val state = render.thread
    val listState = rememberLazyListState()

    // MULTIPLE, and staged rather than sent. The picker hands back a list
    // that lands on the composer; the upload waits for Send.
    val pickImages = rememberLauncherForActivityResult(
        ActivityResultContracts.PickMultipleVisualMedia(ChatThreadViewModel.MAX_ATTACHMENTS),
    ) { uris -> viewModel.stageAttachments(uris) }

    val shouldLoadMore by remember(state) {
        derivedStateOf {
            val last = listState.layoutInfo.visibleItemsInfo.lastOrNull()?.index
                ?: return@derivedStateOf false
            state.canLoadMore && last >= state.messages.lastIndex - PREFETCH_DISTANCE
        }
    }
    LaunchedEffect(shouldLoadMore) {
        if (shouldLoadMore) viewModel.loadMore()
    }

    val newestId = state.messages.firstOrNull()?.id
    LaunchedEffect(newestId, render.pendingSends.size) {
        if (newestId != null && listState.firstVisibleItemIndex <= FOLLOW_THRESHOLD) {
            listState.animateScrollToItem(0)
        }
    }

    UsScaffold(
        topBar = {
            ChatThreadTopBar(
                // A deep-linked thread navigates with a blank title; the
                // loaded conversation supplies the real one.
                title = title.ifBlank { render.loadedTitle.ifBlank { "Conversation" } },
                typing = state.typingUserIds.isNotEmpty(),
                // Calls are DIRECT-only in P0, and the buttons appear only
                // once the roster resolved the peer. Whether the peer may
                // actually be called is the server's decision when the button
                // is pressed — visibility here is not permission.
                showCalls = !isGroup && !render.loadedIsGroup && render.peerUserId.isNotBlank(),
                showGroupInfo = isGroup || render.loadedIsGroup,
                onBack = onBack,
                onAudioCall = {
                    onStartCall(render.peerUserId, title.ifBlank { render.loadedTitle }, false)
                },
                onVideoCall = {
                    onStartCall(render.peerUserId, title.ifBlank { render.loadedTitle }, true)
                },
                onOpenGroupInfo = onOpenGroupInfo,
            )
        },
        applyPageGutter = false,
    ) { padding ->
        // The keyboard inset arrives in `padding`: UsScaffold folds the ime
        // into its contentWindowInsets, so applying it here is what lifts the
        // composer above the keyboard instead of leaving it underneath.
        Column(modifier = Modifier.padding(padding).fillMaxSize()) {
            if (render.offline) {
                OfflineThreadBanner(onRetry = viewModel::refresh)
            }

            Box(modifier = Modifier.weight(1f)) {
                when {
                    state.loading && state.messages.isEmpty() ->
                        UsLoadingState(label = "Loading messages")

                    state.refreshError != null && state.messages.isEmpty() -> UsErrorState(
                        message = "We couldn't load this conversation.",
                        onRetry = viewModel::refresh,
                    )

                    state.messages.isEmpty() && render.pendingSends.isEmpty() -> UsEmptyState(
                        title = "No messages yet",
                        detail = "Say something to start the conversation.",
                    )

                    else -> LazyColumn(
                        state = listState,
                        reverseLayout = true,
                        modifier = Modifier.fillMaxSize(),
                    ) {
                        // Outbox rows render newest-most (index 0 = visual
                        // bottom): whatever is still queued sits under the
                        // confirmed history, where an in-flight message
                        // belongs.
                        items(
                            render.pendingSends.asReversed(),
                            key = { "pending:" + it.idempotencyKey },
                        ) { pending ->
                            PendingSendRow(
                                pending = pending,
                                onRetry = { viewModel.retrySend(pending.idempotencyKey) },
                                onDiscard = { viewModel.abandonSend(pending.idempotencyKey) },
                            )
                        }
                        itemsIndexed(
                            state.messages,
                            key = { _, message -> message.id },
                        ) { index, message ->
                            // The sender label belongs to GROUPS only, and
                            // only at the head of a run. A direct thread has
                            // exactly two people and the alignment already
                            // says which is which, so naming every incoming
                            // bubble just repeats the top bar over and over.
                            //
                            // The list is newest-first under reverseLayout,
                            // so the message drawn ABOVE this one is index+1.
                            val previousSender = state.messages.getOrNull(index + 1)?.senderId
                            MessageRow(
                                message = message,
                                isOwn = message.senderId == render.viewerId,
                                showSenderName = (isGroup || render.loadedIsGroup) &&
                                    previousSender != message.senderId,
                                readByPeer = message.id == state.peerLastReadMessageId,
                                quoteAuthor = viewModel.quoteAuthorName(message.replyToSenderId),
                                onReact = { emoji ->
                                    viewModel.toggleReaction(message.id, emoji)
                                },
                                onReply = { viewModel.startReply(message) },
                                onDelete = { viewModel.deleteMessage(message.id) },
                            )
                        }
                    }
                }
            }

            ComposerStatus(render = render, onDismissError = viewModel::dismissAttachmentError)

            render.replyingTo?.let { original ->
                ReplyBanner(
                    author = viewModel.quoteAuthorName(original.senderId),
                    preview = original.text.ifBlank {
                        if (original.mediaId != null) "Photo" else ""
                    },
                    onCancel = viewModel::cancelReply,
                )
            }

            if (render.staged.isNotEmpty()) {
                StagedAttachmentRow(
                    staged = render.staged,
                    onRemove = viewModel::unstageAttachment,
                )
            }

            Composer(
                draft = state.draft,
                // One send at a time: while an enqueue awaits Room, a second
                // tap must not mint a second outbox row for the same text.
                canSend = render.canSend,
                attaching = render.sendInFlight,
                onDraftChange = {
                    viewModel.onDraftChange(it)
                    viewModel.sendTyping()
                },
                onSend = viewModel::send,
                onAttach = {
                    pickImages.launch(
                        PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly),
                    )
                },
            )
        }
    }
}

/**
 * The thread header, per the Figma conversation frame (98:331): card-dark
 * bar with a hairline underneath, the peer's avatar beside a bold name, and
 * the LIVE state line — "typing…" in the chat green when the peer is typing.
 * The call buttons are the design's video/phone glyphs, wired to the real
 * calling stack.
 */
@Suppress("LongParameterList")
@Composable
private fun ChatThreadTopBar(
    title: String,
    typing: Boolean,
    showCalls: Boolean,
    showGroupInfo: Boolean,
    onBack: () -> Unit,
    onAudioCall: () -> Unit,
    onVideoCall: () -> Unit,
    onOpenGroupInfo: () -> Unit,
) {
    Column {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            modifier = Modifier
                .fillMaxWidth()
                // The bar shares the page's colour rather than sitting on a
                // lighter slab. The old bgCardSolid band read as a grey box
                // pinned across the top, and the composer wore a matching one
                // at the bottom — two grey bars framing a black thread.
                .background(MaterialTheme.colorScheme.background)
                .padding(
                    horizontal = UsTheme.spacing.xxl,
                    vertical = UsTheme.spacing.m,
                ),
        ) {
            IconButton(onClick = onBack) {
                Icon(
                    imageVector = UsIcons.Back,
                    contentDescription = "Back",
                    tint = UsTheme.extended.textPrimary,
                )
            }
            UsAvatar(name = title, size = UsAvatarSize.Small, seed = title)
            Column(modifier = Modifier.weight(1f)) {
                Text(
                    text = title,
                    // The name is the one piece of identity on this screen, so
                    // it gets a treatment of its own: heavier than a title and
                    // tracked tighter, which is what makes a name read as a
                    // name rather than as another label.
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.ExtraBold,
                    letterSpacing = NAME_TRACKING,
                    color = UsTheme.extended.textPrimary,
                    maxLines = 1,
                )
                if (typing) {
                    Text(
                        text = "typing…",
                        style = MaterialTheme.typography.labelSmall,
                        color = UsTheme.extended.chatOnline,
                        modifier = Modifier.testTag("thread-typing"),
                    )
                }
            }
            if (showCalls) {
                IconButton(
                    onClick = onVideoCall,
                    modifier = Modifier.testTag("thread-call-video"),
                ) {
                    Icon(
                        imageVector = UsIcons.Video,
                        contentDescription = "Video call",
                        tint = UsTheme.extended.textPrimary,
                    )
                }
                IconButton(
                    onClick = onAudioCall,
                    modifier = Modifier.testTag("thread-call-audio"),
                ) {
                    Icon(
                        imageVector = UsIcons.Phone,
                        contentDescription = "Voice call",
                        tint = UsTheme.extended.textPrimary,
                    )
                }
            }
            if (showGroupInfo) {
                TextButton(
                    onClick = onOpenGroupInfo,
                    modifier = Modifier.testTag("thread-group-info"),
                ) { Text("Info") }
            }
        }
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .height(HAIRLINE)
                .background(UsTheme.extended.borderSubtle),
        )
    }
}

@Composable
private fun OfflineThreadBanner(onRetry: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .background(MaterialTheme.colorScheme.surfaceVariant)
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.xs),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            "You're offline — showing saved messages. Sends will go out when you're back.",
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
            modifier = Modifier.weight(1f),
        )
        TextButton(onClick = onRetry) { Text("Retry") }
    }
}

/**
 * The photos sitting on the composer, waiting for Send.
 *
 * Each thumbnail carries its OWN state: a ring spinning over it while its
 * bytes are going up, a red edge if the server refused it, and an × to take
 * it back off. That is what a shared percentage row could not express — with
 * several photos in flight, one number cannot say which one it is counting.
 */
@Composable
private fun StagedAttachmentRow(
    staged: List<StagedAttachment>,
    onRemove: (Uri) -> Unit,
) {
    LazyRow(
        contentPadding = PaddingValues(
            horizontal = UsTheme.spacing.pageHorizontal,
            vertical = UsTheme.spacing.s,
        ),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        modifier = Modifier.testTag("staged-attachments"),
    ) {
        items(staged, key = { it.uri.toString() }) { item ->
            Box {
                AsyncImage(
                    model = item.uri,
                    contentDescription = "Attached photo",
                    contentScale = ContentScale.Crop,
                    modifier = Modifier
                        .size(STAGED_THUMB)
                        .clip(RoundedCornerShape(UsTheme.radii.medium))
                        .background(UsTheme.extended.bgCardSolid)
                        .then(
                            if (item.failed) {
                                Modifier.border(
                                    width = STAGED_FAILED_BORDER,
                                    color = MaterialTheme.colorScheme.error,
                                    shape = RoundedCornerShape(UsTheme.radii.medium),
                                )
                            } else {
                                Modifier
                            },
                        ),
                )
                if (item.uploading) {
                    // The ring sits ON the photo, dimming it — the picture
                    // stays visible underneath so it is obvious WHICH one is
                    // going up.
                    Box(
                        contentAlignment = Alignment.Center,
                        modifier = Modifier
                            .size(STAGED_THUMB)
                            .clip(RoundedCornerShape(UsTheme.radii.medium))
                            .background(SCRIM),
                    ) {
                        CircularProgressIndicator(
                            color = Color.White,
                            strokeWidth = SPINNER_STROKE,
                            modifier = Modifier
                                .size(SPINNER_SIZE)
                                .testTag("attachment-progress"),
                        )
                    }
                } else {
                    IconButton(
                        onClick = { onRemove(item.uri) },
                        modifier = Modifier
                            .align(Alignment.TopEnd)
                            .size(REMOVE_BUTTON),
                    ) {
                        Box(
                            contentAlignment = Alignment.Center,
                            modifier = Modifier
                                .size(REMOVE_GLYPH_BG)
                                .clip(CircleShape)
                                .background(SCRIM),
                        ) {
                            Icon(
                                imageVector = UsIcons.Close,
                                contentDescription = "Remove photo",
                                tint = Color.White,
                                modifier = Modifier.size(REMOVE_GLYPH),
                            )
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun ComposerStatus(
    render: ThreadRenderState,
    onDismissError: () -> Unit,
) {
    // Typing lives in the HEADER now (98:340) — one live state line beside
    // the name, instead of a floating caption over the composer.
    val state = render.thread
    if (state.draftTooLong) {
        Text(
            text = "That's longer than a message can be. " +
                "The limit is $MAX_MESSAGE_LENGTH characters.",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.error,
            modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal),
        )
    }

    // NO PERCENTAGE ROW. A number ticking 0→100 under the composer says less
    // than a ring drawn on the photo it belongs to, and with several photos
    // in flight one shared number cannot say WHICH. Progress now lives on
    // each thumbnail in [StagedAttachmentRow].
    render.attachmentError?.let { message ->
        Row(
            modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = message,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
                modifier = Modifier.weight(1f).testTag("attachment-error"),
            )
            TextButton(onClick = onDismissError) { Text("Dismiss") }
        }
    }
    if (render.sendUnavailable) {
        Text(
            text = "Chat is finishing a security cleanup. Your message wasn't " +
                "sent — it's kept below, tap Send to retry.",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.error,
            modifier = Modifier
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .testTag("send-unavailable"),
        )
    }
}

/**
 * The input bar, per the Figma conversation frame (98:365): a card-dark bar
 * over a hairline — plus for attachments, a full-round pill field with the
 * camera inside it, and the circular chat-green send button that dims when
 * there is nothing to send.
 */
@Composable
private fun Composer(
    draft: String,
    canSend: Boolean,
    attaching: Boolean,
    onDraftChange: (String) -> Unit,
    onSend: () -> Unit,
    onAttach: () -> Unit,
) {
    var emojiOpen by remember { mutableStateOf(false) }
    Column {
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .height(HAIRLINE)
                .background(UsTheme.extended.borderSubtle),
        )
        if (emojiOpen) {
            EmojiPanel(onPick = { emoji -> onDraftChange(draft + emoji) })
        }
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .background(MaterialTheme.colorScheme.background)
                .padding(UsTheme.spacing.xxl),
            verticalAlignment = Alignment.Bottom,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            IconButton(
                onClick = onAttach,
                enabled = !attaching,
                modifier = Modifier.testTag("thread-attach"),
            ) {
                Icon(
                    imageVector = UsIcons.Create,
                    contentDescription = "Attach a photo",
                    tint = UsTheme.extended.textPrimary,
                )
            }
            ComposerField(
                draft = draft,
                attaching = attaching,
                emojiOpen = emojiOpen,
                onDraftChange = onDraftChange,
                onAttach = onAttach,
                onToggleEmoji = { emojiOpen = !emojiOpen },
                modifier = Modifier.weight(1f),
            )
            Box(
                contentAlignment = Alignment.Center,
                modifier = Modifier
                    .size(SEND_BUTTON)
                    .clip(CircleShape)
                    .background(
                        if (canSend) UsTheme.extended.chatAccent else UsTheme.extended.bgCanvas,
                    )
                    .clickable(enabled = canSend, onClick = onSend)
                    .semantics {
                        contentDescription = if (canSend) "Send" else "Send. Unavailable."
                    },
            ) {
                Icon(
                    imageVector = UsIcons.Forward,
                    contentDescription = null,
                    tint = if (canSend) Color.White else UsTheme.extended.textGhost,
                    modifier = Modifier.size(SEND_GLYPH),
                )
            }
        }
    }
}

/** The pill field: smiley, placeholder, draft, and the camera glyph inside. */
@Suppress("LongParameterList")
@Composable
private fun ComposerField(
    draft: String,
    attaching: Boolean,
    emojiOpen: Boolean,
    onDraftChange: (String) -> Unit,
    onAttach: () -> Unit,
    onToggleEmoji: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        modifier = modifier
            .clip(RoundedCornerShape(UsTheme.radii.full))
            // The pill is now the only lifted surface down here, so it takes
            // the card colour: on a bar that matches the page, bgCanvas was
            // a black field on black and only the hairline showed it.
            .background(UsTheme.extended.bgCardSolid)
            .border(
                width = HAIRLINE,
                color = UsTheme.extended.borderSubtle,
                shape = RoundedCornerShape(UsTheme.radii.full),
            )
            .padding(
                horizontal = UsTheme.spacing.xl,
                vertical = UsTheme.spacing.l,
            ),
    ) {
        Icon(
            imageVector = UsIcons.Smile,
            contentDescription = if (emojiOpen) "Hide emoji" else "Emoji",
            tint = if (emojiOpen) UsTheme.extended.chatAccent else UsTheme.extended.textMuted,
            modifier = Modifier
                .size(COMPOSER_GLYPH)
                .clip(CircleShape)
                .clickable(onClick = onToggleEmoji)
                .testTag("thread-emoji"),
        )
        Box(modifier = Modifier.weight(1f)) {
            if (draft.isEmpty()) {
                Text(
                    text = "Type a message…",
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textMuted,
                )
            }
            BasicTextField(
                value = draft,
                onValueChange = onDraftChange,
                textStyle = MaterialTheme.typography.bodyMedium.copy(
                    color = UsTheme.extended.textPrimary,
                ),
                cursorBrush = SolidColor(UsTheme.extended.chatAccent),
                maxLines = 5,
                modifier = Modifier
                    .fillMaxWidth()
                    .semantics { contentDescription = "Message" },
            )
        }
        Icon(
            imageVector = UsIcons.Camera,
            contentDescription = "Attach a photo",
            tint = UsTheme.extended.textMuted,
            modifier = Modifier
                .size(COMPOSER_GLYPH)
                .clip(CircleShape)
                .clickable(enabled = !attaching, onClick = onAttach),
        )
    }
}

/**
 * The emoji panel: a curated grid inserted into the draft at a tap. Kept
 * in-app rather than relying on the keyboard's own emoji page — the point
 * of the smiley button is that emoji are ONE tap away, not three.
 */
@Composable
private fun EmojiPanel(onPick: (String) -> Unit) {
    LazyVerticalGrid(
        columns = GridCells.Fixed(EMOJI_COLUMNS),
        modifier = Modifier
            .fillMaxWidth()
            .height(EMOJI_PANEL_HEIGHT)
            .background(UsTheme.extended.bgCardSolid)
            .testTag("emoji-panel"),
        contentPadding = PaddingValues(UsTheme.spacing.m),
    ) {
        items(EMOJI_CHOICES) { emoji ->
            Text(
                text = emoji,
                style = MaterialTheme.typography.headlineSmall,
                textAlign = TextAlign.Center,
                modifier = Modifier
                    .clip(RoundedCornerShape(UsTheme.radii.small))
                    .clickable { onPick(emoji) }
                    .padding(UsTheme.spacing.s)
                    .semantics { contentDescription = "Insert $emoji" },
            )
        }
    }
}

/**
 * A queued or parked-failed send from the durable outbox.
 *
 * Queued renders dimmed with a "Sending…" caption; a parked-failed row states
 * it plainly and offers Retry (same idempotency key — a replay, never a
 * duplicate) and Discard.
 */
@Composable
private fun PendingSendRow(
    pending: PendingSend,
    onRetry: () -> Unit,
    onDiscard: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(
                horizontal = UsTheme.spacing.pageHorizontal,
                vertical = UsTheme.spacing.s,
            )
            .testTag("thread-pending-send"),
    ) {
        // A queued PHOTO shows the photo, not the words "📷 Photo".
        //
        // Sends drain through WorkManager, so there is a real gap between the
        // tap and the confirmed bubble. A queued text row already renders its
        // own text, which is why sending text felt instant while sending an
        // image looked like nothing happened until the screen was reopened.
        // The upload finishes BEFORE the row is enqueued, so the id is live
        // and the authorized serve route renders it immediately.
        pending.mediaId?.let { mediaId ->
            Box(modifier = Modifier.alpha(PENDING_ALPHA)) {
                AttachmentImage(mediaId = mediaId)
            }
        }
        if (pending.text.isNotBlank() || pending.mediaId == null) {
            Text(
                text = pending.text,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textMuted,
            )
        }
        if (pending.failed) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            ) {
                Text(
                    "Not sent.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
                TextButton(onClick = onRetry, modifier = Modifier.testTag("pending-retry")) {
                    Text("Retry")
                }
                TextButton(onClick = onDiscard) { Text("Discard") }
            }
        } else {
            Text(
                "Sending…",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
        }
    }
}

/**
 * One message. Own messages align right and carry a "Read" caption when the
 * privacy-gated receipt named them; long-press opens react/delete actions —
 * both offered only on rows carrying a full Scylla address, and delete is
 * OFFERED on own messages while the server remains the authority on who may.
 */
@Composable
@Suppress("LongMethod", "LongParameterList")
private fun MessageRow(
    message: Message,
    isOwn: Boolean,
    showSenderName: Boolean,
    readByPeer: Boolean,
    quoteAuthor: String,
    onReact: (String) -> Unit,
    onReply: () -> Unit,
    onDelete: () -> Unit,
) {
    var menuOpen by remember { mutableStateOf(false) }
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .combinedClickable(
                enabled = message.addressable,
                onClick = {},
                onLongClick = { menuOpen = true },
            )
            .padding(
                horizontal = UsTheme.spacing.pageHorizontal,
                vertical = UsTheme.spacing.s,
            ),
        horizontalArrangement = if (isOwn) {
            Arrangement.spacedBy(UsTheme.spacing.l, Alignment.End)
        } else {
            Arrangement.spacedBy(UsTheme.spacing.l)
        },
    ) {
        val name = message.senderDisplayName.orEmpty()
        Column(
            modifier = Modifier.weight(1f, fill = false),
            horizontalAlignment = if (isOwn) Alignment.End else Alignment.Start,
        ) {
            // Figma group thread (98:395): the sender's name sits ABOVE the
            // bubble in a colour that stays theirs for the whole thread.
            // Gated by [showSenderName] — groups, run-heads, incoming only.
            if (showSenderName && name.isNotBlank() && !isOwn) {
                Text(
                    text = name,
                    style = MaterialTheme.typography.labelMedium,
                    fontWeight = FontWeight.SemiBold,
                    color = senderColor(message.senderId.ifBlank { name }),
                )
            }
            MessageBubble(message = message, isOwn = isOwn, quoteAuthor = quoteAuthor)
            if (message.reactions.isNotEmpty()) {
                // The reaction chip hangs off the bubble's corner (98:363).
                Text(
                    text = message.reactions.joinToString(" ") { "${it.emoji} ${it.userIds.size}" },
                    style = MaterialTheme.typography.labelSmall,
                    color = UsTheme.extended.textPrimary,
                    modifier = Modifier
                        .padding(top = UsTheme.spacing.xs)
                        .clip(RoundedCornerShape(UsTheme.radii.full))
                        .background(UsTheme.extended.bgCardSolid)
                        .border(
                            width = HAIRLINE,
                            color = UsTheme.extended.borderSubtle,
                            shape = RoundedCornerShape(UsTheme.radii.full),
                        )
                        .padding(
                            horizontal = UsTheme.spacing.s,
                            vertical = UsTheme.spacing.xs,
                        )
                        .testTag("message-reactions"),
                )
            }
            if (isOwn && readByPeer) {
                Text(
                    text = "Read",
                    style = MaterialTheme.typography.labelSmall,
                    color = UsTheme.extended.textMuted,
                    modifier = Modifier.testTag("message-read"),
                )
            }
            DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                // The reaction palette is ONE ROW, the way every messenger
                // draws it — six items stacked read as a menu of choices to
                // study, side by side they read as a strip to tap.
                Row(
                    horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
                    modifier = Modifier.padding(horizontal = UsTheme.spacing.m),
                ) {
                    REACTION_CHOICES.forEach { emoji ->
                        Text(
                            text = emoji,
                            style = MaterialTheme.typography.titleLarge,
                            modifier = Modifier
                                .clip(CircleShape)
                                .clickable {
                                    menuOpen = false
                                    onReact(emoji)
                                }
                                .padding(UsTheme.spacing.xs)
                                .semantics { contentDescription = "React $emoji" },
                        )
                    }
                }
                DropdownMenuItem(
                    text = { Text("Reply") },
                    onClick = {
                        menuOpen = false
                        onReply()
                    },
                    modifier = Modifier.testTag("message-reply"),
                )
                if (isOwn) {
                    DropdownMenuItem(
                        text = { Text("Delete", color = MaterialTheme.colorScheme.error) },
                        onClick = {
                            menuOpen = false
                            onDelete()
                        },
                    )
                }
            }
        }
    }
}

/**
 * The bubble itself (98:348/98:351): incoming card-dark, outgoing chat
 * green, 18dp corners with the 4dp tail on the sender's side. A pending own
 * message renders translucent — visibly not yet the server's.
 */
@Composable
@Suppress("CyclomaticComplexMethod")
private fun MessageBubble(message: Message, isOwn: Boolean, quoteAuthor: String = "") {
    val shape = if (isOwn) {
        RoundedCornerShape(
            topStart = BUBBLE_CORNER,
            topEnd = BUBBLE_CORNER,
            bottomStart = BUBBLE_CORNER,
            bottomEnd = BUBBLE_TAIL,
        )
    } else {
        RoundedCornerShape(
            topStart = BUBBLE_CORNER,
            topEnd = BUBBLE_CORNER,
            bottomStart = BUBBLE_TAIL,
            bottomEnd = BUBBLE_CORNER,
        )
    }
    // A PHOTO IS ITS OWN BUBBLE.
    //
    // A picture in a green slab reads as a picture stuck to a coloured card:
    // the fill wins the eye and the photo's own edges are lost inside it. A
    // media message therefore carries no bubble colour and no padding — the
    // image, clipped to the bubble's shape, IS the bubble. A caption under
    // one still needs a readable ground, so a media message WITH text keeps
    // the fill; only the bare photo goes without — unless it QUOTES another
    // message, because the quote card needs a ground to sit on.
    val photoOnly = message.mediaId != null && message.text.isBlank() &&
        message.replyToId == null
    val bubbleColor = when {
        photoOnly -> Color.Transparent
        isOwn && message.pending -> UsTheme.extended.chatAccent.copy(alpha = PENDING_ALPHA)
        isOwn -> UsTheme.extended.chatAccent
        else -> UsTheme.extended.bgCardSolid
    }
    Column(
        modifier = Modifier
            .widthIn(max = BUBBLE_MAX_WIDTH)
            .clip(shape)
            .background(bubbleColor)
            .padding(
                horizontal = if (photoOnly) 0.dp else UsTheme.spacing.xl,
                vertical = if (photoOnly) 0.dp else UsTheme.spacing.m,
            ),
    ) {
        // The quote sits INSIDE the bubble, above the answer — a slim card
        // with the original's author and snapshot, the way every messenger
        // draws a reply. Rendered from the denormalised fields alone; the
        // original message may be pages away or deleted.
        if (message.replyToId != null) {
            QuoteCard(
                author = quoteAuthor,
                preview = message.replyToPreview.orEmpty(),
                onDarkGround = isOwn || message.text.isBlank(),
            )
        }
        message.mediaId?.let { mediaId ->
            AttachmentImage(mediaId = mediaId)
        }
        if (message.text.isNotBlank()) {
            Text(
                text = message.text,
                style = MaterialTheme.typography.bodyMedium,
                color = if (isOwn) Color.White else UsTheme.extended.textPrimary,
            )
        }
    }
}

/**
 * The quoted original inside a reply bubble: accent bar, author, snapshot.
 * A translucent black ground reads on both bubble fills — deep enough on
 * chat green, a visible step darker on the incoming card.
 */
@Composable
private fun QuoteCard(author: String, preview: String, onDarkGround: Boolean) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(bottom = UsTheme.spacing.s)
            .clip(RoundedCornerShape(UsTheme.radii.small))
            .background(QUOTE_GROUND),
    ) {
        Box(
            modifier = Modifier
                .width(QUOTE_BAR)
                .height(QUOTE_MIN_HEIGHT)
                .background(if (onDarkGround) Color.White else UsTheme.extended.chatAccent),
        )
        Column(
            modifier = Modifier.padding(
                horizontal = UsTheme.spacing.m,
                vertical = UsTheme.spacing.xs,
            ),
        ) {
            if (author.isNotBlank()) {
                Text(
                    text = author,
                    style = MaterialTheme.typography.labelSmall,
                    fontWeight = FontWeight.Bold,
                    color = Color.White,
                    maxLines = 1,
                )
            }
            Text(
                text = preview.ifBlank { "Message" },
                style = MaterialTheme.typography.bodySmall,
                color = Color.White.copy(alpha = QUOTE_TEXT_ALPHA),
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

/**
 * "Replying to …" above the composer — the armed state of a reply, with the
 * original's snapshot and an × to disarm. Sits where the send it shapes will
 * happen, not floating over the thread.
 */
@Composable
private fun ReplyBanner(author: String, preview: String, onCancel: () -> Unit) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier
            .fillMaxWidth()
            .padding(
                horizontal = UsTheme.spacing.pageHorizontal,
                vertical = UsTheme.spacing.xs,
            )
            .clip(RoundedCornerShape(UsTheme.radii.small))
            .background(UsTheme.extended.bgCardSolid)
            .testTag("reply-banner"),
    ) {
        Box(
            modifier = Modifier
                .width(QUOTE_BAR)
                .height(REPLY_BANNER_HEIGHT)
                .background(UsTheme.extended.chatAccent),
        )
        Column(
            modifier = Modifier
                .weight(1f)
                .padding(horizontal = UsTheme.spacing.m),
        ) {
            Text(
                text = if (author.isNotBlank()) "Replying to $author" else "Replying",
                style = MaterialTheme.typography.labelSmall,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.chatAccent,
                maxLines = 1,
            )
            Text(
                text = preview.ifBlank { "Message" },
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        IconButton(onClick = onCancel, modifier = Modifier.testTag("reply-cancel")) {
            Icon(
                imageVector = UsIcons.Close,
                contentDescription = "Cancel reply",
                tint = UsTheme.extended.textMuted,
                modifier = Modifier.size(REMOVE_GLYPH),
            )
        }
    }
}

/**
 * An image attachment, loaded through the AUTHORIZED serve route with the
 * app's authenticated image loader — media-service enforces the chat access
 * check server-side, so an id in a conversation the viewer left simply 403s.
 */
@Composable
private fun AttachmentImage(
    mediaId: String,
    config: ApiConfig = androidx.compose.ui.platform.LocalContext.current.let {
        // ApiConfig is provided via Hilt; using the composition-local context
        // entry point avoids threading it through every call site.
        dagger.hilt.android.EntryPointAccessors.fromApplication(
            it.applicationContext,
            ChatMediaEntryPoint::class.java,
        ).apiConfig()
    },
) {
    AsyncImage(
        model = config.baseUrl.trimEnd('/') + "/v1/media/" + mediaId + "/serve",
        contentDescription = "Photo attachment",
        contentScale = ContentScale.Crop,
        modifier = Modifier
            .fillMaxWidth()
            .aspectRatio(ATTACHMENT_ASPECT)
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .background(UsTheme.extended.bgCard)
            .testTag("thread-attachment"),
    )
}

@dagger.hilt.EntryPoint
@dagger.hilt.InstallIn(dagger.hilt.components.SingletonComponent::class)
interface ChatMediaEntryPoint {
    fun apiConfig(): ApiConfig
}

private const val PREFETCH_DISTANCE = 3
private const val FOLLOW_THRESHOLD = 1
private const val ATTACHMENT_ASPECT = 4f / 3f

/** The quick-reaction palette. The server stores any emoji string. */
private val REACTION_CHOICES = listOf("❤️", "👍", "😂", "😮", "😢", "🙏")

/**
 * The composer's emoji grid — a curated set across the categories people
 * actually send, not a full unicode browser. The keyboard remains the long
 * tail; this is the fast path.
 */
private val EMOJI_CHOICES = listOf(
    "😀", "😂", "🤣", "😊", "😍", "😘", "😎", "🤩",
    "😅", "😉", "🙃", "😇", "🥰", "😜", "🤔", "🙄",
    "😴", "🥺", "😢", "😭", "😡", "🤯", "😱", "🥳",
    "👍", "👎", "👏", "🙌", "🙏", "🤝", "💪", "✌️",
    "👀", "🔥", "✨", "🎉", "🚀", "❤️", "💔", "💯",
    "😋", "🍕", "☕", "🍻", "🎂", "🌟", "🌈", "☀️",
)

private const val EMOJI_COLUMNS = 8
private val EMOJI_PANEL_HEIGHT = 220.dp

// ── The Figma conversation language (98:321) ────────────────────────────

// Quoted replies.
private val QUOTE_BAR = 3.dp
private val QUOTE_MIN_HEIGHT = 40.dp
private val REPLY_BANNER_HEIGHT = 44.dp
private const val QUOTE_TEXT_ALPHA = 0.8f

/** Translucent ground the quote card sits on, inside either bubble fill. */
@Suppress("MagicNumber")
private val QUOTE_GROUND = Color(0x33000000)

// Composer attachments, staged and waiting for Send.
private val STAGED_THUMB = 72.dp
private val STAGED_FAILED_BORDER = 2.dp
private val SPINNER_SIZE = 28.dp
private val SPINNER_STROKE = 3.dp
private val REMOVE_BUTTON = 28.dp
private val REMOVE_GLYPH_BG = 20.dp
private val REMOVE_GLYPH = 12.dp

/** Dim laid over a thumbnail so a white ring or glyph reads on any photo. */
@Suppress("MagicNumber")
private val SCRIM = Color(0x99000000)

/** Tighter tracking is what separates a NAME from a label set in the same face. */
@Suppress("MagicNumber") // The tracking value IS the constant.
private val NAME_TRACKING = (-0.5).sp

private val HAIRLINE = 1.dp
private val BUBBLE_CORNER = 18.dp
private val BUBBLE_TAIL = 4.dp
private val BUBBLE_MAX_WIDTH = 300.dp
private const val PENDING_ALPHA = 0.6f
private val SEND_BUTTON = 40.dp
private val SEND_GLYPH = 18.dp
private val COMPOSER_GLYPH = 20.dp

/**
 * Stable per-sender name colours for group threads (98:396): the id hashes
 * into a fixed palette, so a sender keeps one colour for the whole thread.
 */
@Suppress("MagicNumber")
private val SENDER_PALETTE = listOf(
    Color(0xFFAB47BC),
    Color(0xFF22C55E),
    Color(0xFFFF6B35),
    Color(0xFF2196F3),
    Color(0xFF4ECDC4),
    Color(0xFFFFAB00),
)

private fun senderColor(seed: String): Color =
    SENDER_PALETTE[abs(seed.hashCode()) % SENDER_PALETTE.size]
