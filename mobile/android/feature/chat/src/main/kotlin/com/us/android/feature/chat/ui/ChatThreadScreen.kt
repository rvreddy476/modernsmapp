package com.us.android.feature.chat.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.chat.data.MAX_MESSAGE_LENGTH
import com.us.android.core.chat.data.Message
import com.us.android.core.chat.data.ThreadUiState
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState

/**
 * One conversation.
 *
 * Newest-first, like the server returns it, rendered in a reversed list so the
 * newest message sits at the bottom without re-sorting on every frame.
 */
@Composable
fun ChatThreadScreen(
    title: String,
    onBack: () -> Unit,
    viewModel: ChatThreadViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val listState = rememberLazyListState()

    // Paging is driven by what is visible rather than a button, keyed on the
    // LAST index so it fires once per page instead of on every scrolled pixel.
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

    // Follow the conversation when a new message arrives.
    //
    // Without this the socket delivers correctly and the reader never sees it:
    // a prepended row lands at index 0, which `reverseLayout` puts at the
    // visual BOTTOM, below whatever is on screen. A live device run showed
    // exactly that — two messages arrived, both rendered, and the screen did
    // not move. "Delivered but invisible" is indistinguishable from "not
    // delivered" to the person holding the phone.
    //
    // Only when the reader is ALREADY at the newest end. Yanking someone who
    // has scrolled back through history to the bottom every time a message
    // lands is the other half of this bug, and the more annoying half. The
    // threshold is 1 rather than 0 because the arriving message has already
    // shifted the previously-visible row from index 0 to index 1 by the time
    // this runs.
    val newestId = state.messages.firstOrNull()?.id
    LaunchedEffect(newestId) {
        if (newestId != null && listState.firstVisibleItemIndex <= FOLLOW_THRESHOLD) {
            listState.animateScrollToItem(0)
        }
    }

    UsScaffold(
        topBar = { UsTopBar(title = title, onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Column(modifier = Modifier.padding(padding).fillMaxSize()) {
            Box(modifier = Modifier.weight(1f)) {
                when {
                    state.loading && state.messages.isEmpty() ->
                        UsLoadingState(label = "Loading messages")

                    state.refreshError != null && state.messages.isEmpty() -> UsErrorState(
                        message = "We couldn't load this conversation.",
                        onRetry = viewModel::refresh,
                    )

                    state.messages.isEmpty() -> UsEmptyState(
                        title = "No messages yet",
                        detail = "Say something to start the conversation.",
                    )

                    else -> LazyColumn(
                        state = listState,
                        // The newest message is at index 0 and the list is
                        // reversed, so it renders at the bottom where a reader
                        // expects it — without sorting the list backwards.
                        reverseLayout = true,
                        modifier = Modifier.fillMaxSize(),
                    ) {
                        items(state.messages, key = { it.id }) { message ->
                            MessageRow(message)
                        }
                    }
                }
            }

            ComposerStatus(state = state, onRetrySend = viewModel::send)

            Composer(
                draft = state.draft,
                canSend = state.canSend,
                onDraftChange = viewModel::onDraftChange,
                onSend = viewModel::send,
            )
        }
    }
}

/**
 * The three things that can sit between the thread and the composer.
 *
 * Grouped here rather than inline so the screen reads as list → status →
 * composer. All three are transient and mutually compatible: someone can be
 * typing while your last send is still failing.
 */
@Composable
private fun ComposerStatus(
    state: ThreadUiState,
    onRetrySend: () -> Unit,
) {
    // Cleared by the ViewModel's per-user 3s timer, matching the server's
    // typing key TTL. There is no stop frame on the wire, so without that
    // timer this line would never go away.
    if (state.typingUserIds.isNotEmpty()) {
        Text(
            text = "Typing…",
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
            modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal),
        )
    }

    // The cap is the server's (2,000). Saying so before Send is pressed is the
    // difference between an explanation and a 400 with no field name in it.
    if (state.draftTooLong) {
        Text(
            text = "That's longer than a message can be. " +
                "The limit is $MAX_MESSAGE_LENGTH characters.",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.error,
            modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal),
        )
    }

    if (state.sendError != null) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = UsTheme.spacing.pageHorizontal),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            Text(
                text = "Message not sent.",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
                modifier = Modifier.weight(1f),
            )
            // Retry re-sends under the SAME idempotency key, so a request that
            // did reach the server replays instead of posting a second message.
            UsSecondaryButton(text = "Retry", onClick = onRetrySend)
        }
    }
}

@Composable
private fun Composer(
    draft: String,
    canSend: Boolean,
    onDraftChange: (String) -> Unit,
    onSend: () -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(UsTheme.spacing.l),
        verticalAlignment = Alignment.Bottom,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        UsTextField(
            value = draft,
            onValueChange = onDraftChange,
            label = "Message",
            placeholder = "Write a message…",
            singleLine = false,
            modifier = Modifier.weight(1f),
        )
        // Disabled rather than hidden: a control that vanishes as you type
        // reads as a glitch, and its absence gives no reason.
        UsSecondaryButton(text = "Send", onClick = onSend, enabled = canSend)
    }
}

@Composable
private fun MessageRow(message: Message) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(
                horizontal = UsTheme.spacing.pageHorizontal,
                vertical = UsTheme.spacing.s,
            ),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        val name = message.senderDisplayName.orEmpty()
        UsAvatar(
            name = name.ifBlank { "?" },
            size = UsAvatarSize.Small,
            seed = message.senderId.ifBlank { message.id },
        )
        Column(modifier = Modifier.weight(1f)) {
            if (name.isNotBlank()) {
                Text(
                    text = name,
                    style = MaterialTheme.typography.labelMedium,
                    fontWeight = FontWeight.SemiBold,
                    color = UsTheme.extended.textPrimary,
                )
            }
            Text(
                text = message.text,
                style = MaterialTheme.typography.bodyMedium,
                // A message still in flight is dimmed rather than badged: it
                // is almost always resolved before anyone reads a badge.
                color = if (message.pending) {
                    UsTheme.extended.textMuted
                } else {
                    UsTheme.extended.textPrimary
                },
            )
        }
    }
}

private const val PREFETCH_DISTANCE = 3

/** How near the newest end the reader must be for the thread to follow along. */
private const val FOLLOW_THRESHOLD = 1
