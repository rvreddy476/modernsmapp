package com.us.android.core.ui

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.rememberScrollState
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.layout.Layout
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.unit.Constraints
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp

/**
 * A row of tabs or pills that FITS when it can and SCROLLS when it cannot
 * (2026-10-17).
 *
 * When every child fits an equal column, the columns are equal and span the
 * width — a tab row's usual look. When they fit only at their own widths,
 * the spare width is shared out. When they do not fit at all, each keeps
 * its natural width and the row scrolls sideways. A label is never
 * squeezed, wrapped, cut at the edge or drawn over its neighbour — the
 * Messages tabs ("Communities" over "Suggestions") and the Creator Hub's
 * "Sounds" at the screen's edge were both a fixed share too small for the
 * word.
 *
 * [gap] sits between children and [edge] at both ends, inside the scroll.
 */
@Composable
fun UsFitOrScrollRow(
    modifier: Modifier = Modifier,
    gap: Dp = 0.dp,
    edge: Dp = 0.dp,
    content: @Composable () -> Unit,
) {
    val density = LocalDensity.current
    val gapPx = with(density) { gap.roundToPx() }
    val edgePx = with(density) { edge.roundToPx() }
    BoxWithConstraints(modifier = modifier) {
        val viewport = constraints.maxWidth
        Layout(content = content, modifier = Modifier.horizontalScroll(rememberScrollState())) { measurables, c ->
            val natural = measurables.map { it.maxIntrinsicWidth(c.maxHeight) }
            val gaps = gapPx * (measurables.size - 1).coerceAtLeast(0)
            val widths = fitWidths(natural, viewport - gaps - 2 * edgePx)
            val placeables = measurables.mapIndexed { i, measurable ->
                measurable.measure(
                    Constraints(
                        minWidth = widths[i],
                        maxWidth = widths[i],
                        minHeight = c.minHeight,
                        maxHeight = c.maxHeight,
                    ),
                )
            }
            val width = widths.sum() + gaps + 2 * edgePx
            val height = placeables.maxOfOrNull { it.height } ?: c.minHeight
            layout(width, height) {
                var x = edgePx
                placeables.forEach { placeable ->
                    placeable.placeRelative(x, (height - placeable.height) / 2)
                    x += placeable.width + gapPx
                }
            }
        }
    }
}

/**
 * The widths [UsFitOrScrollRow] lays its children out at, given their
 * [natural] widths and the [available] width: equal columns when the widest
 * child fits one, the spare shared out equally when only the total fits,
 * and the natural widths (the row then scrolls) when even that does not.
 * Pure, so the three cases are a table test.
 */
fun fitWidths(natural: List<Int>, available: Int): List<Int> {
    if (natural.isEmpty()) return natural
    val count = natural.size
    val column = available / count
    return when {
        natural.max() <= column -> spread(List(count) { column }, available - column * count)
        natural.sum() <= available -> spread(natural, available - natural.sum())
        else -> natural
    }
}

/** [widths] with [extra] pixels added, one share each, the remainder to the first ones. */
private fun spread(widths: List<Int>, extra: Int): List<Int> {
    val share = extra / widths.size
    val rest = extra % widths.size
    return widths.mapIndexed { i, w -> w + share + if (i < rest) 1 else 0 }
}
