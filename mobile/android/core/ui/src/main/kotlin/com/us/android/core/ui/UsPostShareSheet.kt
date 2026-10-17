package com.us.android.core.ui

import android.content.ClipData
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
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalClipboard
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.platform.toClipEntry
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import kotlinx.coroutines.launch

/** One conversation a post can be sent into: a direct chat or a group, named as the inbox names it. */
@Immutable
data class UsShareTarget(
    val id: String,
    val name: String,
    val isGroup: Boolean,
    /** A signed avatar URL when the server sent one; the initials otherwise. */
    val avatarUrl: String? = null,
)

/** The Repost row: absent until the viewer's repost state is known, then Repost or Remove repost. */
enum class UsRepostRow { HIDDEN, REPOST, REMOVE }

/** "Send in Messages": the conversations to pick from, and how each send went. */
@Immutable
data class UsSharePickerState(
    val query: String = "",
    val targets: List<UsShareTarget> = emptyList(),
    val loading: Boolean = false,
    /** The inbox could not be read; the picker offers Try again. */
    val failed: Boolean = false,
    /** Conversations with a send on the wire. */
    val sending: Set<String> = emptySet(),
    /** Why the last send was refused, in words; null when there is nothing to say. */
    val error: String? = null,
)

/** What the share sheet draws. [picker] is non-null while "Send in Messages" is open. */
@Immutable
data class UsPostShareState(
    val postId: String,
    /** The post's public permalink, what Copy link puts on the clipboard. */
    val link: String,
    val repost: UsRepostRow = UsRepostRow.HIDDEN,
    val repostBusy: Boolean = false,
    val picker: UsSharePickerState? = null,
)

/** What the sheet asks of its host. */
class UsPostShareCallbacks(
    val onOpenPicker: () -> Unit,
    val onClosePicker: () -> Unit,
    val onQueryChange: (String) -> Unit,
    val onSend: (UsShareTarget) -> Unit,
    val onRetryTargets: () -> Unit,
    /** The link is on the clipboard; the sheet has already left. */
    val onLinkCopied: () -> Unit,
    val onToggleRepost: () -> Unit,
    /** "More…": the system share sheet. The sheet has already left. */
    val onMore: () -> Unit,
)

