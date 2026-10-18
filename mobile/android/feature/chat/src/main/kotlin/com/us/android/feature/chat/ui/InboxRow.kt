package com.us.android.feature.chat.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.us.android.core.chat.data.Conversation
import com.us.android.core.chat.data.ownStatusLabel
import com.us.android.core.chat.data.streakLabel
import com.us.android.core.chat.data.unreadPillText
import com.us.android.core.common.time.formatRelativeTime
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme

/**
 * What a long-press on an inbox row may do (CONTRACT §9). A null action is
 * not offered — Archive and Delete chat only appear where the screen can
 * carry them out.
 */
data class InboxRowActions(
    val onTogglePin: () -> Unit,
    val onToggleMute: () -> Unit,
    val onArchive: (() -> Unit)? = null,
    val onDelete: (() -> Unit)? = null,
    val onMarkRead: (() -> Unit)? = null,
    val onMarkUnread: (() -> Unit)? = null,
    /** Archived list: "Unarchive" in place of "Archive". */
    val archived: Boolean = false,
)

/**
 * One inbox row (chat parity, 2026-10-18): the PHOTO avatar with its online
 * dot, the name with pin and mute glyphs (icons, never emoji), the time, the
 * preview with "Seen" when the peer read the viewer's last message, the
 * streak flame and count, and the unread count pill. A long-press opens the
 * row's actions as a sheet.
 *
 * The card-on-unread rule of the Figma inbox (136:144) is kept: rows that
 * want something lift off the page, settled ones lie flat.
 */
