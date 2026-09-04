package com.us.android.feature.tube.ui.watch

import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
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
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsReelQuality

/**
 * The gear's sheet: Quality — Auto and the HLS ladder the player reports,
 * tallest first — and Speed, 0.5x to 2x. Two short groups of rows with a
 * check at the chosen one; a pick applies at once and the sheet stays, so
 * two changes are two taps, not four.
 *
 * Quality offers Auto alone for an original MP4, which has no ladder; the
 * group is still drawn so the viewer sees why there is nothing to pick.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun WatchSettingsSheet(
    qualities: List<UsReelQuality>,
    selectedQuality: UsReelQuality,
    speed: Float,
    onSelectQuality: (UsReelQuality) -> Unit,
    onSelectSpeed: (Float) -> Unit,
    onDismiss: () -> Unit,
) {
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgCardSolid,
        contentColor = UsTheme.extended.textPrimary,
        shape = RoundedCornerShape(topStart = SHEET_RADIUS, topEnd = SHEET_RADIUS),
        scrimColor = Color.Black.copy(alpha = SCRIM_ALPHA),
        modifier = Modifier.testTag("watch_settings_sheet"),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(bottom = UsTheme.spacing.xxl)
                .navigationBarsPadding(),
        ) {
            GroupTitle(icon = UsIcons.Sliders, text = "Quality")
            qualities.forEach { quality ->
                OptionRow(
                    label = quality.label,
                    selected = quality == selectedQuality,
                    onClick = { onSelectQuality(quality) },
                    tag = "watch_quality:${quality.label}",
                )
            }
            Spacer(Modifier.height(UsTheme.spacing.xl))
            GroupTitle(icon = UsIcons.Gauge, text = "Speed")
            PLAYBACK_SPEEDS.forEach { option ->
                OptionRow(
                    label = speedLabel(option),
                    selected = option == speed,
                    onClick = { onSelectSpeed(option) },
                    tag = "watch_speed:${speedLabel(option)}",
                )
            }
        }
    }
}

@Composable
private fun GroupTitle(icon: ImageVector, text: String) {
    Row(
        modifier = Modifier.padding(vertical = UsTheme.spacing.m),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        Icon(
            imageVector = icon,
            contentDescription = null,
            tint = UsTheme.extended.textMuted,
            modifier = Modifier.size(GLYPH),
        )
        Text(
            text = text,
            style = MaterialTheme.typography.titleMedium,
            color = UsTheme.extended.textPrimary,
        )
    }
}

/** A label with a check at the right when chosen. No ripple; the row is the target. */
@Composable
private fun OptionRow(label: String, selected: Boolean, onClick: () -> Unit, tag: String) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                role = Role.RadioButton,
                onClick = onClick,
            )
            .semantics { this.selected = selected }
            .padding(vertical = UsTheme.spacing.l)
            .testTag(tag),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodyLarge,
            fontWeight = if (selected) FontWeight.SemiBold else FontWeight.Normal,
            color = if (selected) UsTheme.extended.textPrimary else UsTheme.extended.textSecondary,
            modifier = Modifier.weight(1f),
        )
        if (selected) {
            Icon(
                imageVector = UsIcons.Check,
                contentDescription = null,
                tint = UsTheme.extended.accentSolid,
                modifier = Modifier.size(GLYPH),
            )
        }
    }
}

private const val SCRIM_ALPHA = 0.55f
private val SHEET_RADIUS = 28.dp
private val GLYPH = 18.dp
