package com.us.android.feature.chat.ui.home

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ExperimentalMaterial3Api
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
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.us.android.core.chat.data.NOTE_AUDIENCE_EVERYONE
import com.us.android.core.chat.data.NOTE_AUDIENCE_FRIENDS
import com.us.android.core.chat.data.NOTE_AUDIENCE_ONLY_ME
import com.us.android.core.chat.data.NOTE_MAX_LENGTH
import com.us.android.core.chat.data.Note
import com.us.android.core.chat.data.Notes
import com.us.android.core.chat.data.OthersNote
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsSettingsRadioRow

/** What the notes row asks of its host. */
internal class NotesActions(
    val onSave: (text: String, audience: String) -> Unit,
    val onDelete: () -> Unit,
    val onReply: (note: OthersNote, text: String) -> Unit,
)

/** The audiences a note can have, in our words (CONTRACT §5). */
internal val NOTE_AUDIENCES = listOf(
    NOTE_AUDIENCE_EVERYONE to "Everyone who can message you",
    NOTE_AUDIENCE_FRIENDS to "Friends",
    NOTE_AUDIENCE_ONLY_ME to "Only me",
)

/**
 * The Notes row (CONTRACT §9): the viewer's own avatar with "Your note"
 * (or their note in a bubble above it) first, then the people with notes,
 * each with an online dot. A note lives 24 hours.
 */
@Composable
internal fun NotesRow(
    notes: Notes,
    viewerName: String,
    viewerId: String,
    saving: Boolean,
    actions: NotesActions,
) {
    var editing by remember { mutableStateOf(false) }
    var replyingTo by remember { mutableStateOf<OthersNote?>(null) }
    LazyRow(
        contentPadding = PaddingValues(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.s),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        modifier = Modifier.fillMaxWidth().testTag("chat_notes_row"),
    ) {
        item(key = "mine") {
            NoteTile(
                bubble = notes.mine?.text ?: "Add a note",
                bubbleMuted = notes.mine == null,
                name = "Your note",
                seed = viewerId,
                avatarName = viewerName,
                avatarUrl = null,
                online = false,
                onClick = { editing = true },
                tag = "chat_note_mine",
            )
        }
        items(notes.others, key = { it.note.id.ifBlank { it.userId } }) { other ->
            NoteTile(
                bubble = other.note.text,
                bubbleMuted = false,
                name = other.displayName.ifBlank { "Someone" },
                seed = other.userId,
                avatarName = other.displayName,
                avatarUrl = other.avatarUrl,
                online = other.isOnline == true,
                onClick = { replyingTo = other },
                tag = "chat_note:${other.userId}",
            )
        }
    }
    if (editing) {
        NoteEditorSheet(
            current = notes.mine,
            saving = saving,
            onSave = { text, audience ->
                actions.onSave(text, audience)
                editing = false
            },
            onDelete = {
                actions.onDelete()
                editing = false
            },
            onDismiss = { editing = false },
        )
    }
    replyingTo?.let { note ->
        NoteReplySheet(
            note = note,
            onSend = { text ->
                actions.onReply(note, text)
                replyingTo = null
            },
            onDismiss = { replyingTo = null },
        )
    }
}

