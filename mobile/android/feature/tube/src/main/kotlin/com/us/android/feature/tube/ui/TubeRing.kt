package com.us.android.feature.tube.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.theme.UsTheme

/**
 * The ember progress ring — the Reels avatar ring, drawn from 12 o'clock
 * over a faint track — around whatever is put inside it. Tube's
 * "Continue watching" cards wear it with the time left inside.
 */
@Composable
fun TubeRing(
    progress: Float,
    modifier: Modifier = Modifier,
    stroke: Dp = RING_STROKE,
    gap: Dp = RING_GAP,
    content: @Composable () -> Unit,
) {
    val track = Color.White.copy(alpha = TRACK_ALPHA)
    val played = UsTheme.extended.ctaGradient
    Box(
        modifier = modifier
            .drawBehind {
                val width = stroke.toPx()
                val inset = width / 2
                val arcSize = Size(size.width - width, size.height - width)
                drawArc(
                    color = track,
                    startAngle = START_ANGLE,
                    sweepAngle = FULL_SWEEP,
                    useCenter = false,
                    topLeft = Offset(inset, inset),
                    size = arcSize,
                    style = Stroke(width = width),
                )
                if (progress > 0f) {
                    drawArc(
                        brush = played,
                        startAngle = START_ANGLE,
                        sweepAngle = FULL_SWEEP * progress.coerceIn(0f, 1f),
                        useCenter = false,
                        topLeft = Offset(inset, inset),
                        size = arcSize,
                        style = Stroke(width = width, cap = StrokeCap.Round),
                    )
                }
            }
            .padding(stroke + gap),
        contentAlignment = Alignment.Center,
    ) {
        content()
    }
}

private const val TRACK_ALPHA = 0.25f
private const val START_ANGLE = -90f
private const val FULL_SWEEP = 360f
private val RING_STROKE = 3.dp
private val RING_GAP = 2.dp
