package com.us.android.core.designsystem.component

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsCreateSwatch
import com.us.android.core.designsystem.theme.UsTheme

/**
 * Momentum's app-icon mark: a rounded square filled with a swatch's
 * gradient, a gloss fading down from the top edge, a white glyph, and the
 * swatch's glow cast beneath as a coloured shadow. Corners at a third of
 * the side — the app-icon proportion — so every square reads as one family
 * at any size.
 *
 * Born in the Create sheet (founder render, 2026-09-04) and lifted here the
 * day the Explore launcher needed the same tile at 56dp (2026-09-05): one
 * drawing, so the two grids cannot drift apart.
 */
@Composable
fun UsIconSquare(
    swatch: UsCreateSwatch,
    icon: ImageVector,
    size: Dp,
    glyph: Dp,
    modifier: Modifier = Modifier,
) {
    val shape = RoundedCornerShape(size / ICON_CORNER_DIVISOR)
    val gloss = remember {
        Brush.verticalGradient(
            colorStops = arrayOf(
                0f to Color.White.copy(alpha = GLOSS_ALPHA),
                GLOSS_END to Color.Transparent,
            ),
        )
    }
    Box(
        modifier = modifier
            .size(size)
            .shadow(
                elevation = ICON_GLOW,
                shape = shape,
                ambientColor = swatch.glow,
                spotColor = swatch.glow,
            )
            .background(swatch.brush, shape)
            .background(gloss, shape),
        contentAlignment = Alignment.Center,
    ) {
        Icon(
            imageVector = icon,
            contentDescription = null,
            tint = Color.White,
            modifier = Modifier.size(glyph),
        )
    }
}

@Preview(name = "Icon square", showBackground = true, backgroundColor = 0xFF041122)
@Composable
private fun UsIconSquarePreview() {
    UsTheme {
        UsIconSquare(swatch = UsTheme.extended.launcher.chat, icon = UsIcons.Comment, size = 56.dp, glyph = 26.dp)
    }
}

private const val GLOSS_ALPHA = 0.28f
private const val GLOSS_END = 0.55f
private const val ICON_CORNER_DIVISOR = 3
private val ICON_GLOW = 10.dp
