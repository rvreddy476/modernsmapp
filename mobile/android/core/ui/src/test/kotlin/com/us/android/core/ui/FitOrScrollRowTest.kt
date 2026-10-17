package com.us.android.core.ui

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * [fitWidths] (2026-10-17): tabs fit in equal columns when they can, share
 * the spare when only their total fits, and keep their own widths — the row
 * then scrolls — when they do not fit at all. Never narrower than a label.
 */
class FitOrScrollRowTest {

    @Test
    fun `short labels get equal columns that span the width`() {
        assertThat(fitWidths(listOf(50, 60, 70, 40), available = 400)).containsExactly(100, 100, 100, 100).inOrder()
    }

    @Test
    fun `a label wider than an equal column keeps its width and the others share the rest`() {
        val widths = fitWidths(listOf(40, 40, 120, 40), available = 300)

        assertThat(widths.sum()).isEqualTo(300)
        assertThat(widths[2]).isAtLeast(120)
        widths.zip(listOf(40, 40, 120, 40)).forEach { (w, natural) -> assertThat(w).isAtLeast(natural) }
    }

    /** The Messages tabs on a narrow phone: too wide in total, so nothing is squeezed and the row scrolls. */
    @Test
    fun `labels that do not fit keep their natural widths`() {
        val natural = listOf(70, 75, 120, 118)

        assertThat(fitWidths(natural, available = 340)).isEqualTo(natural)
    }

    @Test
    fun `the spare is shared to the pixel and an empty row is empty`() {
        assertThat(fitWidths(listOf(10, 10, 10), available = 32)).containsExactly(11, 11, 10).inOrder()
        assertThat(fitWidths(emptyList(), available = 100)).isEmpty()
    }
}
