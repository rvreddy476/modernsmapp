package com.us.android.feature.echo.ui

import android.content.ClipData
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.Clipboard
import androidx.compose.ui.platform.LocalClipboard
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.platform.toClipEntry
import androidx.compose.ui.text.font.FontWeight
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.WebLinks
import com.us.android.core.ui.UsPostReportState
import com.us.android.core.ui.UsPostReportStep
import com.us.android.core.ui.UsPostShareCallbacks
import com.us.android.core.ui.UsPostShareSheet
import com.us.android.core.ui.UsPostShareState
import com.us.android.core.ui.UsSharePickerState
import com.us.android.feature.echo.data.Note
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * Everything [actions] opens over a list: the repost sheet, the share sheet
 * (with Send via Chat), X's `···` menu, and the actions' one-line messages.
 * [onQuote] is the host's gated quote; the author's Edit opens the composer
 * through [destinations].
 */
@Composable
fun NoteActionsHost(actions: EchoNoteActions, destinations: EchoDestinations, onQuote: (Note) -> Unit) {
    val state by actions.state.collectAsStateWithLifecycle()
    val context = LocalContext.current

    LaunchedEffect(state.openComposer) {
        state.openComposer?.let {
            actions.composerOpened()
            destinations.onCompose(it)
        }
    }

    state.repostFor?.let { note ->
        RepostSheet(
            note = note,
            onRepost = { actions.toggleRepost(note) },
            onQuote = {
                actions.closeRepost()
                onQuote(note)
            },
            onDismiss = actions::closeRepost,
        )
    }
    state.shareFor?.let { note ->
        val picker = state.picker
        val shareState = remember(note.id, picker) {
            UsPostShareState(
                postId = note.id,
                link = WebLinks.withShareRef(WebLinks.echoNotePermalink(note.id)),
                picker = picker?.let { p ->
                    UsSharePickerState(
                        query = p.query,
                        targets = p.targets.orEmpty().filter { p.query.isBlank() || it.name.contains(p.query, true) },
                        loading = p.targets == null && !p.failed,
                        failed = p.failed,
                        sending = p.sending,
                        error = p.error,
                    )
                },
            )
        }
        val callbacks = remember(note.id, actions) {
            UsPostShareCallbacks(
                onOpenPicker = actions::openPicker,
                onClosePicker = actions::closePicker,
                onQueryChange = actions::pickerQuery,
                onSend = actions::sendTo,
                onRetryTargets = actions::openPicker,
                onLinkCopied = { actions.linkCopied(note) },
                onToggleRepost = {},
                onMore = {
                    if (context.shareNote(note)) actions.shared(note)
                },
            )
        }
        UsPostShareSheet(state = shareState, callbacks = callbacks, onDismiss = actions::closeShare)
    }
    state.menu?.let { menu ->
        NoteMenuSheet(
            menu = menu,
            report = state.report,
            actions = actions,
            onCopyLink = { actions.linkCopied(menu.note) }
        )
    }
    val message = remember(state.message) { state.message?.let { UsMessage(it, UsMessageType.Info) } }
    EchoMessageHost(message = message, onDismiss = actions::messageShown)
}

