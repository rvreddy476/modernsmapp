package com.us.android.feature.chat.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.chat.data.Conversation
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState

/**
 * The chat inbox.
 *
 * Layout only. Loading, empty and error presentation come from :core:ui, and
 * the avatar and scaffold from :core:designsystem — the same pieces the
 * comments surfaces use. What is NOT shared is the data underneath, which is
 * chat's own controller and transport.
 */
@Composable
fun ChatInboxScreen(
    onOpenConversation: (conversationId: String, title: String) -> Unit,
    onBack: () -> Unit,
    viewModel: ChatInboxViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    UsScaffold(
        topBar = { UsTopBar(title = "Messages", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Column(modifier = Modifier.padding(padding)) {
            when {
                state.loading && state.conversations.isEmpty() ->
                    UsLoadingState(label = "Loading conversations")

                // Only when nothing is on screen: a refresh failure over a
                // loaded inbox keeps the rows and the error stays quiet.
                state.error != null && state.conversations.isEmpty() -> UsErrorState(
                    message = "We couldn't load your messages.",
                    onRetry = viewModel::refresh,
                )

                state.isEmpty -> UsEmptyState(
                    title = "No conversations yet",
                    detail = "Messages from people you can chat with show up here.",
                )

                else -> LazyColumn(modifier = Modifier.fillMaxSize()) {
                    items(state.conversations, key = { it.id }) { conversation ->
                        // Resolved HERE and handed to the thread, because
                        // naming a direct conversation needs the viewer's own
                        // id to pick the other member. The thread does not
                        // have it, and re-deriving it there would open every
                        // direct chat with a blank title until its member list
                        // loaded.
                        val title = conversation.displayTitle(state.viewerId)
                        ConversationRow(
                            conversation = conversation,
                            title = title,
                            onClick = { onOpenConversation(conversation.id, title) },
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun ConversationRow(
    conversation: Conversation,
    title: String,
    onClick: () -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(
                horizontal = UsTheme.spacing.pageHorizontal,
                vertical = UsTheme.spacing.l,
            ),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        UsAvatar(name = title, size = UsAvatarSize.Medium, seed = conversation.id)
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = title,
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            // A pending message request is labelled rather than hidden: the
            // viewer should know why a stranger is in their inbox.
            if (conversation.isRequest) {
                Text(
                    text = "Message request",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                )
            }
        }
    }
}
