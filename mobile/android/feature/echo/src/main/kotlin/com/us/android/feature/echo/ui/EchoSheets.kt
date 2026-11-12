package com.us.android.feature.echo.ui

import android.content.Context
import android.content.Intent
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
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.WebLinks
import com.us.android.core.ui.UsPostReportState
import com.us.android.core.ui.UsPostReportStep
import com.us.android.feature.echo.data.Note
import kotlinx.coroutines.delay

/**
 * Why this viewer cannot post or reply, in plain words. No purchase button
 * and no link out: Google Play's payments policy is still being checked
 * (founder decision 4), so Android does not sell Premium here.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun EligibilitySheet(blocked: ComposeGate.Blocked, onDismiss: () -> Unit) {
    ModalBottomSheet(onDismissRequest = onDismiss, containerColor = UsTheme.extended.bgCardSolid) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .navigationBarsPadding()
                .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l)
                .testTag("echo_eligibility_sheet"),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            Text(
                text = blocked.title,
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
            )
            Text(
                text = blocked.detail,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary
            )
            UsButton(
                text = "OK",
                onClick = onDismiss,
                modifier = Modifier.fillMaxWidth().testTag("echo_eligibility_ok")
            )
        }
    }
}

/**
 * A note's ⋮: Edit (the author, within 30 minutes) and Delete (the author),
 * or Report (anyone else) through the shared report step, target `post`.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun NoteMoreSheet(
    menu: NoteMenu,
    report: UsPostReportState,
    onEdit: () -> Unit,
    onDelete: () -> Unit,
    onReport: (reason: String, details: String) -> Unit,
    onDismiss: () -> Unit,
) {
    var reporting by rememberSaveable { mutableStateOf(false) }
    var confirmDelete by rememberSaveable { mutableStateOf(false) }
    LaunchedEffect(report) {
        if (report == UsPostReportState.Sent || report == UsPostReportState.AlreadyReported) {
            delay(REPORT_CLOSE_MILLIS)
            onDismiss()
        }
    }
    ModalBottomSheet(onDismissRequest = onDismiss, containerColor = UsTheme.extended.bgCardSolid) {
        Column(modifier = Modifier.fillMaxWidth().navigationBarsPadding().padding(bottom = UsTheme.spacing.l)) {
            when {
                reporting -> UsPostReportStep(
                    report = report,
                    onBack = { reporting = false },
                    onSubmit = { reason, details -> onReport(reason.wire, details) },
                    subject = "note",
                )
                confirmDelete -> Column(
                    modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal),
                    verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
                ) {
                    Text(
                        text = "Delete this note?",
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.Bold,
                        color = UsTheme.extended.textPrimary,
                    )
                    Text(
                        text = "It will be removed from Echo and from your profile.",
                        style = MaterialTheme.typography.bodyMedium,
                        color = UsTheme.extended.textSecondary,
                    )
                    UsButton(
                        text = "Delete",
                        onClick = onDelete,
                        modifier = Modifier.fillMaxWidth().testTag("echo_delete_confirm")
                    )
                }
                else -> {
                    if (menu.edit) MenuRow(UsIcons.Pencil, "Edit", "edit", onEdit)
                    if (menu.delete) MenuRow(UsIcons.Trash, "Delete", "delete") { confirmDelete = true }
                    if (menu.report) MenuRow(UsIcons.Flag, "Report", "report") { reporting = true }
                }
            }
        }
    }
}

@Composable
private fun MenuRow(icon: ImageVector, label: String, tag: String, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l)
            .testTag("echo_menu:$tag"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Icon(icon, contentDescription = null, tint = UsTheme.extended.textPrimary)
        Text(text = label, style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textPrimary)
    }
}

/** The system chooser with the note's web address, `https://cleestudio.com/echo/note/{id}`. */
@Composable
fun rememberNoteSharer(): (Note) -> Unit {
    val context = LocalContext.current
    return remember(context) { { note -> context.shareNote(note) } }
}

/** "Alice Rao on Echo: Rain all week… https://cleestudio.com/echo/note/{id}?ref=…". */
fun noteShareText(note: Note): String {
    val link = WebLinks.withShareRef(WebLinks.echoNotePermalink(note.id))
    val who = note.author?.name?.takeIf { it.isNotBlank() }
    val excerpt = note.text.trim().take(
        SHARE_EXCERPT
    ).let { if (note.text.trim().length > SHARE_EXCERPT) "$it…" else it }
    val head = listOfNotNull(who?.let { "$it on Echo" }, excerpt.takeIf { it.isNotBlank() }).joinToString(": ")
    return if (head.isBlank()) link else "$head\n$link"
}

private fun Context.shareNote(note: Note) {
    val send = Intent(Intent.ACTION_SEND).apply {
        type = "text/plain"
        putExtra(Intent.EXTRA_TEXT, noteShareText(note))
    }
    runCatching { startActivity(Intent.createChooser(send, null).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
}

private const val REPORT_CLOSE_MILLIS = 1_200L
private const val SHARE_EXCERPT = 120
