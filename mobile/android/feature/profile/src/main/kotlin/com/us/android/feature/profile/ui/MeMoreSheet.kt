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
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import kotlinx.coroutines.launch

/**
 * The rows of the Me tab's More menu (2026-10-02).
 *
 * founder, 2026-10-02: every page but Home reads Search, the bell, More at
 * its top-right corner. The Me tab had no menu, so this is a NEW and
 * deliberately short one, and every row is a screen that already exists:
 * Messages (a glyph on this header until today, which the standard corner
 * has no place for) and Settings (still beside "Edit profile" as well).
 */
enum class MeMenuRow(val label: String, val icon: ImageVector) {
    MESSAGES("Messages", UsIcons.Comment),
    SETTINGS("Settings", UsIcons.Settings),
}

/**
 * The menu, in ascending alphabetical order by label: the rule for every
 * menu. Settings is offered only when the page was given somewhere to send
 * it; a row that does nothing is never drawn. Pure, so it is a table test.
 */
fun meMenuRows(hasSettings: Boolean): List<MeMenuRow> =
    MeMenuRow.entries
        .filter { it != MeMenuRow.SETTINGS || hasSettings }
        .sortedWith(compareBy(String.CASE_INSENSITIVE_ORDER) { it.label })

/**
 * The Me tab's More sheet: the Momentum sheet idiom Home's and Tube's menus
 * use — the sheet surface, 28dp corners, a grab handle inside the content,
 * 52dp rows — and their rule: a row slides the sheet away FIRST and then
 * acts, so what it opens lands on a clear screen.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun MeMoreSheet(rows: List<MeMenuRow>, onRow: (MeMenuRow) -> Unit, onDismiss: () -> Unit) {
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    val scope = rememberCoroutineScope()
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = sheetState,
        containerColor = UsTheme.extended.bgSheet,
        contentColor = UsTheme.extended.textPrimary,
        shape = RoundedCornerShape(topStart = SHEET_RADIUS, topEnd = SHEET_RADIUS),
        scrimColor = UsTheme.extended.scrim,
        dragHandle = null,
        modifier = Modifier.testTag("me_menu_sheet"),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .navigationBarsPadding()
                .padding(bottom = CONTENT_BOTTOM),
        ) {
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
            rows.forEach { row ->
                MeMenuRowItem(
                    row = row,
                    onClick = {
                        scope.launch { sheetState.hide() }.invokeOnCompletion {
                            onDismiss()
                            onRow(row)
                        }
                    },
                )
            }
        }
    }
}

@Composable
private fun MeMenuRowItem(row: MeMenuRow, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .height(ROW_HEIGHT)
            .clickable(role = Role.Button, onClickLabel = row.label, onClick = onClick)
            .padding(horizontal = ROW_SIDE)
            .testTag("me_menu_row:${row.name.lowercase()}"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(ROW_GAP),
    ) {
        Icon(
            imageVector = row.icon,
            contentDescription = null,
            tint = UsTheme.extended.textPrimary,
            modifier = Modifier.size(ROW_GLYPH),
        )
        Text(
            text = row.label,
            style = MaterialTheme.typography.bodyLarge,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.weight(1f),
        )
    }
}

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
