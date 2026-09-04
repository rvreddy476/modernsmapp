package com.us.android.core.designsystem.component

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.defaultMinSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.us.android.core.designsystem.theme.UsTheme

/**
 * Primary action button.
 *
 * Uses the CTA gradient from the Flutter reference rather than a flat fill,
 * because the gradient is part of the brand identity rather than decoration.
 * Falls back to a flat disabled surface when not enabled.
 */
@Composable
fun UsButton(
    text: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    loading: Boolean = false,
) {
    val shape = androidx.compose.foundation.shape.RoundedCornerShape(UsTheme.radii.full)
    Button(
        onClick = onClick,
        modifier = modifier.defaultMinSize(minHeight = 48.dp),
        enabled = enabled && !loading,
        shape = shape,
        contentPadding = PaddingValues(0.dp),
        colors = ButtonDefaults.buttonColors(
            containerColor = Color.Transparent,
            disabledContainerColor = Color.Transparent,
        ),
    ) {
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .clip(shape)
                .then(
                    // A loading button keeps the gradient. Only a genuinely
                    // disabled one goes flat — otherwise "submitting" is
                    // indistinguishable from "you can't press this".
                    if (enabled) {
                        Modifier.background(UsTheme.extended.ctaGradient)
                    } else {
                        Modifier.background(UsTheme.extended.bgCardHover)
                    },
                )
                .padding(vertical = 14.dp),
            contentAlignment = Alignment.Center,
        ) {
            if (loading) {
                CircularProgressIndicator(
                    // size(), not defaultMinSize(): the latter only sets a
                    // floor, so the indicator kept its ~40dp default and
                    // rendered as an oversized arc.
                    modifier = Modifier.size(20.dp),
                    strokeWidth = 2.dp,
                    color = Color.White,
                )
            } else {
                Text(
                    text = text,
                    style = MaterialTheme.typography.labelLarge,
                    color = if (enabled) Color.White else UsTheme.extended.textDim,
                )
            }
        }
    }
}

/** Secondary action. Outlined, no gradient — it must not compete with [UsButton]. */
@Composable
fun UsSecondaryButton(
    text: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
) {
    OutlinedButton(
        onClick = onClick,
        modifier = modifier.defaultMinSize(minHeight = 48.dp),
        enabled = enabled,
        shape = androidx.compose.foundation.shape.RoundedCornerShape(UsTheme.radii.full),
        border = androidx.compose.foundation.BorderStroke(1.dp, UsTheme.extended.borderMedium),
    ) {
        Text(
            text = text,
            style = MaterialTheme.typography.labelLarge,
            color = UsTheme.extended.textSecondary,
        )
    }
}

@Preview(name = "Buttons", showBackground = true, backgroundColor = 0xFF000000)
@Composable
private fun UsButtonPreview() {
    UsTheme {
        Column(
            modifier = Modifier
                .background(MaterialTheme.colorScheme.background)
                .padding(UsTheme.spacing.pageHorizontal),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            UsButton(text = "Continue", onClick = {}, modifier = Modifier.fillMaxWidth())
            UsButton(
                text = "Loading",
                onClick = {},
                modifier = Modifier.fillMaxWidth(),
                loading = true,
            )
            UsButton(
                text = "Disabled",
                onClick = {},
                modifier = Modifier.fillMaxWidth(),
                enabled = false,
            )
            UsSecondaryButton(text = "Not now", onClick = {}, modifier = Modifier.fillMaxWidth())
        }
    }
}

/**
 * Momentum's small inline pill — "Follow back", Accept, Decline.
 *
 * Gradient-filled by default (6dp corners, 12x6 padding, 11sp bold); pass
 * [filled] = false for the neutral outlined twin that sits beside it. Kept
 * separate from [UsButton] on purpose: that is a full-width 48dp CTA, and
 * shrinking it into a row would have meant a control that is neither.
 */
