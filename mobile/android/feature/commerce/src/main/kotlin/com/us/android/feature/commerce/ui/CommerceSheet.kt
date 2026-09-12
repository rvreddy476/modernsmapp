package com.us.android.feature.commerce.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.theme.UsTheme

/**
 * A Momentum bottom sheet with a title.
 *
 * Every confirmation in the app comes up from the bottom on the card
 * surface: the screen-time nudge, the post menu, the buyer's cancel-order
 * sheet. The seller's ship and cancel sheets need the same frame, and a
 * second hand-rolled copy of the handle and the radius is how two sheets
 * drift a pixel apart. The buyer's sheet predates this and keeps its own
 * private copy; unifying it is a separate, cosmetic change.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun CommerceSheet(
    title: String,
    onDismiss: () -> Unit,
    content: @Composable ColumnScope.() -> Unit,
) {
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = sheetState,
        containerColor = UsTheme.extended.bgCardSolid,
        contentColor = UsTheme.extended.textPrimary,
        shape = RoundedCornerShape(topStart = SHEET_RADIUS, topEnd = SHEET_RADIUS),
        scrimColor = Color.Black.copy(alpha = SCRIM_ALPHA),
        dragHandle = null,
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(bottom = UsTheme.spacing.pageHorizontal)
                .navigationBarsPadding(),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            SheetHandle()
            Text(
                text = title,
                style = MaterialTheme.typography.titleLarge,
                color = UsTheme.extended.textPrimary,
            )
            content()
        }
    }
}

/** 32x4, muted at 35%: the same handle every Momentum sheet wears. */
@Composable
private fun SheetHandle() {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .padding(top = HANDLE_TOP),
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

private const val SCRIM_ALPHA = 0.55f
private const val HANDLE_ALPHA = 0.35f
private val SHEET_RADIUS = 28.dp
private val HANDLE_WIDTH = 32.dp
private val HANDLE_HEIGHT = 4.dp
private val HANDLE_TOP = 8.dp
