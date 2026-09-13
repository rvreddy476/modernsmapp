package com.us.android.core.facear.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.facear.TryOnDiagnostics
import com.us.android.core.facear.diagnosticLines

/**
 * The try-on's own answer to "what actually happened".
 *
 * ## WHY A PANEL AND NOT A LOG LINE
 *
 * The device this runs on is the founder's personal phone with a live SIM. It
 * is not attached to adb. Every failure mode of a try-on looks identical from
 * the outside — a camera with a bare face — so without this, each guess costs
 * a build, an install and a message. The panel turns that into one screenshot.
 *
 * The lines themselves are built by the pure [diagnosticLines], which is where
 * the wording of every state is decided and tested. This file only draws them.
 *
 * ## DEBUG ONLY, AND THE GUARD IS THE CALLER'S
 *
 * [FaceArTryOnSurface] renders this behind `BuildConfig.DEBUG`. It prints the
 * vendor's raw error strings and the effect's internals; none of that belongs
 * in front of a shopper, and none of it ships.
 *
 * Collapsed by default, as a small affordance in the corner, so it does not
 * take part in the design of the screen it is diagnosing.
 */
@Composable
internal fun TryOnDiagnosticsPanel(
    diagnostics: TryOnDiagnostics,
    expanded: Boolean,
    onToggle: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier,
        horizontalAlignment = Alignment.End,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        DiagnosticsAffordance(expanded = expanded, onToggle = onToggle)
        if (expanded) {
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(UsTheme.radii.medium))
                    // Near-opaque, not the glass token: this is text that has
                    // to be READABLE over a moving camera feed, and a
                    // translucent panel over a face is exactly where a
                    // screenshot becomes useless.
                    .background(PANEL)
                    .border(
                        width = HAIRLINE,
                        color = UsTheme.extended.borderMedium,
                        shape = RoundedCornerShape(UsTheme.radii.medium),
                    )
                    .heightIn(max = PANEL_MAX_HEIGHT)
                    .verticalScroll(rememberScrollState())
                    .padding(UsTheme.spacing.l),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
            ) {
                Text(
                    text = "Try-on diagnostics (debug build only)",
                    style = MaterialTheme.typography.labelMedium,
                    color = UsTheme.extended.accentSolid,
                )
                diagnosticLines(diagnostics).forEach { line ->
                    Text(
                        text = line,
                        // Monospace: these lines are read aloud and compared
                        // between runs, and proportional text makes a changed
                        // character hard to spot.
                        style = MaterialTheme.typography.bodySmall
                            .copy(fontFamily = FontFamily.Monospace),
                        color = Color.White,
                    )
                }
            }
        }
    }
}

@Composable
private fun DiagnosticsAffordance(expanded: Boolean, onToggle: () -> Unit) {
    Row(
        modifier = Modifier
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .background(PANEL)
            .clickable(onClick = onToggle)
            .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s)
            .semantics {
                contentDescription =
                    if (expanded) "Hide try-on diagnostics" else "Show try-on diagnostics"
            },
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(
            imageVector = UsIcons.Info,
            contentDescription = null,
            tint = UsTheme.extended.accentSolid,
            modifier = Modifier.size(GLYPH),
        )
        Text(
            text = "Diagnostics",
            style = MaterialTheme.typography.labelMedium,
            color = Color.White,
        )
    }
}

private val PANEL = Color(0xF0041122)
private val PANEL_MAX_HEIGHT = 320.dp
private val GLYPH = 14.dp
private val HAIRLINE = 1.dp
