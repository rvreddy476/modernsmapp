package com.us.android.core.feed.ui.sound

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.media.sound.SoundPreview

/**
 * What the preview button says to a screen reader: what a tap will do, which
 * is not always what the glyph shows — a sound still loading is paused by a
 * tap, not played.
 */
fun soundPreviewDescription(preview: SoundPreview): String = when (preview) {
    SoundPreview.PLAYING -> "Pause sound"
    SoundPreview.LOADING -> "Loading sound. Pause"
    SoundPreview.IDLE, SoundPreview.PAUSED, SoundPreview.FAILED -> "Play sound"
}

/**
 * The round button that plays a sound alone (original sounds, 2026-09-30):
 * play, pause, or a ring while the sound loads. One drawing for the sound
 * page and the reel form's Sound section, so the two cannot drift; it lives
 * here because features must not depend on each other.
 *
 * On the ember gradient, like every primary control, with the glyph in the
 * scheme's on-primary.
 */
@Composable
fun SoundPreviewButton(
    preview: SoundPreview,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    size: Dp = BUTTON,
) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier
            .size(size)
            .clip(CircleShape)
            .background(UsTheme.extended.ctaGradient)
            .clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                enabled = enabled,
                role = Role.Button,
                onClick = onClick,
            )
            .semantics { contentDescription = soundPreviewDescription(preview) }
            .testTag("sound_preview"),
    ) {
        if (preview == SoundPreview.LOADING) {
            CircularProgressIndicator(
                color = MaterialTheme.colorScheme.onPrimary,
                strokeWidth = RING_STROKE,
                modifier = Modifier.size(size * GLYPH_FRACTION),
            )
        } else {
            Icon(
                imageVector = if (preview == SoundPreview.PLAYING) UsIcons.Pause else UsIcons.Play,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onPrimary,
                modifier = Modifier.size(size * GLYPH_FRACTION),
            )
        }
    }
}

@Preview
@Composable
private fun SoundPreviewButtonPreview() {
    UsTheme {
        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
            SoundPreview.entries.forEach { SoundPreviewButton(preview = it, onClick = {}) }
        }
    }
}

private val BUTTON = 56.dp
private val RING_STROKE = 2.dp
private const val GLYPH_FRACTION = 0.42f
