package com.us.android.feature.chat.ui.home

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.us.android.core.chat.data.Conversation
import com.us.android.core.common.time.formatRelativeTime
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsLoadingState

/**
 * Groups: the "New group" pill on top, then every group conversation —
 * the signed avatar when the server has one, initials otherwise, the
 * title, the last line with its sender, the unread dot — filtered by title
 * as the pill is typed. Empty is an invitation, not a blank.
 */
@Composable
internal fun GroupsTab(
    state: ChatHomeUiState,
    destinations: ChatHomeDestinations,
    onTogglePin: (Conversation) -> Unit,
    onToggleMute: (Conversation) -> Unit,
    rowActions: ((Conversation) -> com.us.android.feature.chat.ui.InboxRowActions)? = null,
) {
    val groups = state.visibleGroups
    val onOpenConversation = destinations.onOpenThread
    val onCreateGroup = destinations.onCreateGroup
    val onOpenInvitations = destinations.onOpenInvitations
    val onJoinWithLink = destinations.onJoinWithLink
    if (state.loading && state.conversations.isEmpty()) {
        UsLoadingState(label = "Loading groups")
        return
    }
    LazyColumn(
        modifier = Modifier.fillMaxSize().testTag("chat_home_groups"),
        verticalArrangement = Arrangement.spacedBy(ROW_GAP),
        contentPadding = PaddingValues(bottom = LIST_BOTTOM),
    ) {
        item(key = "actions") {
            Row(
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
                verticalAlignment = Alignment.CenterVertically,
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m),
            ) {
                ChatActionPill(
                    text = "New group",
                    icon = UsIcons.UserPlus,
                    onClick = onCreateGroup,
                    tag = "chat_home_new_group"
                )
                ChatTogglePill(
                    text = "Join with link",
                    selected = false,
                    onClick = onJoinWithLink,
                    tag = "chat_home_join_link_pill"
                )
                if (state.invitationCount > 0) {
                    ChatTogglePill(
                        text = "Invites · ${state.invitationCount}",
                        selected = false,
                        onClick = onOpenInvitations,
                        tag = "chat_home_invites_pill",
                    )
                }
            }
        }
        if (groups.isEmpty()) {
            item(
                key = "empty"
            ) { GroupsEmptyState(searching = state.query.isNotBlank(), onCreateGroup = onCreateGroup) }
        } else {
            item(key = "label") { ChatSectionLabel("Your groups") }
            items(groups, key = { it.id }) { group ->
                // Chat parity: the one inbox row, with the group's PHOTO, the
                // count pill and the long-press sheet the Chats tab has.
                val title = group.displayTitle(state.viewerId).ifBlank { "Group" }
                com.us.android.feature.chat.ui.ConversationRow(
                    conversation = group,
                    title = title,
                    viewerId = state.viewerId,
                    avatarUrl = state.groupAvatarUrls[group.id] ?: group.avatarUrl,
                    onClick = { onOpenConversation(group.id, group.title.orEmpty().ifBlank { "Group" }, true) },
                    onTogglePin = { onTogglePin(group) },
                    onToggleMute = { onToggleMute(group) },
                    actions = rowActions?.invoke(group),
                )
            }
        }
    }
}

@Composable
private fun GroupsEmptyState(searching: Boolean, onCreateGroup: () -> Unit) {
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = EMPTY_TOP)
            .testTag("chat_home_groups_empty"),
    ) {
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .size(EMPTY_ICON)
                .background(UsTheme.extended.launcher.chat.brush, RoundedCornerShape(EMPTY_ICON / ICON_CORNER_DIVISOR)),
        ) {
            Icon(
                imageVector = UsIcons.Friends,
                contentDescription = null,
                tint = UsTheme.extended.onTile,
                modifier = Modifier.size(EMPTY_GLYPH)
            )
        }
        Text(
            text = if (searching) "No groups match" else "Your groups will live here",
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textPrimary,
            textAlign = TextAlign.Center,
        )
        Text(
            text = if (searching) {
                "Try another group name."
            } else {
                "Bring your people together — plan things, share photos, keep one thread for the whole crew."
            },
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
            textAlign = TextAlign.Center,
        )
        if (!searching) {
            Spacer(Modifier.height(UsTheme.spacing.xs))
            ChatActionPill(
                text = "Start a group",
                icon = UsIcons.UserPlus,
                onClick = onCreateGroup,
                tag = "chat_home_groups_empty_pill"
            )
        }
    }
}

private val ROW_GAP = 8.dp
private val EMPTY_TOP = 40.dp
private val EMPTY_ICON = 72.dp
private val EMPTY_GLYPH = 34.dp
private const val ICON_CORNER_DIVISOR = 3
