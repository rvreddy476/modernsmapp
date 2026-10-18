package com.us.android.feature.chat.ui

import android.content.ClipData
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
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
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalClipboard
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.platform.toClipEntry
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.us.android.core.chat.data.Conversation
import com.us.android.core.chat.data.MAX_FORWARD_TARGETS
import com.us.android.core.chat.data.Message
import com.us.android.core.chat.data.MessageReportReason
import com.us.android.core.chat.data.ReadSummary
import com.us.android.core.common.time.formatRelativeTime
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmojiPanel
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.UsSettingsRadioRow
import kotlinx.coroutines.launch

/** The quick-reaction strip. The server stores any single emoji. */
internal val QUICK_REACTIONS = listOf("❤️", "😂", "😮", "😢", "😡", "👍")

/** What a long-press on a message may do; null hides the row. */
internal class MessageActions(
    val onReact: (String) -> Unit,
    val onReply: (() -> Unit)?,
    val onForward: (() -> Unit)?,
    val onEdit: (() -> Unit)?,
    val onDeleteForMe: (() -> Unit)?,
    val onUnsend: (() -> Unit)?,
    val onReport: (() -> Unit)?,
)

/**
 * The message's long-press sheet (CONTRACT §9): the reaction strip with "+"
 * for the full picker, then Reply, Copy, Forward, Edit, Delete for me,
 * Unsend and Report — each offered only where it applies.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun MessageActionSheet(message: Message, actions: MessageActions, onDismiss: () -> Unit) {
    var picker by remember { mutableStateOf(false) }
    var confirmUnsend by remember { mutableStateOf(false) }
    val clipboard = LocalClipboard.current
    val scope = rememberCoroutineScope()
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgSheet,
    ) {
        Column(modifier = Modifier.fillMaxWidth().navigationBarsPadding().padding(bottom = UsTheme.spacing.l).testTag("message-actions")) {
            val act: (() -> Unit) -> Unit = { action ->
                onDismiss()
                action()
            }
            if (picker) {
                UsEmojiPanel(onPick = { emoji -> act { actions.onReact(emoji) } }, modifier = Modifier.testTag("reaction-picker"))
                return@Column
            }
            if (confirmUnsend) {
                Text(
                    "Unsend this message? It's removed for everyone in the chat.",
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textMuted,
                    modifier = Modifier.padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.s),
                )
                SheetAction(UsIcons.Trash, "Unsend", "message-unsend-confirm", danger = true) { actions.onUnsend?.let(act) }
                SheetAction(UsIcons.Close, "Cancel", "message-unsend-cancel") { confirmUnsend = false }
                return@Column
            }
            Row(
                horizontalArrangement = Arrangement.SpaceEvenly,
                verticalAlignment = Alignment.CenterVertically,
                modifier = Modifier.fillMaxWidth().padding(vertical = UsTheme.spacing.s).testTag("reaction-bar"),
            ) {
                QUICK_REACTIONS.forEach { emoji ->
                    Text(
                        text = emoji,
                        style = MaterialTheme.typography.headlineSmall,
                        modifier = Modifier
                            .clickable { act { actions.onReact(emoji) } }
                            .padding(UsTheme.spacing.xs)
                            .semantics { contentDescription = "React $emoji" },
                    )
                }
                Box(
                    contentAlignment = Alignment.Center,
                    modifier = Modifier
                        .size(PLUS)
                        .background(UsTheme.extended.fillStrong, CircleShape)
                        .clickable { picker = true }
                        .semantics { contentDescription = "More reactions" }
                        .testTag("reaction-more"),
                ) {
                    Text("+", style = MaterialTheme.typography.titleMedium, color = UsTheme.extended.textPrimary)
                }
            }
            actions.onReply?.let { SheetAction(UsIcons.Reply, "Reply", "message-reply") { act(it) } }
            if (message.text.isNotBlank() && !message.deleted) {
                SheetAction(UsIcons.Copy, "Copy", "message-copy") {
                    scope.launch { clipboard.setClipEntry(ClipData.newPlainText("Message", message.text).toClipEntry()) }
                    onDismiss()
                }
            }
            actions.onForward?.let { SheetAction(UsIcons.Forward, "Forward", "message-forward") { act(it) } }
            actions.onEdit?.let { SheetAction(UsIcons.Pencil, "Edit", "message-edit") { act(it) } }
            actions.onDeleteForMe?.let { SheetAction(UsIcons.Trash, "Delete for me", "message-delete-me") { act(it) } }
            if (actions.onUnsend != null) {
                SheetAction(UsIcons.RotateCcw, "Unsend", "message-unsend", danger = true) { confirmUnsend = true }
            }
            actions.onReport?.let { SheetAction(UsIcons.Flag, "Report", "message-report", danger = true) { act(it) } }
        }
    }
}

/** Who reacted, grouped by emoji (CONTRACT §2.5). */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun ReactionsSheet(state: ReactionSheetState, onDismiss: () -> Unit) {
    ModalBottomSheet(onDismissRequest = onDismiss, containerColor = UsTheme.extended.bgSheet) {
        Column(modifier = Modifier.fillMaxWidth().navigationBarsPadding().padding(bottom = UsTheme.spacing.l).testTag("reactions-sheet")) {
            SheetTitle("Reactions")
            val groups = state.groups
            when {
                state.failed -> UsErrorState(message = "We couldn't load the reactions.", onRetry = null)
                groups == null -> UsLoadingState(label = "Loading reactions")
                else -> LazyColumn(modifier = Modifier.heightIn(max = LIST_MAX)) {
                    groups.forEach { group ->
                        item(key = "emoji:" + group.emoji) {
                            Text(
                                "${group.emoji}  ${group.count}",
                                style = MaterialTheme.typography.titleSmall,
                                modifier = Modifier.padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.s),
                            )
                        }
                        items(group.users, key = { group.emoji + ":" + it.userId }) { user ->
                            PersonLine(user.displayName, user.userId, user.avatarUrl, trailing = group.emoji)
                        }
                    }
                }
            }
        }
    }
}

