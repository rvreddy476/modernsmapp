// MatchingDeclarationName: the file is the sheet; the enum is its rows.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.feed.ui

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
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import kotlinx.coroutines.launch

/**
 * The rows of Home's More menu (2026-10-02).
 *
 * founder, 2026-10-02: every section's top-right corner is Search, then the
 * three-dots More. Reels and Tube already had a menu behind their More; Home
 * had none, so this is a NEW, deliberately short one, and every row is a
 * screen that already exists: the Friends feed and Live (until now reached
 * only through the Explore launcher) and Settings (until now only through
 * the Me tab). Messages and Notifications are NOT here: they stay on the
 * bar, where the unread count is read at a glance.
 */
enum class HomeMenuRow(val label: String, val icon: ImageVector) {
    FRIENDS("Friends", UsIcons.Friends),
    LIVE("Live", UsIcons.Live),
    SETTINGS("Settings", UsIcons.Settings),
}

/** The menu, in ascending alphabetical order by label: the rule for every menu. Pure, so it is a table test. */
fun homeMenuRows(): List<HomeMenuRow> =
    HomeMenuRow.entries.sortedWith(compareBy(String.CASE_INSENSITIVE_ORDER) { it.label })

/**
 * Home's More sheet: the Momentum sheet idiom the Tube menu uses — the sheet
 * surface, 28dp corners, a grab handle inside the content, 52dp rows — and
 * the Create sheet's rule: a row slides the sheet away FIRST and then acts,
 * so what it opens lands on a clear screen. [onRow] is resolved by `:app`.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun HomeMoreSheet(onRow: (HomeMenuRow) -> Unit, onDismiss: () -> Unit) {
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
        modifier = Modifier.testTag("home_menu_sheet"),
    ) {
        HomeMenuRows(
            onRow = { row ->
                scope.launch { sheetState.hide() }.invokeOnCompletion {
                    onDismiss()
                    onRow(row)
                }
            },
        )
    }
}

@Composable
private fun HomeMenuRows(onRow: (HomeMenuRow) -> Unit) {
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
        homeMenuRows().forEach { row ->
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .height(ROW_HEIGHT)
                    .clickable(role = Role.Button, onClickLabel = row.label, onClick = { onRow(row) })
                    .padding(horizontal = ROW_SIDE)
                    .testTag("home_menu_row:${row.name.lowercase()}"),
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

@Preview
@Composable
private fun HomeMenuRowsPreview() {
    UsTheme {
        Box(Modifier.background(UsTheme.extended.bgSheet)) { HomeMenuRows(onRow = {}) }
    }
}
