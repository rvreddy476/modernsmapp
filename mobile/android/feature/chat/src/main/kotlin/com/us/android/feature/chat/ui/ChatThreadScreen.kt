package com.us.android.feature.chat.ui

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
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
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import coil3.compose.AsyncImage
import com.us.android.core.chat.data.MAX_MESSAGE_LENGTH
import com.us.android.core.chat.data.Message
import com.us.android.core.chat.data.PendingSend
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.network.ApiConfig
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState

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
    viewModel: ChatThreadViewModel = hiltViewModel(),
) {
    val render by viewModel.state.collectAsStateWithLifecycle()
    val state = render.thread
    val listState = rememberLazyListState()

    val pickImage = rememberLauncherForActivityResult(
        ActivityResultContracts.PickVisualMedia(),
    ) { uri -> uri?.let(viewModel::sendAttachment) }

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
            UsTopBar(
                // A deep-linked thread navigates with a blank title; the
                // loaded conversation supplies the real one.
                title = title.ifBlank { render.loadedTitle.ifBlank { "Conversation" } },
                onBack = onBack,
                actions = {
                    if (isGroup || render.loadedIsGroup) {
                        TextButton(
                            onClick = onOpenGroupInfo,
                            modifier = Modifier.testTag("thread-group-info"),
                        ) { Text("Info") }
                    }
                },
            )
        },
        applyPageGutter = false,
    ) { padding ->
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
                        items(state.messages, key = { it.id }) { message ->
                            MessageRow(
                                message = message,
                                isOwn = message.senderId == render.viewerId,
                                readByPeer = message.id == state.peerLastReadMessageId,
                                onReact = { emoji ->
                                    viewModel.toggleReaction(message.id, emoji)
                                },
                                onDelete = { viewModel.deleteMessage(message.id) },
                            )
                        }
                    }
                }
            }

            ComposerStatus(render = render, onCancelAttachment = viewModel::cancelAttachment)

            Composer(
                draft = state.draft,
                canSend = state.canSend,
                attaching = render.attachmentUploading,
                onDraftChange = {
                    viewModel.onDraftChange(it)
                    viewModel.sendTyping()
                },
                onSend = viewModel::send,
                onAttach = {
                    pickImage.launch(
                        PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly),
                    )
                },
            )
        }
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

@Composable
private fun ComposerStatus(
    render: ThreadRenderState,
    onCancelAttachment: () -> Unit,
) {
    val state = render.thread
    if (state.typingUserIds.isNotEmpty()) {
        Text(
            text = "Typing…",
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
            modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal),
        )
    }

    if (state.draftTooLong) {
        Text(
            text = "That's longer than a message can be. " +
                "The limit is $MAX_MESSAGE_LENGTH characters.",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.error,
            modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal),
        )
    }

    if (render.attachmentUploading) {
        Row(
            modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = "Uploading photo… ${render.attachmentProgressPercent}%",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                modifier = Modifier.weight(1f).testTag("attachment-progress"),
            )
            TextButton(
                onClick = onCancelAttachment,
                modifier = Modifier.testTag("attachment-cancel"),
            ) { Text("Cancel") }
        }
    }
    render.attachmentError?.let {
        Text(
            text = it,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.error,
            modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal),
        )
    }
}

@Composable
private fun Composer(
    draft: String,
    canSend: Boolean,
    attaching: Boolean,
    onDraftChange: (String) -> Unit,
    onSend: () -> Unit,
    onAttach: () -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(UsTheme.spacing.l),
        verticalAlignment = Alignment.Bottom,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        UsSecondaryButton(
            text = "Photo",
            onClick = onAttach,
            enabled = !attaching,
            modifier = Modifier.testTag("thread-attach"),
        )
        UsTextField(
            value = draft,
            onValueChange = onDraftChange,
            label = "Message",
            placeholder = "Write a message…",
            singleLine = false,
            modifier = Modifier.weight(1f),
        )
        UsSecondaryButton(text = "Send", onClick = onSend, enabled = canSend)
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
        Text(
            text = if (pending.mediaId != null) "📷 Photo" else pending.text,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
        )
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
@Suppress("LongMethod")
private fun MessageRow(
    message: Message,
    isOwn: Boolean,
    readByPeer: Boolean,
    onReact: (String) -> Unit,
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
        if (!isOwn) {
            UsAvatar(
                name = name.ifBlank { "?" },
                size = UsAvatarSize.Small,
                seed = message.senderId.ifBlank { message.id },
            )
        }
        Column(
            modifier = Modifier.weight(1f, fill = false),
            horizontalAlignment = if (isOwn) Alignment.End else Alignment.Start,
        ) {
            if (name.isNotBlank() && !isOwn) {
                Text(
                    text = name,
                    style = MaterialTheme.typography.labelMedium,
                    fontWeight = FontWeight.SemiBold,
                    color = UsTheme.extended.textPrimary,
                )
            }
            message.mediaId?.let { mediaId ->
                AttachmentImage(mediaId = mediaId)
            }
            if (message.text.isNotBlank()) {
                Text(
                    text = message.text,
                    style = MaterialTheme.typography.bodyMedium,
                    color = if (message.pending) {
                        UsTheme.extended.textMuted
                    } else {
                        UsTheme.extended.textPrimary
                    },
                )
            }
            if (message.reactions.isNotEmpty()) {
                Text(
                    text = message.reactions.joinToString(" ") { "${it.emoji} ${it.userIds.size}" },
                    style = MaterialTheme.typography.labelMedium,
                    color = UsTheme.extended.textMuted,
                    modifier = Modifier.testTag("message-reactions"),
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
                REACTION_CHOICES.forEach { emoji ->
                    DropdownMenuItem(
                        text = { Text(emoji) },
                        onClick = {
                            menuOpen = false
                            onReact(emoji)
                        },
                    )
                }
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
private val REACTION_CHOICES = listOf("❤️", "👍", "😂")
