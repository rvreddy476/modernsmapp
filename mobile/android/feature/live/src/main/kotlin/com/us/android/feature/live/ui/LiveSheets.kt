package com.us.android.feature.live.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.tooling.preview.Preview
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsPostReportState
import com.us.android.feature.live.data.HostMessageAction
import com.us.android.feature.live.data.LiveChatMessageDto
import com.us.android.feature.live.data.LiveReportReason
import com.us.android.feature.live.data.MAX_STREAM_MODERATORS
import com.us.android.feature.live.data.ViewerMessageAction
import com.us.android.feature.live.data.liveReportReasons
import kotlinx.coroutines.delay

/*
 * The live screens' sheets and dialogs. Material 3 sheets inside UsTheme,
 * styled from tokens (there is no house sheet component).
 */

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun LiveSheet(tag: String, onDismiss: () -> Unit, content: @Composable () -> Unit) {
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgCardSolid,
        contentColor = UsTheme.extended.textPrimary,
        shape = RoundedCornerShape(topStart = UsTheme.radii.card, topEnd = UsTheme.radii.card),
        modifier = Modifier.testTag(tag),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(bottom = UsTheme.spacing.xxl)
                .navigationBarsPadding()
                .imePadding(),
        ) { content() }
    }
}

@Composable
private fun SheetTitle(text: String) {
    Text(
        text,
        style = MaterialTheme.typography.titleMedium,
        fontWeight = FontWeight.Bold,
        color = UsTheme.extended.textPrimary,
    )
    Spacer(Modifier.height(UsTheme.spacing.l))
}

@Composable
private fun SheetRow(label: String, tag: String, onClick: () -> Unit, checked: Boolean = false) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier
            .fillMaxWidth()
            .clickable(role = Role.Button, onClick = onClick)
            .padding(vertical = UsTheme.spacing.l)
            .testTag(tag),
    ) {
        Text(
            label,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.weight(1f),
        )
        if (checked) {
            Icon(UsIcons.Check, contentDescription = "Selected", tint = UsTheme.extended.accentSolid)
        }
    }
}

// ── Viewer: Report ──────────────────────────────────────────────────────

/** Report a stream or one chat message. Closes itself a beat after Sent / AlreadyReported. */
@Composable
fun LiveReportSheet(
    aboutMessage: Boolean,
    report: UsPostReportState,
    onSubmit: (LiveReportReason, String) -> Unit,
    onDismiss: () -> Unit,
) {
    if (report == UsPostReportState.Sent || report == UsPostReportState.AlreadyReported) {
        LaunchedEffect(report) {
            delay(REPORT_CLOSE_DELAY_MILLIS)
            onDismiss()
        }
    }
    LiveSheet(tag = "live-report-sheet", onDismiss = onDismiss) {
        LiveReportContent(aboutMessage = aboutMessage, report = report, onSubmit = onSubmit)
    }
}

