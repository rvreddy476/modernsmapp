package com.us.android.feature.chat.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Badge
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Tab
import androidx.compose.material3.TabRow
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.chat.data.Conversation
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsLoadingState

/**
 * The chat inbox: Chats, Requests and group Invites (directive §5.5).
 *
 * Rows render from the durable cache, so the list appears instantly and
 * offline; a sync failure keeps the rows and shows a quiet banner instead of
 * blanking someone's inbox.
 */
@Composable
fun ChatInboxScreen(
    onOpenConversation: (conversationId: String, title: String, isGroup: Boolean) -> Unit,
    onOpenRequest: (conversationId: String, title: String) -> Unit,
    onCreateGroup: () -> Unit,
    onOpenLockSettings: () -> Unit = {},
    onBack: () -> Unit,
    viewModel: ChatInboxViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    UsScaffold(
        topBar = {
            UsTopBar(
                title = "Messages",
                onBack = onBack,
                actions = {
                    TextButton(
                        onClick = onOpenLockSettings,
                        modifier = Modifier.testTag("chat-lock-settings"),
                    ) { Text("Lock") }
                    TextButton(
                        onClick = onCreateGroup,
                        modifier = Modifier.testTag("chat-new-group"),
                    ) { Text("New group") }
                },
            )
        },
        applyPageGutter = false,
    ) { padding ->
        Column(modifier = Modifier.padding(padding)) {
            InboxTabs(state = state, onSelect = viewModel::selectTab)

            val nothingCached = state.conversations.isEmpty() && state.requests.isEmpty()
            if (state.syncFailed && nothingCached && state.tab != InboxTab.Invites) {
                OfflineBanner(onRetry = viewModel::refresh)
            }

            when (state.tab) {
                InboxTab.Chats -> ChatsTab(
                    state = state,
                    onOpenConversation = onOpenConversation,
                    onTogglePin = viewModel::togglePin,
                    onToggleMute = viewModel::toggleMute,
                )
                InboxTab.Requests -> RequestsTab(state, onOpenRequest)
                InboxTab.Invites -> InvitesTab(
                    state = state,
                    onAccept = viewModel::acceptInvitation,
                    onDecline = viewModel::declineInvitation,
                )
            }
        }
    }
}

@Composable
private fun InboxTabs(state: InboxUiState, onSelect: (InboxTab) -> Unit) {
    TabRow(selectedTabIndex = state.tab.ordinal) {
        Tab(
            selected = state.tab == InboxTab.Chats,
            onClick = { onSelect(InboxTab.Chats) },
            text = { Text("Chats") },
            modifier = Modifier.testTag("chat-tab-chats"),
        )
        Tab(
            selected = state.tab == InboxTab.Requests,
            onClick = { onSelect(InboxTab.Requests) },
            text = { TabLabelWithCount("Requests", state.requestCount) },
            modifier = Modifier.testTag("chat-tab-requests"),
        )
        Tab(
            selected = state.tab == InboxTab.Invites,
            onClick = { onSelect(InboxTab.Invites) },
            text = { TabLabelWithCount("Invites", state.inviteCount) },
            modifier = Modifier.testTag("chat-tab-invites"),
        )
    }
}

@Composable
private fun TabLabelWithCount(label: String, count: Int) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Text(label)
        if (count > 0) {
            Badge { Text(count.toString()) }
        }
    }
}

@Composable
private fun OfflineBanner(onRetry: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .background(MaterialTheme.colorScheme.surfaceVariant)
            .padding(
                horizontal = UsTheme.spacing.pageHorizontal,
                vertical = UsTheme.spacing.s,
            ),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            "Couldn't refresh. Showing what's saved on this device.",
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
            modifier = Modifier.weight(1f),
        )
        TextButton(onClick = onRetry) { Text("Retry") }
    }
}

@Composable
private fun ChatsTab(
    state: InboxUiState,
    onOpenConversation: (String, String, Boolean) -> Unit,
    onTogglePin: (Conversation) -> Unit,
    onToggleMute: (Conversation) -> Unit,
) {
    when {
        state.loading && state.conversations.isEmpty() ->
            UsLoadingState(label = "Loading conversations")

        state.conversations.isEmpty() -> UsEmptyState(
            title = "No conversations yet",
            detail = "Messages from people you can chat with show up here.",
        )

        else -> LazyColumn(modifier = Modifier.fillMaxSize()) {
            items(state.conversations, key = { it.id }) { conversation ->
                val title = conversation.displayTitle(state.viewerId)
                ConversationRow(
                    conversation = conversation,
                    title = title,
                    onClick = { onOpenConversation(conversation.id, title, conversation.type == "group") },
                    onTogglePin = { onTogglePin(conversation) },
                    onToggleMute = { onToggleMute(conversation) },
                )
            }
        }
    }
}