@Suppress("LongParameterList")
@Composable
private fun NoteTile(
    bubble: String,
    bubbleMuted: Boolean,
    name: String,
    seed: String,
    avatarName: String,
    avatarUrl: String?,
    online: Boolean,
    onClick: () -> Unit,
    tag: String,
) {
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        modifier = Modifier
            .width(TILE_WIDTH)
            .pressScale(onClick)
            .testTag(tag),
    ) {
        Text(
            text = bubble,
            style = MaterialTheme.typography.labelSmall,
            color = if (bubbleMuted) UsTheme.extended.textMuted else UsTheme.extended.textPrimary,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
            textAlign = TextAlign.Center,
            modifier = Modifier
                .widthIn(max = TILE_WIDTH)
                .background(UsTheme.extended.bgCardSolid, RoundedCornerShape(UsTheme.radii.medium))
                .border(HAIRLINE, UsTheme.extended.borderSubtle, RoundedCornerShape(UsTheme.radii.medium))
                .padding(horizontal = UsTheme.spacing.s, vertical = UsTheme.spacing.xs),
        )
        Box {
            UsAvatar(name = avatarName, size = UsAvatarSize.Medium, seed = seed, imageUrl = avatarUrl)
            if (online) {
                Box(
                    modifier = Modifier
                        .align(Alignment.BottomEnd)
                        .size(ONLINE_DOT)
                        .background(UsTheme.extended.chatOnline, CircleShape)
                        .border(ONLINE_RING, UsTheme.extended.bgCanvas, CircleShape),
                )
            }
        }
        Text(
            text = name,
            style = MaterialTheme.typography.labelSmall,
            color = UsTheme.extended.textMuted,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

/** Write (or replace, or remove) your note: up to 60 characters, with who may see it. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun NoteEditorSheet(
    current: Note?,
    saving: Boolean,
    onSave: (String, String) -> Unit,
    onDelete: () -> Unit,
    onDismiss: () -> Unit,
) {
    var text by remember { mutableStateOf(current?.text.orEmpty()) }
    var audience by remember { mutableStateOf(current?.audience ?: NOTE_AUDIENCE_EVERYONE) }
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
                .testTag("chat_note_editor"),
        ) {
            Text("Your note", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
            Text(
                "A short note for the people you chat with. It disappears after 24 hours.",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
            UsTextField(
                value = text,
                onValueChange = { text = it.take(NOTE_MAX_LENGTH) },
                label = "Note",
                placeholder = "What's on your mind?",
                modifier = Modifier.fillMaxWidth().testTag("chat_note_text"),
            )
            Text(
                "${text.length}/$NOTE_MAX_LENGTH",
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.textMuted,
            )
            Text("Who can see it", style = MaterialTheme.typography.titleSmall)
            NOTE_AUDIENCES.forEach { (value, label) ->
                UsSettingsRadioRow(title = label, selected = audience == value, onClick = { audience = value })
            }
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                if (current != null) {
                    UsSecondaryButton(text = "Remove note", onClick = onDelete, modifier = Modifier.weight(1f))
                }
                UsButton(
                    text = if (saving) "Sharing…" else "Share",
                    onClick = { onSave(text, audience) },
                    enabled = text.isNotBlank() && !saving,
                    modifier = Modifier.weight(1f).testTag("chat_note_share"),
                )
            }
        }
    }
}

/** Someone's note, and a reply that goes to them as a message. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun NoteReplySheet(note: OthersNote, onSend: (String) -> Unit, onDismiss: () -> Unit) {
    var reply by remember { mutableStateOf("") }
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgSheet,
    ) {
        Column(
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            horizontalAlignment = Alignment.CenterHorizontally,
            modifier = Modifier
                .fillMaxWidth()
                .navigationBarsPadding()
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(bottom = UsTheme.spacing.l)
                .testTag("chat_note_reply"),
        ) {
            UsAvatar(name = note.displayName, size = UsAvatarSize.Large, seed = note.userId, imageUrl = note.avatarUrl)
            Text(note.displayName.ifBlank { "Someone" }, style = MaterialTheme.typography.titleMedium)
            Text(
                note.note.text,
                style = MaterialTheme.typography.bodyLarge,
                textAlign = TextAlign.Center,
                color = UsTheme.extended.textPrimary,
            )
            UsTextField(
                value = reply,
                onValueChange = { reply = it },
                label = "Reply",
                placeholder = "Reply to ${note.displayName.ifBlank { "their note" }}",
                modifier = Modifier.fillMaxWidth().testTag("chat_note_reply_text"),
            )
            UsButton(
                text = "Send",
                onClick = { onSend(reply) },
                enabled = reply.isNotBlank(),
                modifier = Modifier.fillMaxWidth().testTag("chat_note_reply_send"),
            )
        }
    }
}

private val TILE_WIDTH = 76.dp
private val HAIRLINE = 1.dp
private val ONLINE_DOT = 12.dp
private val ONLINE_RING = 2.dp
