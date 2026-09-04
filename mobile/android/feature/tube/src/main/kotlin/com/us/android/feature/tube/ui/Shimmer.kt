package com.us.android.feature.tube.ui

import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import com.us.android.core.designsystem.theme.UsTheme

/**
 * A placeholder block that shimmers while the page loads: the raised
 * surface with a soft band of light sweeping across it. Cards draw their
 * own shape from these so the skeleton has the layout of the page it
 * stands in for — a spinner says "wait", a skeleton says "this is what is
 * coming". Announced once, as a whole, by the screen; each block is silent.
 */
@Composable
fun Modifier.shimmer(shape: Shape): Modifier {
    val transition = rememberInfiniteTransition(label = "tubeShimmer")
    val sweep by transition.animateFloat(
        initialValue = 0f,
        targetValue = 1f,
        animationSpec = infiniteRepeatable(
            animation = tween(durationMillis = SWEEP_MILLIS, easing = LinearEasing),
            repeatMode = RepeatMode.Restart,
        ),
        label = "tubeShimmerSweep",
    )
    val base = UsTheme.extended.bgRaised
    val highlight = UsTheme.extended.bgCardHover
    val brush = Brush.linearGradient(
        colors = listOf(base, highlight, base),
        start = Offset(x = (sweep * SWEEP_SPAN - SWEEP_BAND), y = 0f),
        end = Offset(x = sweep * SWEEP_SPAN, y = SWEEP_BAND),
    )
    return this
        .clip(shape)
        .background(brush)
        .semantics { contentDescription = "" }
}

private const val SWEEP_MILLIS = 1_400

/** The band travels this many pixels per cycle — wider than a phone at any density. */
private const val SWEEP_SPAN = 2_400f
private const val SWEEP_BAND = 600f