@Composable
@Suppress("LongMethod", "LongParameterList", "CyclomaticComplexMethod")
internal fun ConversationRow(
    conversation: Conversation,
    title: String,
    viewerId: String,
    onClick: () -> Unit,
    online: Boolean = false,
    onTogglePin: () -> Unit = {},
    onToggleMute: () -> Unit = {},
    avatarUrl: String? = null,
    actions: InboxRowActions? = null,
) {
    var sheetOpen by remember { mutableStateOf(false) }
    val isOnline = online || conversation.peer?.isOnline == true
    val photo = avatarUrl ?: conversation.peer?.avatarUrl
        ?: conversation.sender?.takeUnless { it.deleted }?.avatarUrl ?: conversation.avatarUrl
    val unread = unreadPillText(conversation.unreadCount, conversation.hasUnread)
    // Fix pass 2: a row with actions also swipes; the swipe wrapper then owns
    // the margin and the rounded clip so the revealed buttons share the shape.
    val swipe = actions != null
    var confirmDeleteFirst by remember { mutableStateOf(false) }
    val body: @Composable () -> Unit = {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = if (swipe) 0.dp else ROW_MARGIN)
                .clip(RoundedCornerShape(if (swipe) 0.dp else ROW_CORNER))
                .background(if (conversation.hasUnread) UsTheme.extended.bgCardSolid else Color.Transparent)
                .combinedClickable(onClick = onClick, onLongClick = { sheetOpen = true })
                .padding(ROW_PADDING)
                .testTag("chat-row:${conversation.id}"),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(ROW_PADDING),
        ) {
            Box {
                UsAvatar(name = title, size = UsAvatarSize.Chat, seed = conversation.id, imageUrl = photo)
                if (isOnline) {
                    Box(
                        modifier = Modifier
                            .align(Alignment.BottomEnd)
                            .size(ONLINE_DOT)
                            .clip(CircleShape)
                            .background(UsTheme.extended.chatOnline)
                            .border(ONLINE_DOT_RING, UsTheme.extended.bgCanvas, CircleShape)
                            .testTag("chat-online-dot"),
                    )
                }
            }
            Column(modifier = Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(NAME_PREVIEW_GAP)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    // Name and its glyphs take all the space the time leaves,
                    // so the time always sits at the end and a long name only
                    // truncates against it (two weighted children used to
                    // split the row in half).
                    Row(modifier = Modifier.weight(1f), verticalAlignment = Alignment.CenterVertically) {
                        Text(
                            text = title,
                            style = MaterialTheme.typography.titleSmall,
                            fontWeight = FontWeight.Bold,
                            color = UsTheme.extended.textPrimary,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                            modifier = Modifier.weight(1f, fill = false),
                        )
                        if (conversation.isPinned) RowGlyph(UsIcons.Pin, "Pinned", "chat-row-pinned")
                        if (conversation.isMuted) RowGlyph(UsIcons.NotificationsOff, "Muted", "chat-row-muted")
                    }
                    val time = formatRelativeTime(conversation.lastMessage?.at?.takeIf { it.isNotBlank() }
                        ?: conversation.lastMessageAt ?: conversation.updatedAt)
                    if (time.isNotBlank()) {
                        Text(
                            text = time,
                            style = MaterialTheme.typography.labelSmall,
                            color = if (conversation.hasUnread) UsTheme.extended.chatAccentText else UsTheme.extended.textMuted,
                            maxLines = 1,
                        )
                    }
                }
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Box(modifier = Modifier.weight(1f)) {
                        val seen = ownStatusLabel(conversation.lastMessage, viewerId)
                        if (seen != null && !conversation.hasUnread) {
                            Text(
                                text = seen,
                                style = MaterialTheme.typography.bodySmall,
                                color = UsTheme.extended.textMuted,
                                maxLines = 1,
                                modifier = Modifier.testTag("chat-row-status"),
                            )
                        } else {
                            ConversationPreview(conversation = conversation, viewerId = viewerId)
                        }
                    }
                    streakLabel(conversation.streak)?.let { days -> StreakBadge(days, conversation.streak?.atRisk == true) }
                }
            }
            if (unread != null) UnreadPill(unread)
        }
    }
    if (actions != null) {
        SwipeableInboxRow(
            start = listOf(
                SwipeAction(
                    icon = UsIcons.Pin,
                    label = if (conversation.isPinned) "Unpin" else "Pin",
                    tag = "chat-row-swipe-pin",
                    fill = UsTheme.extended.chatAccent,
                    ink = UsTheme.extended.onChatAccent,
                    onClick = actions.onTogglePin,
                ),
            ),
            end = buildList {
                val readToggle = if (conversation.hasUnread) actions.onMarkRead else actions.onMarkUnread
                readToggle?.let {
                    add(
                        SwipeAction(
                            icon = if (conversation.hasUnread) UsIcons.CheckCheck else UsIcons.Circle,
                            label = if (conversation.hasUnread) "Read" else "Unread",
                            tag = "chat-row-swipe-read",
                            fill = UsTheme.extended.statusInfo,
                            ink = UsTheme.extended.onStatus,
                            onClick = it,
                        ),
                    )
                }
                if (actions.onDelete != null) {
                    add(
                        SwipeAction(
                            icon = UsIcons.Trash,
                            label = "Delete",
                            tag = "chat-row-swipe-delete",
                            fill = UsTheme.extended.statusDanger,
                            ink = UsTheme.extended.onStatus,
                            // Deleting is never one gesture: the confirm sheet asks first.
                            onClick = {
                                confirmDeleteFirst = true
                                sheetOpen = true
                            },
                        ),
                    )
                }
            },
        ) { body() }
    } else {
        body()
    }

    if (sheetOpen) {
        ConversationActionsSheet(
            conversation = conversation,
            title = title,
            actions = actions ?: InboxRowActions(onTogglePin = onTogglePin, onToggleMute = onToggleMute),
            startWithDeleteConfirm = confirmDeleteFirst,
            onDismiss = {
                sheetOpen = false
                confirmDeleteFirst = false
            },
        )
    }
}

/** A pin or mute glyph beside the name: 14dp, muted, with words for a screen reader. */
@Composable
private fun RowGlyph(icon: ImageVector, description: String, tag: String) {
    Icon(
        imageVector = icon,
        contentDescription = description,
        tint = UsTheme.extended.textMuted,
        modifier = Modifier
            .padding(start = UsTheme.spacing.xs)
            .size(ROW_GLYPH)
            .testTag(tag),
    )
}

/** The streak: a flame and the day count; the flame turns to the danger colour when the streak is at risk. */
@Composable
internal fun StreakBadge(days: String, atRisk: Boolean) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier
            .padding(start = UsTheme.spacing.s)
            .semantics { contentDescription = "$days day streak" + if (atRisk) ", ending soon" else "" }
            .testTag("chat-row-streak"),
    ) {
        Icon(
            imageVector = UsIcons.Flame,
            contentDescription = null,
            tint = if (atRisk) UsTheme.extended.statusDanger else UsTheme.extended.accentSolid,
            modifier = Modifier.size(ROW_GLYPH),
        )
        Text(
            text = days,
            style = MaterialTheme.typography.labelSmall,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textPrimary,
        )
    }
}

