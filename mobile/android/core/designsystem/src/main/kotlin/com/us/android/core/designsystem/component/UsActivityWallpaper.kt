package com.us.android.core.designsystem.component

import androidx.compose.foundation.Canvas
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.ColorFilter
import androidx.compose.ui.graphics.drawscope.rotate
import androidx.compose.ui.graphics.drawscope.translate
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.rememberVectorPainter
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme

/**
 * The sign-in wallpaper (founder, 23 Oct 2026: "a wallpaper that shows every
 * activity of our app"): a scattered doodle pattern of what VChat does —
 * chat, play, likes, camera, music, shopping, film, sending, voice, stickers
 * — each glyph tinted with one of the theme's accent colours at low strength,
 * so it reads as colourful but stays quiet behind a form, in light and dark.
 *
 * Drawn on ONE canvas from remembered painters, at fixed pseudo-random
 * positions (a seeded lattice, so it is the same on every launch and never
 * shifts on recomposition). Static: nothing animates, so reduced motion needs
 * nothing and typing in the form costs no redraw.
 */
@Composable
fun UsActivityWallpaper(modifier: Modifier = Modifier) {
    val glyphs: List<ImageVector> = WALLPAPER_GLYPHS
    val painters = glyphs.map { rememberVectorPainter(it) }
    val colours = UsTheme.extended
    val dark = MaterialTheme.colorScheme.background.luminance() < HALF
    val tints = remember(colours, dark) {
        val strength = if (dark) DARK_STRENGTH else LIGHT_STRENGTH
        listOf(
            colours.accent, colours.statusSuccess, colours.statusWarning,
            colours.liveRed, colours.statusInfo, colours.chatAccent,
        ).map { it.copy(alpha = strength) }
    }
    Canvas(modifier = modifier) {
        val cell = CELL.toPx()
        val glyph = GLYPH.toPx()
        val columns = (size.width / cell).toInt() + 2
        val rows = (size.height / cell).toInt() + 2
        for (row in 0 until rows) {
            for (column in 0 until columns) {
                val seed = wallpaperSeed(row, column)
                // Every other row is shifted half a cell, so the pattern
                // never reads as a grid; the jitter loosens it further.
                val stagger = if (row % 2 == 0) 0f else cell / 2f
                val x = column * cell + stagger + (seed % JITTER_STEPS - JITTER_STEPS / 2) * cell * JITTER
                val y = row * cell + ((seed / JITTER_STEPS) % JITTER_STEPS - JITTER_STEPS / 2) * cell * JITTER
                val painter = painters[seed % painters.size]
                val tint = tints[(seed / painters.size) % tints.size]
                val angle = ((seed % ROTATION_STEPS) - ROTATION_STEPS / 2) * ROTATION_DEGREES
                translate(left = x - glyph / 2f, top = y - glyph / 2f) {
                    rotate(degrees = angle, pivot = Offset(glyph / 2f, glyph / 2f)) {
                        with(painter) {
                            draw(size = Size(glyph, glyph), colorFilter = ColorFilter.tint(tint))
                        }
                    }
                }
            }
        }
    }
}

/** What the app does, one glyph each; the order mixes them across the lattice. */
private val WALLPAPER_GLYPHS: List<ImageVector> = listOf(
    UsIcons.Comment, UsIcons.Play, UsIcons.HeartOutline, UsIcons.Camera,
    UsIcons.Music, UsIcons.ShoppingBag, UsIcons.Film, UsIcons.Send,
    UsIcons.Mic, UsIcons.Smile, UsIcons.Star, UsIcons.Sparkles,
)

/** A small, stable hash of the cell: the same cell gets the same glyph, tint, angle and jitter every time. */
internal fun wallpaperSeed(row: Int, column: Int): Int {
    var h = row * 73_856_093 xor column * 19_349_663
    h = h xor (h ushr 13)
    h *= 1_540_483_477 // MurmurHash2 mix constant
    h = h xor (h ushr 15)
    return h and Int.MAX_VALUE
}

private val CELL = 64.dp
private val GLYPH = 26.dp
private const val JITTER = 0.08f
private const val JITTER_STEPS = 5
private const val ROTATION_STEPS = 7
private const val ROTATION_DEGREES = 6f
private const val LIGHT_STRENGTH = 0.16f
private const val DARK_STRENGTH = 0.22f
private const val HALF = 0.5f

@Preview(name = "Activity wallpaper — dark", widthDp = 360, heightDp = 640)
@Composable
private fun UsActivityWallpaperDarkPreview() {
    UsTheme(darkTheme = true) {
        UsActivityWallpaper(Modifier)
    }
}