@Composable
private fun LiveReportContent(
    aboutMessage: Boolean,
    report: UsPostReportState,
    onSubmit: (LiveReportReason, String) -> Unit,
) {
    var chosen by rememberSaveable { mutableStateOf<LiveReportReason?>(null) }
    var note by rememberSaveable { mutableStateOf("") }

    SheetTitle(if (aboutMessage) "Report message" else "Report stream")
    when (report) {
        UsPostReportState.Sent -> ReportOutcome("Thanks for reporting. We'll take a look.")
        UsPostReportState.AlreadyReported -> ReportOutcome("You've already reported this.")
        else -> {
            liveReportReasons().forEach { reason ->
                SheetRow(
                    label = reason.label,
                    tag = "live-report-${reason.wire}",
                    checked = chosen == reason,
                    onClick = { chosen = reason },
                )
            }
            Spacer(Modifier.height(UsTheme.spacing.m))
            UsTextField(
                value = note,
                onValueChange = { note = it.take(REPORT_NOTE_MAX) },
                label = "Add a note (optional)",
                singleLine = false,
                modifier = Modifier
                    .fillMaxWidth()
                    .testTag("live-report-note"),
            )
            if (report == UsPostReportState.Failed) {
                Spacer(Modifier.height(UsTheme.spacing.m))
                Text(
                    "Couldn't send the report.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
            Spacer(Modifier.height(UsTheme.spacing.xxl))
            UsButton(
                text = if (report == UsPostReportState.Failed) "Try again" else "Report",
                onClick = { chosen?.let { onSubmit(it, note) } },
                enabled = chosen != null,
                loading = report == UsPostReportState.Sending,
                modifier = Modifier
                    .fillMaxWidth()
                    .testTag("live-report-submit"),
            )
        }
    }
}

@Composable
private fun ReportOutcome(text: String) {
    Text(
        text,
        style = MaterialTheme.typography.bodyMedium,
        color = UsTheme.extended.textSecondary,
        modifier = Modifier.padding(vertical = UsTheme.spacing.xxl),
    )
}

// ── Host: one message ───────────────────────────────────────────────────

/** The host's actions on one chat message, already in alphabetical order. */
@Composable
fun HostMessageSheet(
    message: LiveChatMessageDto,
    actions: List<HostMessageAction>,
    onAction: (HostMessageAction) -> Unit,
    onDismiss: () -> Unit,
) {
    LiveSheet(tag = "live-host-message-sheet", onDismiss = onDismiss) {
        HostMessageContent(message = message, actions = actions, onAction = onAction)
    }
}

@Composable
private fun HostMessageContent(
    message: LiveChatMessageDto,
    actions: List<HostMessageAction>,
    onAction: (HostMessageAction) -> Unit,
) {
    MessageHeader(message)
    actions.forEach { action ->
        SheetRow(label = action.label, tag = "live-host-${action.name}", onClick = { onAction(action) })
    }
}

// ── Viewer who moderates: one message ───────────────────────────────────

/** A stream moderator's actions on one chat message, already in alphabetical order. */
@Composable
fun ViewerMessageSheet(
    message: LiveChatMessageDto,
    actions: List<ViewerMessageAction>,
    onAction: (ViewerMessageAction) -> Unit,
    onDismiss: () -> Unit,
) {
    LiveSheet(tag = "live-viewer-message-sheet", onDismiss = onDismiss) {
        ViewerMessageContent(message = message, actions = actions, onAction = onAction)
    }
}

@Composable
private fun ViewerMessageContent(
    message: LiveChatMessageDto,
    actions: List<ViewerMessageAction>,
    onAction: (ViewerMessageAction) -> Unit,
) {
    MessageHeader(message)
    actions.forEach { action ->
        SheetRow(label = action.label, tag = "live-viewer-${action.name}", onClick = { onAction(action) })
    }
}

@Composable
private fun MessageHeader(message: LiveChatMessageDto) {
    Text(
        shortUserLabel(message.userId),
        style = MaterialTheme.typography.labelMedium,
        color = UsTheme.extended.textMuted,
    )
    Text(
        message.text,
        style = MaterialTheme.typography.bodyMedium,
        color = UsTheme.extended.textPrimary,
        modifier = Modifier.padding(bottom = UsTheme.spacing.m),
    )
}

// ── Host: moderators and bans ───────────────────────────────────────────

/** Who moderates this stream, and who was banned from it this session. */
@Composable
fun HostToolsSheet(
    moderators: List<String>,
    banned: Set<String>,
    onRemoveModerator: (String) -> Unit,
    onUnban: (String) -> Unit,
    onDismiss: () -> Unit,
) {
    LiveSheet(tag = "live-host-tools-sheet", onDismiss = onDismiss) {
        HostToolsContent(moderators, banned, onRemoveModerator, onUnban)
    }
}

@Composable
private fun HostToolsContent(
    moderators: List<String>,
    banned: Set<String>,
    onRemoveModerator: (String) -> Unit,
    onUnban: (String) -> Unit,
) {
    SheetTitle("Moderators (${moderators.size} of $MAX_STREAM_MODERATORS)")
    if (moderators.isEmpty()) {
        HelpLine("Tap a chat message to make its author a moderator.")
    }
    moderators.sortedBy { shortUserLabel(it) }.forEach { userId ->
        PersonRow(userId = userId, actionLabel = "Remove", tag = "live-moderator-remove", onAction = onRemoveModerator)
    }
    Spacer(Modifier.height(UsTheme.spacing.xxl))
    SheetTitle("Banned from this stream")
    if (banned.isEmpty()) {
        HelpLine("Nobody yet. Tap a chat message to ban its author.")
    }
    banned.sortedBy { shortUserLabel(it) }.forEach { userId ->
        PersonRow(userId = userId, actionLabel = "Unban", tag = "live-ban-lift", onAction = onUnban)
    }
}

@Composable
private fun HelpLine(text: String) {
    Text(text, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
}

@Composable
private fun PersonRow(userId: String, actionLabel: String, tag: String, onAction: (String) -> Unit) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        modifier = Modifier.fillMaxWidth(),
    ) {
        Text(
            shortUserLabel(userId),
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.weight(1f),
        )
        TextButton(onClick = { onAction(userId) }, modifier = Modifier.testTag(tag)) {
            Text(actionLabel, color = UsTheme.extended.accentSolid)
        }
    }
}

// ── Host: end ───────────────────────────────────────────────────────────

/** Ending is final, so it asks first. */
@Composable
fun EndStreamDialog(onConfirm: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        containerColor = UsTheme.extended.bgRaised,
        title = { Text("End your stream?", color = UsTheme.extended.textPrimary) },
        text = { Text("Viewers will see that the stream has ended.", color = UsTheme.extended.textSecondary) },
        confirmButton = {
            TextButton(onClick = onConfirm, modifier = Modifier.testTag("live-end-confirm")) {
                Text("End", color = UsTheme.extended.statusDanger, fontWeight = FontWeight.SemiBold)
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss, modifier = Modifier.testTag("live-end-cancel")) {
                Text("Keep streaming", color = UsTheme.extended.textMuted)
            }
        },
        modifier = Modifier.testTag("live-end-dialog"),
    )
}