/** X's repost sheet: Repost (or Undo repost) and Quote. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun RepostSheet(note: Note, onRepost: () -> Unit, onQuote: () -> Unit, onDismiss: () -> Unit) {
    ModalBottomSheet(onDismissRequest = onDismiss, containerColor = UsTheme.extended.bgSheet) {
        Column(modifier = Modifier.fillMaxWidth().navigationBarsPadding().padding(bottom = UsTheme.spacing.l)) {
            SheetRow(UsIcons.Repost, if (note.reposted) "Undo repost" else "Repost", "repost", onClick = onRepost)
            SheetRow(UsIcons.Pencil, "Quote", "quote", onClick = onQuote)
        }
    }
}

/**
 * X's `···` menu. Someone else's post: Not interested, Follow / Unfollow,
 * Mute, Block, Copy link, Report. The viewer's own: Edit (30 minutes),
 * Delete, Copy link.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
@Suppress("LongMethod", "CyclomaticComplexMethod") // one sheet, X's rows and two confirmations
fun NoteMenuSheet(menu: NoteMenuState, report: UsPostReportState, actions: EchoNoteActions, onCopyLink: () -> Unit) {
    val note = menu.note
    val handle = note.author?.handle?.ifBlank { null } ?: "this account"
    val clipboard = LocalClipboard.current
    val scope = rememberCoroutineScope()
    val link = remember(note.id) { WebLinks.withShareRef(WebLinks.echoNotePermalink(note.id)) }
    LaunchedEffect(report) {
        if (report == UsPostReportState.Sent || report == UsPostReportState.AlreadyReported) {
            delay(REPORT_CLOSE_MILLIS)
            actions.closeMenu()
        }
    }
    ModalBottomSheet(onDismissRequest = actions::closeMenu, containerColor = UsTheme.extended.bgSheet) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .navigationBarsPadding()
                .padding(bottom = UsTheme.spacing.l)
                .testTag("echo_menu"),
        ) {
            when {
                menu.reporting -> UsPostReportStep(
                    report = report,
                    onBack = actions::closeMenu,
                    onSubmit = { reason, details -> actions.report(note, reason.wire, details) },
                    subject = "post",
                )
                menu.confirmBlock -> Confirm(
                    title = "Block $handle?",
                    detail = "They won't see your posts or be able to follow or message you, and you won't see theirs.",
                    action = "Block",
                    tag = "block_confirm",
                ) { actions.block(note) }
                menu.confirmDelete -> Confirm(
                    title = "Delete post?",
                    detail = "This can't be undone and it will be removed from your profile, the timeline of any " +
                        "accounts that follow you, and from search results.",
                    action = "Delete",
                    tag = "delete_confirm",
                ) { actions.delete(note) }
                menu.own -> {
                    if (menu.canEdit) SheetRow(UsIcons.Pencil, "Edit post", "edit") { actions.edit(note) }
                    SheetRow(UsIcons.Trash, "Delete post", "delete", danger = true, onClick = actions::askDelete)
                    SheetRow(UsIcons.Link, "Copy link", "copy_link") {
                        copy(scope, clipboard, link)
                        actions.closeMenu()
                        onCopyLink()
                    }
                }
                else -> {
                    SheetRow(UsIcons.ThumbsDown, "Not interested in this post", "not_interested") {
                        actions.notInterested(note)
                    }
                    when (val follows = menu.follows) {
                        null -> Unit
                        true -> SheetRow(UsIcons.UserMinus, "Unfollow $handle", "unfollow") {
                            actions.toggleFollow(note, follows = true)
                        }
                        false -> SheetRow(UsIcons.UserPlus, "Follow $handle", "follow") {
                            actions.toggleFollow(note, follows = false)
                        }
                    }
                    SheetRow(UsIcons.SoundOff, "Mute $handle", "mute") { actions.mute(note) }
                    SheetRow(UsIcons.Ban, "Block $handle", "block", onClick = actions::askBlock)
                    SheetRow(UsIcons.Link, "Copy link", "copy_link") {
                        copy(scope, clipboard, link)
                        actions.closeMenu()
                        onCopyLink()
                    }
                    SheetRow(UsIcons.Flag, "Report post", "report", onClick = actions::startReport)
                }
            }
        }
    }
}

private fun copy(scope: CoroutineScope, clipboard: Clipboard, link: String) {
    scope.launch { clipboard.setClipEntry(ClipData.newPlainText(CLIP_LABEL, link).toClipEntry()) }
}

@Composable
private fun Confirm(title: String, detail: String, action: String, tag: String, onConfirm: () -> Unit) {
    Column(
        modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        Text(
            text = title,
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textPrimary,
        )
        Text(text = detail, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary)
        UsButton(text = action, onClick = onConfirm, modifier = Modifier.fillMaxWidth().testTag("echo_menu:$tag"))
    }
}

@Composable
internal fun SheetRow(
    icon: ImageVector,
    label: String,
    tag: String,
    danger: Boolean = false,
    onClick: () -> Unit,
) {
    val color: Color = if (danger) UsTheme.extended.statusDanger else UsTheme.extended.textPrimary
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l)
            .testTag("echo_menu:$tag"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Icon(icon, contentDescription = null, tint = color)
        Text(text = label, style = MaterialTheme.typography.bodyLarge, fontWeight = FontWeight.Bold, color = color)
    }
}

private const val REPORT_CLOSE_MILLIS = 1_200L
private const val CLIP_LABEL = "Echo post"
