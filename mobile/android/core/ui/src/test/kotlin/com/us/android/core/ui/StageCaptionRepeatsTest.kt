package com.us.android.core.ui

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** The caption says each thing once (founder, 2026-10-19: "so much duplicate description"). */
class StageCaptionRepeatsTest {
    private fun caption(title: String? = null, text: String = "", hashtags: List<String> = emptyList()) =
        StageCaptionState(postId = "p1", handle = "@asha", title = title, text = text, hashtags = hashtags)

    @Test
    fun `a title the caption already opens with is not drawn again`() {
        assertThat(caption(title = "Test copy", text = "test copy and more").withoutRepeats().title).isNull()
        assertThat(caption(title = "Sunset", text = "A walk by the sea").withoutRepeats().title).isEqualTo("Sunset")
    }

    @Test
    fun `a tag the caption already carries is not listed under it`() {
        val shown = caption(text = "Evening run #Fitness #दौड़", hashtags = listOf("fitness", "#दौड़", "beach")).withoutRepeats()
        assertThat(shown.hashtags).containsExactly("beach")
    }

    @Test
    fun `nothing else changes`() {
        val state = caption(title = "Sunset", text = "A walk", hashtags = listOf("sea"))
        assertThat(state.withoutRepeats()).isEqualTo(state)
    }
}
