package com.us.android.feature.chat.ui

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** Message ticks (2026-10-22): the "Read tick colour" choice and its stored key. */
class ReadTickColourTest {

    @Test
    fun `five distinct choices, default first`() {
        assertThat(ReadTickColour.entries).hasSize(5)
        assertThat(ReadTickColour.entries.first()).isEqualTo(ReadTickColour.DEFAULT)
        assertThat(ReadTickColour.entries.map { it.key }.toSet()).hasSize(5)
        assertThat(ReadTickColour.entries.map { it.label }.toSet()).hasSize(5)
    }

    @Test
    fun `a stored key round-trips and anything unknown is the default`() {
        ReadTickColour.entries.forEach { assertThat(ReadTickColour.fromKey(it.key)).isEqualTo(it) }
        assertThat(ReadTickColour.fromKey(" GREEN ")).isEqualTo(ReadTickColour.GREEN)
        assertThat(ReadTickColour.fromKey(null)).isEqualTo(ReadTickColour.DEFAULT)
        assertThat(ReadTickColour.fromKey("")).isEqualTo(ReadTickColour.DEFAULT)
        assertThat(ReadTickColour.fromKey("chartreuse")).isEqualTo(ReadTickColour.DEFAULT)
    }
}