/** Group "Seen by" (CONTRACT §2.9). */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun SeenBySheet(summary: ReadSummary?, failed: Boolean, onDismiss: () -> Unit) {
    ModalBottomSheet(onDismissRequest = onDismiss, containerColor = UsTheme.extended.bgSheet) {
        Column(modifier = Modifier.fillMaxWidth().navigationBarsPadding().padding(bottom = UsTheme.spacing.l).testTag("seen-by-sheet")) {
            SheetTitle(summary?.let { "Seen by ${it.count}" } ?: "Seen by")
            when {
                failed -> UsErrorState(message = "We couldn't load who has seen it.", onRetry = null)
                summary == null -> UsLoadingState(label = "Loading")
                else -> LazyColumn(modifier = Modifier.heightIn(max = LIST_MAX)) {
                    items(summary.seenBy, key = { it.userId }) { seen ->
                        PersonLine(seen.displayName, seen.userId, seen.avatarUrl, trailing = formatRelativeTime(seen.at))
                    }
                }
            }
        }
    }
}

/** Forward to up to five chats, groups included (CONTRACT §2.4). */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun ForwardSheet(
    targets: List<Conversation>?,
    sending: Boolean,
    viewerId: String,
    onSend: (List<String>) -> Unit,
    onDismiss: () -> Unit,
) {
    var picked by remember { mutableStateOf(emptyList<String>()) }
    var query by remember { mutableStateOf("") }
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
                .testTag("forward-sheet"),
        ) {
            SheetTitle("Forward to")
            UsTextField(
                value = query,
                onValueChange = { query = it },
                label = "Search",
                modifier = Modifier.fillMaxWidth().padding(horizontal = UsTheme.spacing.l),
            )
            Text(
                "Pick up to $MAX_FORWARD_TARGETS chats",
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.textMuted,
                modifier = Modifier.padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.xs),
            )
            when {
                targets == null -> UsLoadingState(label = "Loading your chats")
                else -> LazyColumn(modifier = Modifier.heightIn(max = LIST_MAX)) {
                    val shown = targets.filter {
                        query.isBlank() || it.displayTitle(viewerId).contains(query.trim(), ignoreCase = true)
                    }
                    items(shown, key = { it.id }) { conversation ->
                        val selected = conversation.id in picked
                        Row(
                            verticalAlignment = Alignment.CenterVertically,
                            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
                            modifier = Modifier
                                .fillMaxWidth()
                                .clickable {
                                    picked = when {
                                        selected -> picked - conversation.id
                                        picked.size >= MAX_FORWARD_TARGETS -> picked
                                        else -> picked + conversation.id
                                    }
                                }
                                .padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.s)
                                .testTag("forward-target:${conversation.id}"),
                        ) {
                            val title = conversation.displayTitle(viewerId)
                            UsAvatar(
                                name = title,
                                seed = conversation.id,
                                size = UsAvatarSize.Medium,
                                imageUrl = conversation.peer?.avatarUrl ?: conversation.avatarUrl,
                            )
                            Text(title, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
                            SelectDot(selected)
                        }
                    }
                }
            }
            UsButton(
                text = when {
                    sending -> "Sending…"
                    picked.isEmpty() -> "Send"
                    else -> "Send to ${picked.size}"
                },
                onClick = { onSend(picked) },
                enabled = picked.isNotEmpty() && !sending,
                modifier = Modifier.fillMaxWidth().padding(UsTheme.spacing.l).testTag("forward-send"),
            )
        }
    }
}

