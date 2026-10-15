// MatchingDeclarationName: the file is the sheet; the enum is its rows.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.profile.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsPostReportState
import com.us.android.core.ui.UsPostReportStep
import com.us.android.core.ui.UsReportReason
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * The rows of the More menu on SOMEONE ELSE's profile (2026-10-14, audit T-03).
 *
 * Report is the only row today; the menu exists so the account can be
 * flagged at all — before this, abuse on a profile had no report path on
 * Android. [destructive] rows read in the danger tint, as Report does on the
 * post sheet.
 */
enum class ProfileMenuRow(val label: String, val icon: ImageVector, val destructive: Boolean) {
    REPORT("Report", UsIcons.Flag, destructive = true),
}

/**
 * The menu for a profile: empty on the viewer's OWN profile — nobody reports
 * themselves, and the Me tab has its own menu ([meMenuRows]) — and Report on
 * anyone else's. Ascending alphabetical order by label, the rule for every
 * menu. Pure, so it is a table test; the screen draws no More control at all
 * when this is empty.
 */
fun profileMenuRows(isOwnProfile: Boolean): List<ProfileMenuRow> =
    if (isOwnProfile) {
        emptyList()
    } else {
        ProfileMenuRow.entries.sortedWith(compareBy(String.CASE_INSENSITIVE_ORDER) { it.label })
    }

/**
 * The profile's More sheet: the Momentum sheet idiom [MeMoreSheet] and the
 * post sheet use (sheet surface, 28dp corners, an inner grab handle, 52dp
 * rows). Report steps INSIDE the sheet into the post sheet's own reason list
 * ([UsPostReportStep], subject "account"), so a profile is reported with
 * exactly the reasons and outcomes a post is. A filed (or already-filed)
 * report shows its sentence for a beat, then the sheet leaves on its own.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun ProfileMoreSheet(
    rows: List<ProfileMenuRow>,
    report: UsPostReportState,
    onReport: (reason: UsReportReason, details: String) -> Unit,
    onDismiss: () -> Unit,
) {
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    val scope = rememberCoroutineScope()
    var reporting by rememberSaveable { mutableStateOf(false) }

    fun leave() {
        scope.launch { sheetState.hide() }.invokeOnCompletion { onDismiss() }
    }

    LaunchedEffect(report) {
        if (report == UsPostReportState.Sent || report == UsPostReportState.AlreadyReported) {
            delay(REPORT_LINGER_MILLIS)
            leave()
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
        modifier = Modifier.testTag("profile_menu_sheet"),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .navigationBarsPadding()
                .padding(bottom = CONTENT_BOTTOM),
        ) {
            GrabHandle()
            if (reporting) {
                UsPostReportStep(
                    report = report,
                    onBack = { reporting = false },
                    onSubmit = onReport,
                    subject = "account",
                )
            } else {
                rows.forEach { row ->
                    ProfileMenuRowItem(
                        row = row,
                        onClick = {
                            when (row) {
                                ProfileMenuRow.REPORT -> reporting = true
                            }
                        },
                    )
                }
            }
        }
    }
}

@Composable
private fun GrabHandle() {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .padding(top = HANDLE_TOP, bottom = HANDLE_BOTTOM),
        contentAlignment = Alignment.Center,
    ) {
        Box(
            modifier = Modifier
                .size(width = HANDLE_WIDTH, height = HANDLE_HEIGHT)
                .clip(CircleShape)
                .background(UsTheme.extended.textMuted.copy(alpha = HANDLE_ALPHA)),
        )
    }
}

@Composable
private fun ProfileMenuRowItem(row: ProfileMenuRow, onClick: () -> Unit) {
    val tint = if (row.destructive) UsTheme.extended.liveRed else UsTheme.extended.textPrimary
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .height(ROW_HEIGHT)
            .clickable(role = Role.Button, onClickLabel = row.label, onClick = onClick)
            .padding(horizontal = ROW_SIDE)
            .testTag("profile_menu_row:${row.name.lowercase()}"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(ROW_GAP),
    ) {
        Icon(
            imageVector = row.icon,
            contentDescription = null,
            tint = tint,
            modifier = Modifier.size(ROW_GLYPH),
        )
        Text(
            text = row.label,
            style = MaterialTheme.typography.bodyLarge,
            color = tint,
            modifier = Modifier.weight(1f),
        )
    }
}

private const val REPORT_LINGER_MILLIS = 1_400L
private const val HANDLE_ALPHA = 0.35f
private val SHEET_RADIUS = 28.dp
private val CONTENT_BOTTOM = 12.dp
private val HANDLE_TOP = 10.dp
private val HANDLE_BOTTOM = 8.dp
private val HANDLE_WIDTH = 32.dp
private val HANDLE_HEIGHT = 4.dp
private val ROW_HEIGHT = 52.dp
private val ROW_SIDE = 20.dp
private val ROW_GAP = 16.dp
private val ROW_GLYPH = 22.dp
