package com.us.android.core.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.engagement.data.CommentMenuAction
import com.us.android.core.engagement.data.CommentReportStatus
import com.us.android.core.engagement.data.CommentRow
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * One comment's ⋮ (2026-10-14, audit T-17): the rows [actions] allows —
 * see `commentMenuActions` for who gets which — in the post sheet's idiom.
 *
 * Report steps INSIDE the sheet into the shared reason list
 * ([UsPostReportStep], subject "comment"), so a comment is reported with
 * exactly the reasons and outcomes a post or an account is. Delete asks once
 * ("Delete this comment?") in the same card the post sheet uses. Pin and
 * heart act at once and close the sheet: they are reversible from the same
 * row.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun UsCommentActionsSheet(
    row: CommentRow,
    actions: List<CommentMenuAction>,
    report: CommentReportStatus,
    callbacks: UsCommentsCallbacks,
    onDismiss: () -> Unit,
) {
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    val scope = rememberCoroutineScope()
    var reporting by rememberSaveable { mutableStateOf(false) }
    var confirmDelete by rememberSaveable { mutableStateOf(false) }

    fun leave() {
        scope.launch { sheetState.hide() }.invokeOnCompletion {
            callbacks.onReportClosed()
            onDismiss()
        }
    }

    // A filed (or already-filed) report shows its sentence for a beat, then
    // the sheet leaves on its own — the post and profile sheets' rule.
    LaunchedEffect(report) {
        if (report == CommentReportStatus.SENT || report == CommentReportStatus.ALREADY_REPORTED) {
            delay(REPORT_LINGER_MILLIS)
            leave()
        }
    }

    // Delete takes the sheet away FIRST, then asks (2026-10-17): the sheet
    // stayed up under "Delete this comment?", two surfaces at once.
    fun onAction(action: CommentMenuAction) {
        when (action) {
            CommentMenuAction.REPORT -> reporting = true
            CommentMenuAction.DELETE -> scope.launch { sheetState.hide() }.invokeOnCompletion { confirmDelete = true }
            CommentMenuAction.PIN, CommentMenuAction.UNPIN -> {
                callbacks.onTogglePin(row.id)
                leave()
            }
            CommentMenuAction.HEART, CommentMenuAction.UNHEART -> {
                callbacks.onToggleHeart(row.id)
                leave()
            }
        }
    }

    if (!confirmDelete) {
        ModalBottomSheet(
            onDismissRequest = {
                callbacks.onReportClosed()
                onDismiss()
            },
            sheetState = sheetState,
            containerColor = UsTheme.extended.bgSheet,
            contentColor = UsTheme.extended.textPrimary,
            shape = RoundedCornerShape(topStart = SHEET_RADIUS, topEnd = SHEET_RADIUS),
            scrimColor = UsTheme.extended.scrim,
            dragHandle = null,
            modifier = Modifier.testTag("comment_menu_sheet:${row.id}"),
        ) {
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .navigationBarsPadding()
                    .padding(bottom = CONTENT_BOTTOM),
            ) {
                SheetGrabHandle()
                if (reporting) {
                    UsPostReportStep(
                        report = report.asReportState(),
                        onBack = {
                            callbacks.onReportClosed()
                            reporting = false
                        },
                        onSubmit = { reason, details -> callbacks.onReport(row.id, reason.wire, details) },
                        subject = "comment",
                    )
                } else {
                    actions.forEach { action ->
                        SheetRow(
                            icon = action.icon(),
                            label = action.label,
                            tint = if (action.destructive) UsTheme.extended.liveRed else UsTheme.extended.textPrimary,
                            testTag = "comment_menu_row:${action.name.lowercase()}",
                            onClick = { onAction(action) },
                        )
                    }
                }
            }
        }
    }

    if (confirmDelete) {
        ConfirmDialog(
            title = if (row.isReply) "Delete this reply?" else "Delete this comment?",
            body = "It will be removed from the conversation. This can't be undone.",
            confirmLabel = "Delete",
            testTag = "comment_delete_dialog",
            confirmTestTag = "comment_delete_confirm",
            // The sheet has already gone, so either answer closes the menu.
            onConfirm = {
                callbacks.onDelete(row.id)
                callbacks.onReportClosed()
                onDismiss()
            },
            onDismiss = {
                callbacks.onReportClosed()
                onDismiss()
            },
        )
    }
}

private fun CommentMenuAction.icon(): ImageVector = when (this) {
    CommentMenuAction.DELETE -> UsIcons.Trash
    CommentMenuAction.HEART -> UsIcons.HeartOutline
    CommentMenuAction.UNHEART -> UsIcons.HeartFilled
    CommentMenuAction.PIN, CommentMenuAction.UNPIN -> UsIcons.Pin
    CommentMenuAction.REPORT -> UsIcons.Flag
}

/** The comment report's progress, in the shared report step's terms. */
internal fun CommentReportStatus.asReportState(): UsPostReportState = when (this) {
    CommentReportStatus.IDLE -> UsPostReportState.Idle
    CommentReportStatus.SENDING -> UsPostReportState.Sending
    CommentReportStatus.SENT -> UsPostReportState.Sent
    CommentReportStatus.ALREADY_REPORTED -> UsPostReportState.AlreadyReported
    CommentReportStatus.FAILED -> UsPostReportState.Failed
}

private const val REPORT_LINGER_MILLIS = 1_400L
private val SHEET_RADIUS = 28.dp
private val CONTENT_BOTTOM = 12.dp
