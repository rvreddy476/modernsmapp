package com.us.android.feature.commerce.ui

import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.spring
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics

/**
 * No ripple. The press is shown by the target dipping to 97% on a spring —
 * the app's one touch (Tube, Search, Chat, the feed card). Every tappable
 * thing in the shop uses this modifier, so commerce feels like the rest of
 * the app rather than like a Material sample.
 *
 * [role] is a parameter because the shop taps three different kinds of
 * thing: buttons, list rows that open a screen, and the option rows in the
 * variant / payment / address pickers, which must announce as radio buttons
 * or a screen-reader user cannot tell what "selected" means.
 */
@Composable
internal fun Modifier.pressScale(
    onClick: () -> Unit,
    enabled: Boolean = true,
    role: Role = Role.Button,
): Modifier {
    val interaction = remember { MutableInteractionSource() }
    val pressed by interaction.collectIsPressedAsState()
    val scale by animateFloatAsState(
        targetValue = if (pressed) PRESS_SCALE else 1f,
        animationSpec = spring(dampingRatio = Spring.DampingRatioMediumBouncy, stiffness = PRESS_STIFFNESS),
        label = "commercePress",
    )
    val requestedRole = role
    return this
        .clickable(interactionSource = interaction, indication = null, enabled = enabled, onClick = onClick)
        .semantics { this.role = requestedRole }
        .graphicsLayer {
            scaleX = scale
            scaleY = scale
        }
}

private const val PRESS_SCALE = 0.97f
private const val PRESS_STIFFNESS = 1200f