/** The unread count pill (blank text = unread with no count: the dot). */
@Composable
private fun UnreadPill(text: String) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier
            .heightIn(min = UNREAD_BADGE)
            .widthIn(min = UNREAD_BADGE)
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .background(UsTheme.extended.chatAccent)
            .padding(horizontal = if (text.isEmpty()) 0.dp else UsTheme.spacing.s)
            .semantics { contentDescription = if (text.isEmpty()) "Unread" else "$text unread" }
            .testTag(if (text.isEmpty()) "chat-unread-dot" else "chat-unread-count"),
    ) {
        if (text.isNotEmpty()) {
            Text(
                text = text,
                style = MaterialTheme.typography.labelSmall,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.onChatAccent,
            )
        }
    }
}

/** The row's long-press sheet: Pin, Mute, Mark read/unread, Archive, Delete chat. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ConversationActionsSheet(
    conversation: Conversation,
    title: String,
    actions: InboxRowActions,
    onDismiss: () -> Unit,
    /** Opened from the swipe's Delete: the sheet starts at the confirmation. */
    startWithDeleteConfirm: Boolean = false,
) {
    var confirmDelete by remember { mutableStateOf(startWithDeleteConfirm) }
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgSheet,
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .navigationBarsPadding()
                .padding(bottom = UsTheme.spacing.l)
                .testTag("chat-row-actions"),
        ) {
            Text(
                text = title,
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.m),
            )
            val act: (() -> Unit) -> Unit = { action ->
                onDismiss()
                action()
            }
            if (confirmDelete) {
                Text(
                    text = "Delete this chat? It goes from your inbox and your copy of the messages is cleared. " +
                        "The other person keeps theirs.",
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textMuted,
                    modifier = Modifier.padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.s),
                )
                SheetAction(UsIcons.Trash, "Delete chat", "chat-row-delete-confirm", danger = true) {
                    actions.onDelete?.let(act)
                }
                SheetAction(UsIcons.Close, "Cancel", "chat-row-delete-cancel") { confirmDelete = false }
                return@Column
            }
            SheetAction(UsIcons.Pin, if (conversation.isPinned) "Unpin" else "Pin to top", "chat-row-pin") {
                act(actions.onTogglePin)
            }
            SheetAction(
                if (conversation.isMuted) UsIcons.Notifications else UsIcons.NotificationsOff,
                if (conversation.isMuted) "Unmute" else "Mute messages",
                "chat-row-mute",
            ) { act(actions.onToggleMute) }
            if (conversation.hasUnread) {
                actions.onMarkRead?.let { SheetAction(UsIcons.CheckCheck, "Mark as read", "chat-row-read") { act(it) } }
            } else {
                actions.onMarkUnread?.let { SheetAction(UsIcons.Circle, "Mark as unread", "chat-row-unread") { act(it) } }
            }
            actions.onArchive?.let {
                SheetAction(UsIcons.Archive, if (actions.archived) "Unarchive" else "Archive", "chat-row-archive") { act(it) }
            }
            if (actions.onDelete != null) {
                SheetAction(UsIcons.Trash, "Delete chat", "chat-row-delete", danger = true) { confirmDelete = true }
            }
        }
    }
}

@Composable
internal fun SheetAction(
    icon: ImageVector,
    label: String,
    tag: String,
    danger: Boolean = false,
    enabled: Boolean = true,
    onClick: () -> Unit,
) {
    val tint = when {
        !enabled -> UsTheme.extended.textGhost
        danger -> UsTheme.extended.statusDanger
        else -> UsTheme.extended.textPrimary
    }
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        modifier = Modifier
            .fillMaxWidth()
            .combinedClickable(enabled = enabled, onClick = onClick)
            .padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.l)
            .testTag(tag),
    ) {
        Icon(imageVector = icon, contentDescription = null, tint = tint, modifier = Modifier.size(SHEET_GLYPH))
        Text(text = label, style = MaterialTheme.typography.bodyLarge, color = tint)
    }
}

/** What a typed last message says when the server left the preview empty. */
internal fun typedRowPreview(type: String): String = when (type) {
    "voice" -> "Voice message"
    "sticker" -> "Sticker"
    "gif" -> "GIF"
    "post" -> "Shared a post"
    "profile" -> "Shared a profile"
    "sound" -> "Shared a sound"
    "live" -> "Shared a LIVE"
    "call" -> "Call"
    else -> "Photo"
}

private val ROW_MARGIN = 12.dp
private val ROW_PADDING = 12.dp
private val ROW_CORNER = 16.dp
private val NAME_PREVIEW_GAP = 2.dp
private val UNREAD_BADGE = 18.dp
private val ONLINE_DOT_RING = 2.dp
private val ONLINE_DOT = 12.dp
private val ROW_GLYPH = 14.dp
private val SHEET_GLYPH = 22.dp