@Composable
private fun RequestsTab(
    state: InboxUiState,
    onOpenRequest: (String, String) -> Unit,
) {
    if (state.requests.isEmpty()) {
        UsEmptyState(
            title = "No message requests",
            detail = "When someone outside your connections messages you, it waits here for your decision.",
        )
        return
    }
    LazyColumn(modifier = Modifier.fillMaxSize()) {
        items(state.requests, key = { it.id }) { request ->
            val title = request.displayTitle(state.viewerId)
            ConversationRow(
                conversation = request,
                title = title,
                onClick = { onOpenRequest(request.id, title) },
            )
        }
    }
}

@Composable
private fun InvitesTab(
    state: InboxUiState,
    onAccept: (String) -> Unit,
    onDecline: (String) -> Unit,
) {
    if (state.invitations.isEmpty()) {
        UsEmptyState(
            title = "No group invites",
            detail = "Groups that need your consent to join appear here.",
        )
        return
    }
    LazyColumn(modifier = Modifier.fillMaxSize()) {
        items(state.invitations, key = { it.id }) { invitation ->
            InvitationRow(
                busy = invitation.id in state.busyInvitationIds,
                onAccept = { onAccept(invitation.id) },
                onDecline = { onDecline(invitation.id) },
            )
        }
    }
}

@Composable
private fun InvitationRow(
    busy: Boolean,
    onAccept: () -> Unit,
    onDecline: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(
                horizontal = UsTheme.spacing.pageHorizontal,
                vertical = UsTheme.spacing.m,
            ),
    ) {
        Text(
            "You've been invited to a group",
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
        )
        Text(
            "Joining shares your messages in this group with its members.",
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
        )
        Spacer(Modifier.size(UsTheme.spacing.s))
        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            UsSecondaryButton(
                text = "Decline",
                onClick = onDecline,
                enabled = !busy,
                modifier = Modifier.weight(1f),
            )
            com.us.android.core.designsystem.component.UsButton(
                text = if (busy) "Working…" else "Join group",
                onClick = onAccept,
                enabled = !busy,
                modifier = Modifier.weight(1f),
            )
        }
    }
}

@Composable
@Suppress("LongMethod")
private fun ConversationRow(
    conversation: Conversation,
    title: String,
    onClick: () -> Unit,
    onTogglePin: () -> Unit = {},
    onToggleMute: () -> Unit = {},
) {
    var menuOpen by remember { mutableStateOf(false) }
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .combinedClickable(onClick = onClick, onLongClick = { menuOpen = true })
            .padding(
                horizontal = UsTheme.spacing.pageHorizontal,
                vertical = UsTheme.spacing.l,
            ),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        UsAvatar(name = title, size = UsAvatarSize.Medium, seed = conversation.id)
        Column(modifier = Modifier.weight(1f)) {
            val markers = buildString {
                if (conversation.isPinned) append("📌 ")
                if (conversation.isMuted) append("🔕 ")
            }
            Text(
                text = markers + title,
                style = MaterialTheme.typography.titleSmall,
                fontWeight = if (conversation.hasUnread) FontWeight.Bold else FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                DropdownMenuItem(
                    text = { Text(if (conversation.isPinned) "Unpin" else "Pin") },
                    onClick = {
                        menuOpen = false
                        onTogglePin()
                    },
                )
                DropdownMenuItem(
                    text = { Text(if (conversation.isMuted) "Unmute" else "Mute") },
                    onClick = {
                        menuOpen = false
                        onToggleMute()
                    },
                )
            }
            val preview = when {
                conversation.isRequest -> "Message request"
                conversation.lastMessagePreview.isNotBlank() -> conversation.lastMessagePreview
                else -> null
            }
            preview?.let {
                Text(
                    text = it,
                    style = MaterialTheme.typography.bodySmall,
                    fontWeight = if (conversation.hasUnread) FontWeight.SemiBold else FontWeight.Normal,
                    color = if (conversation.hasUnread) {
                        UsTheme.extended.textPrimary
                    } else {
                        UsTheme.extended.textMuted
                    },
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        if (conversation.hasUnread) {
            Box(
                modifier = Modifier
                    .size(UNREAD_DOT)
                    .clip(CircleShape)
                    .background(MaterialTheme.colorScheme.primary)
                    .testTag("chat-unread-dot"),
            )
        }
    }
}

private val UNREAD_DOT = 10.dp
