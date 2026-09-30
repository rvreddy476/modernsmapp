package com.us.android.core.media

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The sound on its way from "Use this sound" to the reel create flow.
 *
 * What this protects: the sound being there when the form starts, and NOT
 * being there the next time the form is opened from the "+" — a reel made
 * tomorrow must not quietly carry a sound chosen today.
 */
class SoundEntryTest {

    private val entry = SoundEntry()
    private val sound = ChosenSound(id = "s1", title = "Original sound - Asha", artist = "Asha", durationMs = 28_400L)

    @Test
    fun `nothing is waiting until a sound is chosen`() {
        assertThat(entry.chosen.value).isNull()
        assertThat(entry.take()).isNull()
    }

    @Test
    fun `a chosen sound is handed over once, and the next visit finds none`() {
        entry.choose(sound)
        assertThat(entry.chosen.value).isEqualTo(sound)

        assertThat(entry.take()).isEqualTo(sound)

        assertThat(entry.chosen.value).isNull()
        assertThat(entry.take()).isNull()
    }

    @Test
    fun `the last sound chosen is the one that waits`() {
        entry.choose(sound)
        entry.choose(sound.copy(id = "s2"))

        assertThat(entry.take()?.id).isEqualTo("s2")
    }

    @Test
    fun `a sound without an id is no sound`() {
        entry.choose(sound)
        entry.choose(sound.copy(id = "  "))

        assertThat(entry.take()).isNull()
    }

    @Test
    fun `clearing lets the sound go`() {
        entry.choose(sound)

        entry.clear()

        assertThat(entry.take()).isNull()
    }
}
