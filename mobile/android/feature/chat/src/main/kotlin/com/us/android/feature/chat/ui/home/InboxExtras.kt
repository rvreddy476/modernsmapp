package com.us.android.feature.chat.ui.home

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.us.android.core.chat.data.ChatSearchResults
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsSettingsRadioRow

/** `who_can_see_online_status` values, in our words (CONTRACT §4, §9). */
internal val ACTIVITY_STATUS_OPTIONS = listOf(
    "everyone" to "Everyone",
    "connections_only" to "Your connections",
    "no_one" to "No one",
)

/** True when people can see you're active, for the header dot's colour. */
internal fun activityVisible(status: String?): Boolean = status != null && status != "no_one"

/**
 * "Messages" with the activity dot and ▾ (CONTRACT §9): green while others
 * can see when you're active, hollow when no one can. Tap opens the sheet.
 */
@Composable
internal fun ActivityStatusDot(status: String, onClick: () -> Unit) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        modifier = Modifier
            .pressScale(onClick)
            .semantics { contentDescription = "Activity status: " + labelForStatus(status) }
            .testTag("chat_activity_status"),
    ) {
        Box(
            modifier = Modifier
                .size(DOT)
                .background(
                    if (activityVisible(status)) UsTheme.extended.chatOnline else UsTheme.extended.textGhost,
                    CircleShape,
                ),
        )
        Icon(
            imageVector = UsIcons.ChevronDown,
            contentDescription = null,
            tint = UsTheme.extended.textMuted,
            modifier = Modifier.size(CHEVRON),
        )
    }
}

internal fun labelForStatus(status: String?): String =
    ACTIVITY_STATUS_OPTIONS.firstOrNull { it.first == status }?.second ?: "Your connections"

/** Who sees when you're active. Writes `who_can_see_online_status`. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun ActivityStatusSheet(status: String, onSelect: (String) -> Unit, onDismiss: () -> Unit) {
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgSheet,
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .navigationBarsPadding()
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(bottom = UsTheme.spacing.l)
                .testTag("chat_activity_sheet"),
        ) {
            Text("Activity status", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
            Text(
                "Choose who can see when you're active or were recently active. " +
                    "If you hide yours, you won't see theirs either.",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                modifier = Modifier.padding(vertical = UsTheme.spacing.s),
            )
            ACTIVITY_STATUS_OPTIONS.forEach { (value, label) ->
                UsSettingsRadioRow(
                    title = label,
                    selected = status == value,
                    onClick = {
                        onSelect(value)
                        onDismiss()
                    },
                    modifier = Modifier.testTag("chat_activity_option:$value"),
                )
            }
        }
    }
}

/**
 * The server's search answer (CONTRACT §6) as list sections: People (opens
 * or starts their chat), Chats, Messages (opens the thread).
 */
internal fun LazyListScope.searchResultItems(
    results: ChatSearchResults,
    viewerId: String,
    onOpenPerson: (userId: String, name: String) -> Unit,
    onOpenConversation: (conversationId: String, title: String, isGroup: Boolean) -> Unit,
    titleFor: (conversationId: String) -> String?,
) {
    if (results.people.isNotEmpty()) {
        item(key = "search-people-label") { ChatSectionLabel("People") }
        items(results.people, key = { "person:" + it.userId }) { person ->
            SearchRow(
                title = person.nameForDisplay,
                subtitle = person.username.takeIf { it.isNotBlank() }?.let { "@$it" },
                seed = person.userId,
                avatarUrl = person.avatarUrl,
                tag = "chat_search_person:${person.userId}",
                onClick = { onOpenPerson(person.userId, person.nameForDisplay) },
            )
        }
    }
    if (results.conversations.isNotEmpty()) {
        item(key = "search-chats-label") { ChatSectionLabel("Chats") }
        items(results.conversations, key = { "conversation:" + it.id }) { conversation ->
            val title = conversation.displayTitle(viewerId)
            SearchRow(
                title = title,
                subtitle = conversation.lastMessagePreview.takeIf { it.isNotBlank() },
                seed = conversation.id,
                avatarUrl = conversation.peer?.avatarUrl ?: conversation.avatarUrl,
                tag = "chat_search_chat:${conversation.id}",
                onClick = { onOpenConversation(conversation.id, title, conversation.type == ChatHomeFilters.GROUP_TYPE) },
            )
        }
    }
    if (results.messages.isNotEmpty()) {
        item(key = "search-messages-label") { ChatSectionLabel("Messages") }
        items(results.messages, key = { "message:" + it.conversationId + ":" + it.messageId }) { hit ->
            val title = titleFor(hit.conversationId) ?: "Conversation"
            SearchRow(
                title = title,
                subtitle = hit.snippet,
                seed = hit.conversationId,
                avatarUrl = null,
                tag = "chat_search_message:${hit.messageId}",
                onClick = { onOpenConversation(hit.conversationId, title, false) },
            )
        }
    }
}

@Suppress("LongParameterList")
@Composable
private fun SearchRow(
    title: String,
    subtitle: String?,
    seed: String,
    avatarUrl: String?,
    tag: String,
    onClick: () -> Unit,
) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        modifier = Modifier
            .fillMaxWidth()
            .background(Color.Transparent)
            .pressScale(onClick)
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.s)
            .testTag(tag),
    ) {
        UsAvatar(name = title, size = UsAvatarSize.Medium, seed = seed, imageUrl = avatarUrl)
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = title,
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            subtitle?.let {
                Text(
                    text = it,
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}

private val DOT = 10.dp
private val CHEVRON = 16.dp
