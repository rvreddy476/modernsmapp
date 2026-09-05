package com.us.android.feature.post.createhub.studio

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
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
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme

/**
 * "Preparing your reel… 42 %" — the Momentum sheet over the studio while
 * the Transformer renders (2026-09-05). The percent is the sheet's one
 * big number; the bar under it is the ember gradient; Cancel stops the
 * render and deletes the half-written file. Swiping the sheet away is the
 * same as Cancel — there is no way to hide a render and keep it running.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun ExportSheet(percent: Int, onCancel: () -> Unit) {
    ModalBottomSheet(
        onDismissRequest = onCancel,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgCardSolid,
        contentColor = UsTheme.extended.textPrimary,
        shape = RoundedCornerShape(topStart = SHEET_RADIUS, topEnd = SHEET_RADIUS),
        scrimColor = Color.Black.copy(alpha = SCRIM_ALPHA),
        dragHandle = null,
        modifier = Modifier.testTag("studio-export-sheet"),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(bottom = UsTheme.spacing.xxl)
                .navigationBarsPadding(),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Handle()
            Text(
                text = "Preparing your reel…",
                style = MaterialTheme.typography.titleLarge.copy(fontSize = TITLE_SIZE),
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier.fillMaxWidth(),
            )
            Spacer(Modifier.height(UsTheme.spacing.xxl))
            Text(
                text = "$percent %",
                style = MaterialTheme.typography.displaySmall.copy(fontSize = PERCENT_SIZE),
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier
                    .semantics { contentDescription = "Preparing your reel, $percent percent" }
                    .testTag("studio-export-percent"),
            )
            Spacer(Modifier.height(UsTheme.spacing.l))
            ProgressBar(fraction = percent / PERCENT)
            Spacer(Modifier.height(UsTheme.spacing.xxl))
            UsSecondaryButton(
                text = "Cancel",
                onClick = onCancel,
                modifier = Modifier
                    .fillMaxWidth()
                    .testTag("studio-export-cancel"),
            )
        }
    }
}

/** The error twin: why the render stopped, and one button back to the studio. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun ExportErrorSheet(message: String, onDismiss: () -> Unit) {
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgCardSolid,
        contentColor = UsTheme.extended.textPrimary,
        shape = RoundedCornerShape(topStart = SHEET_RADIUS, topEnd = SHEET_RADIUS),
        scrimColor = Color.Black.copy(alpha = SCRIM_ALPHA),
        dragHandle = null,
        modifier = Modifier.testTag("studio-export-error"),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(bottom = UsTheme.spacing.xxl)
                .navigationBarsPadding(),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            Handle()
            Text(
                text = "Couldn't prepare your reel",
                style = MaterialTheme.typography.titleLarge.copy(fontSize = TITLE_SIZE),
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
            )
            Text(text = message, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary)
            UsSecondaryButton(text = "OK", onClick = onDismiss, modifier = Modifier.fillMaxWidth())
        }
    }
}

@Composable
private fun Handle() {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .padding(top = UsTheme.spacing.m, bottom = UsTheme.spacing.xxl),
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

/** A track with the ember gradient filling [fraction] of it, left to right. */
@Composable
private fun ProgressBar(fraction: Float) {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .height(BAR_HEIGHT)
            .clip(CircleShape)
            .background(Color.White.copy(alpha = TRACK_ALPHA)),
    ) {
        Box(
            modifier = Modifier
                .fillMaxWidth(fraction.coerceIn(0f, 1f))
                .height(BAR_HEIGHT)
                .clip(CircleShape)
                .background(UsTheme.extended.ctaGradient),
        )
    }
}

private const val PERCENT = 100f
private const val SCRIM_ALPHA = 0.55f
private const val HANDLE_ALPHA = 0.35f
private const val TRACK_ALPHA = 0.12f
private val SHEET_RADIUS = 28.dp
private val HANDLE_WIDTH = 32.dp
private val HANDLE_HEIGHT = 4.dp
private val BAR_HEIGHT = 6.dp
private val TITLE_SIZE = 20.sp
private val PERCENT_SIZE = 40.sp
