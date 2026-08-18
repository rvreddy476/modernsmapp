package com.us.android.core.designsystem.modifier

import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color

/**
 * Darkens the area behind text that sits on top of media.
 *
 * WHY THIS EXISTS
 *
 * Observed on a device on 2026-08-18. Reels captions were drawn in white
 * directly over the video, which is legible only for as long as the video
 * happens to be dark. The test fixture is colour bars, and the caption landed
 * on saturated yellow and cyan — white on yellow is roughly a 1.07:1 contrast
 * ratio, i.e. invisible.
 *
 * The point is that we do not control the pixels underneath. User video can be
 * any colour at any moment, so foreground colour alone can never be sufficient;
 * the only fix that holds for every frame is to put a known-dark surface
 * between the video and the text.
 *
 * The gradient stays clear at the top so there is no visible edge where the
 * scrim begins — a hard line across a video reads as a rendering bug.
 */
fun Modifier.usMediaScrim(): Modifier = drawBehind {
    drawRect(
        brush = Brush.verticalGradient(
            colorStops = arrayOf(
                0f to Color.Transparent,
                SCRIM_RAMP_START to Color.Black.copy(alpha = SCRIM_MID_ALPHA),
                1f to Color.Black.copy(alpha = SCRIM_MAX_ALPHA),
            ),
            startY = 0f,
            endY = size.height,
        ),
    )
}

/**
 * The scrim reaches most of its strength in the first quarter of the height.
 *
 * Tuned against a device capture, not chosen for looks: the caption sits near
 * the TOP of the overlay, so a gradient that only gets dark near the bottom
 * darkens the empty space below the text and leaves the text itself washed out
 * — which is what the first attempt did.
 */
private const val SCRIM_RAMP_START = 0.25f
private const val SCRIM_MID_ALPHA = 0.70f

/**
 * Not fully opaque. The scrim has to win against the brightest possible frame
 * while still reading as an overlay on the video rather than a black bar.
 */
private const val SCRIM_MAX_ALPHA = 0.88f