@Composable
fun UsPillButton(
    text: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    filled: Boolean = true,
    enabled: Boolean = true,
    busy: Boolean = false,
) {
    val shape = androidx.compose.foundation.shape.RoundedCornerShape(UsTheme.radii.pill)
    val active = enabled && !busy
    val fill = when {
        !filled -> Modifier.border(1.dp, UsTheme.extended.borderMedium, shape)
        active -> Modifier.background(UsTheme.extended.ctaGradient, shape)
        else -> Modifier.background(UsTheme.extended.bgCardHover, shape)
    }
    val labelColor = when {
        !filled && active -> UsTheme.extended.textPrimary
        !filled -> UsTheme.extended.textMuted
        active -> Color.White
        else -> UsTheme.extended.textDim
    }
    Box(
        modifier = modifier
            .clip(shape)
            .then(fill)
            .clickable(enabled = active, onClick = onClick)
            .semantics { role = Role.Button }
            .padding(horizontal = PILL_HORIZONTAL, vertical = PILL_VERTICAL),
        contentAlignment = Alignment.Center,
    ) {
        if (busy) {
            CircularProgressIndicator(
                modifier = Modifier.size(PILL_SPINNER),
                strokeWidth = 2.dp,
                color = labelColor,
            )
        } else {
            Text(
                text = text,
                style = MaterialTheme.typography.labelSmall,
                fontSize = PILL_TEXT,
                fontWeight = FontWeight.Bold,
                color = labelColor,
            )
        }
    }
}

/**
 * THE follow button — the same control wherever an account can be
 * followed: the post header's right end, the reel's author row, a profile.
 *
 * WHITE with navy text, so "follow" reads the same on a navy card, over a
 * video and on a profile (founder, 2026-09-05: the ember version "looked
 * odd"; one colour, kept consistent). A capsule, 32dp tall, 13sp semibold —
 * a real button, not an inline text link. The ground navy, not the accent,
 * for the text: white-on-navy is the brand pairing and it holds up over
 * any video frame.
 */
@Composable
fun UsFollowButton(
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    text: String = "Follow",
    busy: Boolean = false,
) {
    val shape = androidx.compose.foundation.shape.RoundedCornerShape(UsTheme.radii.full)
    Box(
        modifier = modifier
            .clip(shape)
            .background(Color.White, shape)
            .clickable(enabled = !busy, onClick = onClick)
            .semantics {
                role = Role.Button
                contentDescription = text
            }
            .padding(horizontal = FOLLOW_HORIZONTAL, vertical = FOLLOW_VERTICAL),
        contentAlignment = Alignment.Center,
    ) {
        if (busy) {
            CircularProgressIndicator(
                modifier = Modifier.size(PILL_SPINNER),
                strokeWidth = 2.dp,
                color = FOLLOW_TEXT_NAVY,
            )
        } else {
            Text(
                text = text,
                style = MaterialTheme.typography.labelLarge,
                fontSize = FOLLOW_TEXT,
                fontWeight = FontWeight.SemiBold,
                color = FOLLOW_TEXT_NAVY,
                maxLines = 1,
            )
        }
    }
}

private val PILL_HORIZONTAL = 12.dp
private val PILL_VERTICAL = 6.dp
private val PILL_TEXT = 11.sp
private val PILL_SPINNER = 12.dp
private val FOLLOW_HORIZONTAL = 16.dp
private val FOLLOW_VERTICAL = 7.dp
private val FOLLOW_TEXT = 13.sp

/** Momentum ground navy — the same value the bar's create tile cuts its plus in. */
private val FOLLOW_TEXT_NAVY = Color(0xFF041122)

@Preview(name = "Pills", showBackground = true, backgroundColor = 0xFF041122)
@Composable
private fun UsPillButtonPreview() {
    UsTheme {
        Row(
            modifier = Modifier.padding(UsTheme.spacing.l),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        ) {
            UsPillButton(text = "Follow back", onClick = {})
            UsPillButton(text = "Decline", onClick = {}, filled = false)
            UsPillButton(text = "Busy", onClick = {}, busy = true)
            UsPillButton(text = "Following", onClick = {}, filled = false, enabled = false)
        }
    }
}