private const val REPORT_CLOSE_DELAY_MILLIS = 1_200L
private const val REPORT_NOTE_MAX = 500

// ── Previews ────────────────────────────────────────────────────────────

private val previewMessage = LiveChatMessageDto(id = "m1", userId = "5f0c2a9e-1111", text = "hello from the chat")

@Preview
@Composable
private fun LiveReportContentPreview() {
    UsTheme(darkTheme = true) {
        Column { LiveReportContent(aboutMessage = true, report = UsPostReportState.Idle, onSubmit = { _, _ -> }) }
    }
}

@Preview
@Composable
private fun HostMessageContentPreview() {
    UsTheme(darkTheme = true) {
        Column {
            HostMessageContent(
                message = previewMessage,
                actions = listOf(
                    HostMessageAction.Ban,
                    HostMessageAction.MakeModerator,
                    HostMessageAction.RemoveMessage,
                ),
                onAction = {},
            )
        }
    }
}

@Preview
@Composable
private fun ViewerMessageContentPreview() {
    UsTheme(darkTheme = true) {
        Column {
            ViewerMessageContent(
                message = previewMessage,
                actions = listOf(
                    ViewerMessageAction.Ban,
                    ViewerMessageAction.RemoveMessage,
                    ViewerMessageAction.Report,
                ),
                onAction = {},
            )
        }
    }
}

@Preview
@Composable
private fun HostToolsContentPreview() {
    UsTheme(darkTheme = true) {
        Column {
            HostToolsContent(
                moderators = listOf("5f0c2a9e-1111"),
                banned = setOf("7a7a7a7a-2222"),
                onRemoveModerator = {},
                onUnban = {},
            )
        }
    }
}

@Preview
@Composable
private fun EndStreamDialogPreview() {
    UsTheme(darkTheme = true) { EndStreamDialog(onConfirm = {}, onDismiss = {}) }
}
