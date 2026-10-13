package com.us.android.feature.commerce.ui

import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.runtime.Composable
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.unit.dp

/** More columns on a tablet; fewer at large text sizes. Never squeeze a price to fit. */
@Composable
fun commerceGridCells(): GridCells = GridCells.Adaptive(
    minSize = MIN_CARD_WIDTH * LocalDensity.current.fontScale.coerceIn(1f, MAX_GRID_FONT_SCALE),
)

private val MIN_CARD_WIDTH = 148.dp
private const val MAX_GRID_FONT_SCALE = 1.6f
