package com.us.android.core.designsystem.component

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The sign-in wallpaper's lattice (23 Oct 2026): the same cell always gets
 * the same glyph, tint and angle, so the pattern never shifts between
 * launches or recompositions, and the mix spreads across every glyph.
 */
class UsActivityWallpaperTest {

    @Test
    fun `a cell's seed is stable and never negative`() {
        for (row in 0 until 40) {
            for (column in 0 until 12) {
                val seed = wallpaperSeed(row, column)
                assertThat(seed).isAtLeast(0)
                assertThat(wallpaperSeed(row, column)).isEqualTo(seed)
            }
        }
    }

    @Test
    fun `a phone-sized wall uses every glyph and no glyph dominates`() {
        val glyphs = 12
        val counts = IntArray(glyphs)
        for (row in 0 until 16) {
            for (column in 0 until 8) {
                counts[wallpaperSeed(row, column) % glyphs]++
            }
        }
        assertThat(counts.all { it > 0 }).isTrue()
        assertThat(counts.max()).isAtMost(128 / glyphs * 3)
    }

    @Test
    fun `neighbouring cells differ`() {
        var same = 0
        for (row in 0 until 16) {
            for (column in 0 until 7) {
                if (wallpaperSeed(row, column) % 12 == wallpaperSeed(row, column + 1) % 12) same++
            }
        }
        assertThat(same).isLessThan(16 * 7 / 4)
    }
}
