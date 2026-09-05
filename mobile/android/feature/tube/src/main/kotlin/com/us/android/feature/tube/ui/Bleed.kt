package com.us.android.feature.tube.ui

import androidx.compose.ui.Modifier
import androidx.compose.ui.layout.layout
import androidx.compose.ui.unit.Dp

/**
 * Lets a full-width row run out past the grid's gutter on both sides — a
 * strip or a carousel whose cards should reach the screen's edge while the
 * tiles beside it keep the page margin. The row is measured [gutter] wider
 * on each side and placed that much to the left; the grid still sees a
 * row of its own width, so nothing else moves.
 */
fun Modifier.bleed(gutter: Dp): Modifier = layout { measurable, constraints ->
    val extra = (gutter * 2).roundToPx()
    val placeable = measurable.measure(
        constraints.copy(
            minWidth = (constraints.minWidth + extra).coerceAtMost(constraints.maxWidth + extra),
            maxWidth = constraints.maxWidth + extra,
        ),
    )
    layout(placeable.width - extra, placeable.height) {
        placeable.place(-gutter.roundToPx(), 0)
    }
}
