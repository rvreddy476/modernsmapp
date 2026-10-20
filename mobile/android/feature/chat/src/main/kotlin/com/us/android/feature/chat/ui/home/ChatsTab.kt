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
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.us.android.core.chat.data.Conversation
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.chat.ui.ConversationRow
import com.us.android.feature.chat.ui.InboxRowActions
import com.us.android.feature.chat.ui.OnlineNowRail

/**
 * Chats: the inbox narrowed to direct conversations — the Online Now rail,
 * the requests door when any wait, then the rows, filtered by the pill.
 */
@Composable
@Suppress("LongMethod", "LongParameterList")
internal fun ChatsTab(
    state: ChatHomeUiState,
    onOpenConversation: (String, String, Boolean) -> Unit,
    onOpenRequests: () -> Unit,
    onTogglePin: (Conversation) -> Unit,
    onToggleMute: (Conversation) -> Unit,
    // Chat parity (2026-10-18).
    rowActions: ((Conversation) -> InboxRowActions)? = null,
    notesActions: NotesActions? = null,
    onOpenPerson: (userId: String, name: String) -> Unit = { _, _ -> },
) {
    val chats = state.visibleChats
    val searching = state.query.isNotBlank()
    val search = state.search.takeIf { searching }
    when {
        state.loading && state.conversations.isEmpty() -> UsLoadingState(label = "Loading conversations")
        chats.isEmpty() && !state.showRequestsDoor && state.notes == null && search == null -> UsEmptyState(
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
            val notes = state.notes
            if (notes != null && notesActions != null && !searching) {
                item(key = "notes") {
                    NotesRow(
                        notes = notes,
                        viewerName = "You",
                        viewerId = state.viewerId,
                        saving = state.noteSaving,
                        actions = notesActions,
                    )
                }
            }
            val online = chats.filter { conversation -> conversation.isOnline(state) }
            if (online.isNotEmpty() && !searching && notes == null) {
                item(key = "online-now") {
                    OnlineNowRail(online = online, viewerId = state.viewerId, onOpen = onOpenConversation)
                }
            }
            if (state.showRequestsDoor && !searching) {
                item(key = "requests") {
                    RequestsDoor(
                        subtitle = requestsDoorSubtitle(state.requestCount, state.requestCountLabel, state.hiddenRequestCount),
                        onClick = onOpenRequests,
                    )
                }
            }
            if (search != null) {
                searchResultItems(
                    results = search,
                    viewerId = state.viewerId,
                    onOpenPerson = onOpenPerson,
                    onOpenConversation = onOpenConversation,
                    titleFor = { id -> state.conversations.firstOrNull { it.id == id }?.displayTitle(state.viewerId) },
                )
            }
            if (chats.isNotEmpty() && (search == null || search.conversations.isEmpty())) {
                item(key = "recent-label") { ChatSectionLabel(if (searching) "Chats" else "Recent chats") }
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
                        actions = rowActions?.invoke(conversation),
                    )
                }
            }
            if (searching && state.searching) {
                item(key = "searching") { ChatSectionLabel("Searching…") }
            }
        }
    }
}

private fun Conversation.isOnline(state: ChatHomeUiState): Boolean =
    peer?.isOnline == true ||
        members.any { it.userId != state.viewerId && it.userId in state.onlineUserIds }

/** The door to message requests: a raised row with the count in the accent. */
@Composable
private fun RequestsDoor(subtitle: String, onClick: () -> Unit) {
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
                tint = UsTheme.extended.onAccent,
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
                text = subtitle,
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
        }
        Icon(imageVector = UsIcons.ChevronRight, contentDescription = null, tint = UsTheme.extended.textMuted)
    }
}

/**
 * The requests door's second line. Waiting requests come first; when only
 * the safety filter's Hidden requests wait, the door still shows and says so
 * (founder, 2026-10-20), so they are never out of sight. Pure.
 */
internal fun requestsDoorSubtitle(count: Int, label: String, hidden: Int): String = when {
    count == 1 -> "1 person is waiting for your answer"
    count > 1 -> "$label people are waiting for your answer"
    hidden == 1 -> "1 hidden request"
    else -> "$hidden hidden requests"
}

private val ROW_GAP = 8.dp
private val ROW_MARGIN = 12.dp
private val DOOR_ICON = 40.dp
private val DOOR_GLYPH = 20.dp
internal val LIST_BOTTOM = 96.dp
