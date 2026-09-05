package com.us.android.feature.chat.ui.home

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.us.android.core.chat.data.Conversation
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.chat.ui.ConversationRow
import com.us.android.feature.chat.ui.OnlineNowRail

/**
 * Chats: the inbox narrowed to direct conversations — the Online Now rail,
 * the requests door when any wait, then the rows, filtered by the pill.
 */
@Composable
internal fun ChatsTab(
    state: ChatHomeUiState,
    onOpenConversation: (String, String, Boolean) -> Unit,
    onOpenRequests: () -> Unit,
    onTogglePin: (Conversation) -> Unit,
    onToggleMute: (Conversation) -> Unit,
) {
    val chats = state.visibleChats
    when {
        state.loading && state.conversations.isEmpty() -> UsLoadingState(label = "Loading conversations")
        chats.isEmpty() && state.requests.isEmpty() -> UsEmptyState(
            title = if (state.query.isBlank()) "No chats yet" else "No chats match",
            detail = if (state.query.isBlank()) {
                "Start from Suggestions — people you follow can message you here."
            } else {
                "Try another name or a word from the message."
            },
        )
        else -> LazyColumn(
            modifier = Modifier.fillMaxSize().testTag("chat_home_chats"),
            verticalArrangement = Arrangement.spacedBy(ROW_GAP),
            contentPadding = PaddingValues(bottom = LIST_BOTTOM),
        ) {
            val online = chats.filter { conversation -> conversation.isOnline(state) }
            if (online.isNotEmpty() && state.query.isBlank()) {
                item(key = "online-now") {
                    OnlineNowRail(online = online, viewerId = state.viewerId, onOpen = onOpenConversation)
                }
            }
            if (state.requests.isNotEmpty() && state.query.isBlank()) {
                item(key = "requests") { RequestsDoor(count = state.requests.size, onClick = onOpenRequests) }
            }
            item(key = "recent-label") { ChatSectionLabel("Recent chats") }
            items(chats, key = { it.id }) { conversation ->
                val title = conversation.displayTitle(state.viewerId)
                ConversationRow(
                    conversation = conversation,
                    title = title,
                    viewerId = state.viewerId,
                    online = conversation.isOnline(state),
                    onClick = { onOpenConversation(conversation.id, title, false) },
                    onTogglePin = { onTogglePin(conversation) },
                    onToggleMute = { onToggleMute(conversation) },
                )
            }
        }
    }
}

private fun Conversation.isOnline(state: ChatHomeUiState): Boolean =
    members.any { it.userId != state.viewerId && it.userId in state.onlineUserIds }

/** The door to message requests: a raised row with the count in the accent. */
@Composable
private fun RequestsDoor(count: Int, onClick: () -> Unit) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = ROW_MARGIN)
            .background(UsTheme.extended.bgRaised, RoundedCornerShape(UsTheme.radii.panel))
            .pressScale(onClick)
            .padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.l)
            .testTag("chat_home_requests_door"),
    ) {
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .size(DOOR_ICON)
                .background(UsTheme.extended.ctaGradient, RoundedCornerShape(UsTheme.radii.medium)),
        ) {
            Icon(
                imageVector = UsIcons.UserPlus,
                contentDescription = null,
                tint = Color.White,
                modifier = Modifier.size(DOOR_GLYPH)
            )
        }
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = "Message requests",
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
            )
            Text(
                text = if (count == 1) {
                    "1 person is waiting for your answer"
                } else {
                    "$count people are waiting for your answer"
                },
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
        }
        Icon(imageVector = UsIcons.ChevronRight, contentDescription = null, tint = UsTheme.extended.textMuted)
    }
}

private val ROW_GAP = 8.dp
private val ROW_MARGIN = 12.dp
private val DOOR_ICON = 40.dp
private val DOOR_GLYPH = 20.dp
internal val LIST_BOTTOM = 96.dp