/**
 * The post's Share sheet (2026-10-17), Momentum's own rather than a jump
 * straight to the system chooser: Send in Messages (a conversation picker),
 * Copy link, Repost / Remove repost, and "More…" for every other app, which
 * is the system chooser the Share action used to open directly.
 *
 * The idiom is the More sheet's: `bgSheet`, 28dp corners, the scrim, the
 * grab handle inside the content, one row per action.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun UsPostShareSheet(
    state: UsPostShareState,
    callbacks: UsPostShareCallbacks,
    onDismiss: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    val scope = rememberCoroutineScope()
    val clipboard = LocalClipboard.current

    // Slide the sheet away FIRST, then act — the More sheet's rule.
    fun leaveThen(action: () -> Unit) {
        scope.launch { sheetState.hide() }.invokeOnCompletion {
            onDismiss()
            action()
        }
    }

    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = sheetState,
        containerColor = UsTheme.extended.bgSheet,
        contentColor = UsTheme.extended.textPrimary,
        shape = RoundedCornerShape(topStart = SHEET_RADIUS, topEnd = SHEET_RADIUS),
        scrimColor = UsTheme.extended.scrim,
        dragHandle = null,
        modifier = modifier.testTag("post_share_sheet:${state.postId}"),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .navigationBarsPadding()
                .padding(bottom = CONTENT_BOTTOM),
        ) {
            SheetGrabHandle()
            val picker = state.picker
            if (picker == null) {
                ShareMenu(
                    state = state,
                    onSendInMessages = callbacks.onOpenPicker,
                    onCopyLink = {
                        scope.launch {
                            clipboard.setClipEntry(ClipData.newPlainText(CLIP_LABEL, state.link).toClipEntry())
                        }
                        leaveThen(callbacks.onLinkCopied)
                    },
                    onToggleRepost = callbacks.onToggleRepost,
                    onMore = { leaveThen(callbacks.onMore) },
                )
            } else {
                SharePicker(picker = picker, callbacks = callbacks)
            }
        }
    }
}

@Composable
private fun ShareMenu(
    state: UsPostShareState,
    onSendInMessages: () -> Unit,
    onCopyLink: () -> Unit,
    onToggleRepost: () -> Unit,
    onMore: () -> Unit,
) {
    SheetTitle("Share")
    SheetRow(
        icon = UsIcons.Send,
        label = SEND_IN_MESSAGES,
        onClick = onSendInMessages,
        testTag = "post_share_row:messages",
    )
    SheetRow(icon = UsIcons.Link, label = "Copy link", onClick = onCopyLink, testTag = "post_share_row:copy_link")
    if (state.repost != UsRepostRow.HIDDEN) {
        SheetRow(
            icon = UsIcons.Repost,
            label = if (state.repost == UsRepostRow.REMOVE) "Remove repost" else "Repost",
            onClick = onToggleRepost,
            enabled = !state.repostBusy,
            testTag = "post_share_row:repost",
        )
    }
    SheetRow(icon = UsIcons.More, label = "More…", onClick = onMore, testTag = "post_share_row:more")
}

/** "Send in Messages": a search field over the recent conversations, each with Send. */
@Composable
private fun SharePicker(picker: UsSharePickerState, callbacks: UsPostShareCallbacks) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.s),
    ) {
        IconButton(onClick = callbacks.onClosePicker, modifier = Modifier.testTag("post_share_picker_back")) {
            Icon(UsIcons.Back, contentDescription = "Back", tint = UsTheme.extended.textPrimary)
        }
        Text(
            text = SEND_IN_MESSAGES,
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.semantics { heading() },
        )
    }
    UsTextField(
        value = picker.query,
        onValueChange = callbacks.onQueryChange,
        label = "Search",
        placeholder = "Search by name",
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.l)
            .testTag("post_share_search"),
    )
    picker.error?.let {
        Text(
            text = it,
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.statusDanger,
            modifier = Modifier
                .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s)
                .testTag("post_share_error"),
        )
    }
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .heightIn(min = PICKER_MIN_HEIGHT, max = PICKER_MAX_HEIGHT),
        contentAlignment = Alignment.TopCenter,
    ) {
        when {
            picker.loading -> UsLoadingState(label = "Loading your chats")
            picker.failed -> UsErrorState(message = "We couldn't load your chats.", onRetry = callbacks.onRetryTargets)
            picker.targets.isEmpty() -> UsEmptyState(
                title = if (picker.query.isBlank()) "No chats yet" else "No chats match \"${picker.query.trim()}\"",
            )
            else -> LazyColumn(modifier = Modifier.fillMaxWidth().testTag("post_share_targets")) {
                items(picker.targets, key = { it.id }) { target ->
                    TargetRow(
                        target = target,
                        sending = target.id in picker.sending,
                        onSend = { callbacks.onSend(target) },
                    )
                }
            }
        }
    }
}

@Composable
private fun TargetRow(target: UsShareTarget, sending: Boolean, onSend: () -> Unit) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s)
            .testTag("post_share_target:${target.id}"),
    ) {
        UsAvatar(name = target.name, seed = target.id, size = UsAvatarSize.Medium, imageUrl = target.avatarUrl)
        Text(
            text = target.name,
            style = MaterialTheme.typography.bodyLarge,
            color = UsTheme.extended.textPrimary,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
        if (sending) {
            CircularProgressIndicator(
                modifier = Modifier.size(SPINNER),
                strokeWidth = SPINNER_STROKE,
                color = UsTheme.extended.accent,
            )
        } else {
            UsPillButton(
                text = "Send",
                onClick = onSend,
                modifier = Modifier.testTag("post_share_send:${target.id}"),
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
        modifier = Modifier
            .padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.s)
            .semantics { heading() },
    )
}

private const val SEND_IN_MESSAGES = "Send in Messages"
private const val CLIP_LABEL = "Post link"
private val SHEET_RADIUS = 28.dp
private val CONTENT_BOTTOM = 12.dp
private val PICKER_MIN_HEIGHT = 160.dp
private val PICKER_MAX_HEIGHT = 420.dp
private val SPINNER = 20.dp
private val SPINNER_STROKE = 2.dp
