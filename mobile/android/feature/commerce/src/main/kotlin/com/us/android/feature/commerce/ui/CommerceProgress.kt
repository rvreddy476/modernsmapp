package com.us.android.feature.commerce.ui

import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.theme.UsTheme

/**
 * The shop's one "working on it" indicator: a 3dp ember line on a hairline
 * track.
 *
 * Material's spinner is the wrong shape here twice over. It is a Material
 * mark on a screen that is otherwise entirely Momentum, and its indeterminate
 * arc says "something is happening" without saying how much — which for a
 * document upload is exactly the information the seller wants.
 *
 * [progress] null means indeterminate: a short ember band sweeps the track,
 * the same gesture as the skeleton shimmer. A value in 0..1 fills from the
 * left. [contentDescription] is announced by TalkBack, because a bare line is
 * invisible to a screen reader — pass what is being waited for.
 */
@Composable
internal fun CommerceProgressLine(
    modifier: Modifier = Modifier,
    progress: Float? = null,
    contentDescription: String? = null,
) {
    val track = UsTheme.extended.borderMedium
    val ember = UsTheme.extended.ctaGradient
    val sweep by rememberInfiniteTransition(label = "commerceProgress").animateFloat(
        initialValue = 0f,
        targetValue = 1f,
        animationSpec = infiniteRepeatable(
            animation = tween(durationMillis = SWEEP_MILLIS, easing = LinearEasing),
            repeatMode = RepeatMode.Restart,
        ),
        label = "commerceProgressSweep",
    )

    Canvas(
        modifier = modifier
            .fillMaxWidth()
            .height(LINE_HEIGHT)
            .semantics { contentDescription?.let { this.contentDescription = it } },
    ) {
        val radius = CornerRadius(size.height / 2)
        drawRoundRect(color = track, cornerRadius = radius)

        if (progress != null) {
            val filled = size.width * progress.coerceIn(0f, 1f)
            if (filled > 0f) {
                drawRoundRect(brush = ember, size = size.copy(width = filled), cornerRadius = radius)
            }
            return@Canvas
        }

        // Indeterminate: a band the width of a third of the track, entering
        // from the left edge and leaving past the right, so the ends are
        // never clipped mid-stroke.
        val band = size.width * BAND_FRACTION
        val travel = size.width + band
        val left = sweep * travel - band
        val start = left.coerceAtLeast(0f)
        val end = (left + band).coerceAtMost(size.width)
        if (end > start) {
            drawRoundRect(
                brush = ember,
                topLeft = Offset(start, 0f),
                size = Size(end - start, size.height),
                cornerRadius = radius,
            )
        }
    }
}

private const val SWEEP_MILLIS = 1_100
private const val BAND_FRACTION = 0.35f
private val LINE_HEIGHT = 3.dp
