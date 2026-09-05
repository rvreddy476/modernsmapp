package com.us.android.feature.chat.ui.home

import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.spring
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.us.android.core.designsystem.theme.UsTheme

/**
 * No ripple. The press is shown by the target dipping to 97% on a spring —
 * the app's one touch (Tube, Search, the feed card). Every tappable thing on
 * the chat surfaces uses this modifier.
 */
@Composable
internal fun Modifier.pressScale(onClick: () -> Unit, enabled: Boolean = true): Modifier {
    val interaction = remember { MutableInteractionSource() }
    val pressed by interaction.collectIsPressedAsState()
    val scale by animateFloatAsState(
        targetValue = if (pressed) PRESS_SCALE else 1f,
        animationSpec = spring(dampingRatio = Spring.DampingRatioMediumBouncy, stiffness = PRESS_STIFFNESS),
        label = "chatPress",
    )
    return this
        .clickable(interactionSource = interaction, indication = null, enabled = enabled, onClick = onClick)
        .graphicsLayer {
            scaleX = scale
            scaleY = scale
        }
}

/** A square target, the icon in white, no ripple — the header's glyph. */
@Composable
internal fun HeaderGlyph(
    icon: ImageVector,
    description: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    tag: String? = null,
    size: Dp = GLYPH_TARGET,
    glyph: Dp = GLYPH_SIZE,
    tint: Color = Color.White,
) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier
            .size(size)
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                contentDescription = description
            }
            .then(if (tag != null) Modifier.testTag(tag) else Modifier),
    ) {
        Icon(imageVector = icon, contentDescription = null, tint = tint, modifier = Modifier.size(glyph))
    }
}

/**
 * The surface's action pill — "New group", "Create community": an ember
 * capsule with a white glyph and label, 36dp tall. The one accent on a
 * list, so the way to make something is the loudest thing on it.
 */
@Composable
internal fun ChatActionPill(
    text: String,
    icon: ImageVector,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    tag: String? = null,
) {
    val shape = RoundedCornerShape(UsTheme.radii.full)
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        modifier = modifier
            .height(ACTION_PILL_HEIGHT)
            .background(UsTheme.extended.ctaGradient, shape)
            .pressScale(onClick)
            .semantics { role = Role.Button }
            .then(if (tag != null) Modifier.testTag(tag) else Modifier)
            .padding(horizontal = ACTION_PILL_HORIZONTAL),
    ) {
        Icon(
            imageVector = icon,
            contentDescription = null,
            tint = Color.White,
            modifier = Modifier.size(ACTION_PILL_GLYPH)
        )
        Text(
            text = text,
            style = MaterialTheme.typography.labelLarge,
            fontSize = ACTION_PILL_TEXT,
            fontWeight = FontWeight.SemiBold,
            color = Color.White,
            maxLines = 1,
        )
    }
}

/**
 * The membership pill — Join / Joined / Follow-style toggles on a card.
 * Selected is WHITE with navy text (the bar's rule; never the accent);
 * unselected is a glass capsule with a hairline border and white text.
 */
@Composable
internal fun ChatTogglePill(
    text: String,
    selected: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    busy: Boolean = false,
    tag: String? = null,
) {
    val shape = RoundedCornerShape(UsTheme.radii.full)
    val fill = if (selected) Color.White else UsTheme.extended.glassBg
    val outline = if (selected) Color.White else UsTheme.extended.glassBorder
    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier
            .height(TOGGLE_PILL_HEIGHT)
            .background(fill, shape)
            .border(HAIRLINE, outline, shape)
            .pressScale(onClick, enabled = !busy)
            .semantics {
                role = Role.Button
                contentDescription = text
            }
            .then(if (tag != null) Modifier.testTag(tag) else Modifier)
            .padding(horizontal = TOGGLE_PILL_HORIZONTAL),
    ) {
        Text(
            text = if (busy) "…" else text,
            style = MaterialTheme.typography.labelLarge,
            fontSize = TOGGLE_PILL_TEXT,
            fontWeight = FontWeight.SemiBold,
            color = if (selected) UsTheme.extended.brandNavy else UsTheme.extended.textPrimary,
            maxLines = 1,
        )
    }
}

/** The uppercase muted section label every chat list uses. */
@Composable
internal fun ChatSectionLabel(label: String, modifier: Modifier = Modifier) {
    Text(
        text = label.uppercase(),
        style = MaterialTheme.typography.labelSmall,
        color = UsTheme.extended.textMuted,
        modifier = modifier.padding(
            horizontal = UsTheme.spacing.pageHorizontal,
            vertical = UsTheme.spacing.m,
        ),
    )
}

private const val PRESS_SCALE = 0.97f
private const val PRESS_STIFFNESS = 1200f
private val GLYPH_TARGET = 44.dp
private val GLYPH_SIZE = 24.dp
private val ACTION_PILL_HEIGHT = 36.dp
private val ACTION_PILL_HORIZONTAL = 14.dp
private val ACTION_PILL_GLYPH = 16.dp
private val ACTION_PILL_TEXT = 13.sp
private val TOGGLE_PILL_HEIGHT = 32.dp
private val TOGGLE_PILL_HORIZONTAL = 14.dp
private val TOGGLE_PILL_TEXT = 13.sp
private val HAIRLINE = 1.dp