/** Why the message is reported (CONTRACT §2.6), with an optional note. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun ReportSheet(sending: Boolean, onReport: (MessageReportReason, String?) -> Unit, onDismiss: () -> Unit) {
    var reason by remember { mutableStateOf<MessageReportReason?>(null) }
    var note by remember { mutableStateOf("") }
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
                .testTag("report-sheet"),
        ) {
            SheetTitle("Why are you reporting this message?")
            MessageReportReason.entries.forEach { option ->
                UsSettingsRadioRow(
                    title = option.label,
                    selected = reason == option,
                    onClick = { reason = option },
                    modifier = Modifier.testTag("report-reason:${option.wire}"),
                )
            }
            if (reason == MessageReportReason.OTHER) {
                UsTextField(value = note, onValueChange = { note = it.take(REPORT_NOTE_MAX) }, label = "Tell us more (optional)")
            }
            UsButton(
                text = if (sending) "Sending…" else "Report",
                onClick = { reason?.let { onReport(it, note) } },
                enabled = reason != null && !sending,
                modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.m).testTag("report-send"),
            )
        }
    }
}

/** Edit own text. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun EditSheet(text: String, saving: Boolean, onChange: (String) -> Unit, onSave: () -> Unit, onDismiss: () -> Unit) {
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgSheet,
    ) {
        Column(
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            modifier = Modifier
                .fillMaxWidth()
                .navigationBarsPadding()
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(bottom = UsTheme.spacing.l)
                .testTag("edit-sheet"),
        ) {
            SheetTitle("Edit message")
            UsTextField(value = text, onValueChange = onChange, label = "Message", singleLine = false)
            UsButton(
                text = if (saving) "Saving…" else "Save",
                onClick = onSave,
                enabled = text.isNotBlank() && !saving,
                modifier = Modifier.fillMaxWidth().testTag("edit-save"),
            )
        }
    }
}

@Composable
private fun SheetTitle(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.titleMedium,
        fontWeight = FontWeight.SemiBold,
        color = UsTheme.extended.textPrimary,
        modifier = Modifier.padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.m),
    )
}

@Composable
private fun PersonLine(name: String, userId: String, avatarUrl: String?, trailing: String) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        modifier = Modifier.fillMaxWidth().padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.s),
    ) {
        UsAvatar(name = name, seed = userId, size = UsAvatarSize.Small, imageUrl = avatarUrl)
        Text(name.ifBlank { "Someone" }, modifier = Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis)
        Text(trailing, style = MaterialTheme.typography.labelSmall, color = UsTheme.extended.textMuted)
    }
}

@Composable
internal fun SelectDot(selected: Boolean) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier
            .size(CHECK)
            .background(if (selected) UsTheme.extended.chatAccent else UsTheme.extended.bgCanvas, CircleShape)
            .border(HAIRLINE, if (selected) UsTheme.extended.chatAccent else UsTheme.extended.borderMedium, CircleShape),
    ) {
        if (selected) Icon(UsIcons.Check, contentDescription = null, tint = UsTheme.extended.onChatAccent, modifier = Modifier.size(CHECK_GLYPH))
    }
}

private const val REPORT_NOTE_MAX = 500
private val PLUS = 36.dp
private val LIST_MAX = 420.dp
private val CHECK = 22.dp
private val CHECK_GLYPH = 14.dp
private val HAIRLINE = 1.dp
