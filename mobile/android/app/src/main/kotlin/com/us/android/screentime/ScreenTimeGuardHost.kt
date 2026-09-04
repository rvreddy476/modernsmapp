package com.us.android.screentime

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
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
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.theme.UsTheme

/**
 * The daily-limit / sleep-hours nudge, over whatever is on screen.
 *
 * A sheet, never a wall. The first cut was Material's AlertDialog with a
 * lone "OK"; on the founder's phone (2026-09-04) a 30-minute limit turned
 * into "I can't do anything, I can't even change it back". Whatever the
 * exact cause, a nudge that can trap its own settings screen behind it is
 * the wrong shape. This one offers three real ways out — keep going,
 * snooze for a quarter hour, or go straight to the Screen time setting —
 * and dismisses on scrim tap and Back like every other Momentum sheet.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ScreenTimeGuardHost(
    onChangeLimit: () -> Unit,
    viewModel: ScreenTimeGuardViewModel = hiltViewModel(),
) {
    val message by viewModel.message.collectAsStateWithLifecycle()
    val current = message ?: return
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)

    ModalBottomSheet(
        onDismissRequest = viewModel::dismiss,
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
                .padding(horizontal = CONTENT_PADDING)
                .padding(bottom = CONTENT_PADDING)
                .navigationBarsPadding(),
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
            Text(
                text = current.title(),
                style = MaterialTheme.typography.titleLarge,
                color = UsTheme.extended.textPrimary,
            )
            Spacer(Modifier.height(UsTheme.spacing.m))
            Text(
                text = current.body(),
                style = MaterialTheme.typography.bodyLarge,
                color = UsTheme.extended.textMuted,
            )
            Spacer(Modifier.height(UsTheme.spacing.xxxxl))
            GuardAction(label = "Keep going", primary = true, onClick = viewModel::dismiss)
            Spacer(Modifier.height(UsTheme.spacing.m))
            GuardAction(label = "Remind me in 15 minutes", primary = false, onClick = viewModel::snooze)
            Spacer(Modifier.height(UsTheme.spacing.m))
            GuardAction(
                label = if (current == ScreenTimeGuardMessage.DAILY_LIMIT) "Change my limit" else "Change sleep hours",
                primary = false,
                onClick = {
                    viewModel.dismiss()
                    onChangeLimit()
                },
            )
        }
    }
}

/** A full-width pill: ember fill for the primary way out, glass for the others. */
@Composable
private fun GuardAction(label: String, primary: Boolean, onClick: () -> Unit) {
    val shape = RoundedCornerShape(UsTheme.radii.full)
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .clip(shape)
            .then(
                if (primary) {
                    Modifier.background(UsTheme.extended.ctaGradient, shape)
                } else {
                    Modifier.background(UsTheme.extended.glassBg, shape)
                },
            )
            .clickable(onClick = onClick)
            .padding(vertical = ACTION_PADDING)
            .semantics { role = Role.Button },
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodyLarge,
            fontWeight = FontWeight.SemiBold,
            color = if (primary) Color.White else UsTheme.extended.textPrimary,
        )
    }
}

private fun ScreenTimeGuardMessage.title(): String = when (this) {
    ScreenTimeGuardMessage.DAILY_LIMIT -> "You've reached your daily limit"
    ScreenTimeGuardMessage.SLEEP_TIME -> "It's your sleep time"
}

private fun ScreenTimeGuardMessage.body(): String = when (this) {
    ScreenTimeGuardMessage.DAILY_LIMIT ->
        "You've used as much time today as you asked us to allow. Nothing is locked — this is a reminder."
    ScreenTimeGuardMessage.SLEEP_TIME ->
        "This is inside the sleep hours you set. Nothing is locked — this is a reminder."
}

private const val SCRIM_ALPHA = 0.55f
private const val HANDLE_ALPHA = 0.35f
private val SHEET_RADIUS = 28.dp
private val CONTENT_PADDING = 20.dp
private val HANDLE_WIDTH = 32.dp
private val HANDLE_HEIGHT = 4.dp
private val HANDLE_TOP = 8.dp
private val HANDLE_BOTTOM = 16.dp
private val ACTION_PADDING = 14.dp
