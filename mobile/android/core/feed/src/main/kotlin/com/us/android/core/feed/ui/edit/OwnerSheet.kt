package com.us.android.core.feed.ui.edit

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme

/**
 * The frame both owner sheets of a video's More menu share (2026-10-21):
 * "Audio tracks" and "Edit". The Create-channel sheet's idiom — navy
 * `bgSheet`, 28dp corners, the scrim, a grab handle drawn inside — with a
 * titled header, a close button, and a body that scrolls above the keyboard.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun OwnerSheet(
    title: String,
    subtitle: String,
    icon: ImageVector,
    testTag: String,
    onDismiss: () -> Unit,
    content: @Composable ColumnScope.() -> Unit,
) {
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = sheetState,
        containerColor = UsTheme.extended.bgSheet,
        contentColor = UsTheme.extended.textPrimary,
        shape = RoundedCornerShape(topStart = SHEET_RADIUS, topEnd = SHEET_RADIUS),
        scrimColor = UsTheme.extended.scrim,
        dragHandle = null,
        modifier = Modifier.testTag(testTag),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(bottom = UsTheme.spacing.xxl)
                .navigationBarsPadding()
                .imePadding(),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            GrabHandle()
            Header(title = title, subtitle = subtitle, icon = icon, testTag = testTag, onClose = onDismiss)
            content()
        }
    }
}

@Composable
private fun GrabHandle() {
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

@Composable
private fun Header(title: String, subtitle: String, icon: ImageVector, testTag: String, onClose: () -> Unit) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        Icon(
            imageVector = icon,
            contentDescription = null,
            tint = UsTheme.extended.textPrimary,
            modifier = Modifier.size(HEADER_GLYPH),
        )
        Column(modifier = Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
            Text(
                text = title,
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
            )
            Text(
                text = subtitle,
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
        }
        Box(
            modifier = Modifier
                .size(CLOSE_BUTTON)
                .clip(CircleShape)
                .background(UsTheme.extended.glassBg)
                .clickable(
                    interactionSource = remember { MutableInteractionSource() },
                    indication = null,
                    onClick = onClose,
                )
                .semantics {
                    contentDescription = "Close"
                    role = Role.Button
                }
                .testTag("$testTag:close"),
            contentAlignment = Alignment.Center,
        ) {
            Icon(
                imageVector = UsIcons.Close,
                contentDescription = null,
                tint = UsTheme.extended.textPrimary,
                modifier = Modifier.size(CLOSE_GLYPH),
            )
        }
    }
}

/** A line under a form: a refusal in the error colour, or a confirmation in the success one. */
@Composable
internal fun SheetNote(text: String, isError: Boolean, testTag: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.bodySmall,
        color = if (isError) MaterialTheme.colorScheme.error else UsTheme.extended.statusSuccess,
        modifier = Modifier.testTag(testTag),
    )
}

private const val HANDLE_ALPHA = 0.35f

private val SHEET_RADIUS = 28.dp
private val HANDLE_WIDTH = 32.dp
private val HANDLE_HEIGHT = 4.dp
private val HANDLE_TOP = 8.dp
private val HEADER_GLYPH = 22.dp
private val CLOSE_BUTTON = 30.dp
private val CLOSE_GLYPH = 14.dp
