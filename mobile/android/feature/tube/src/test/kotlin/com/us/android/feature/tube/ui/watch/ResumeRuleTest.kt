package com.us.android.feature.tube.ui.watch

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** The 95% rule, both faces: where a video resumes, and when it counts as watched. */
class ResumeRuleTest {

    private val duration = 100_000L

    @Test
    fun `resumes where the viewer left off`() {
        assertThat(resumePositionMs(30_000L, duration)).isEqualTo(30_000L)
        assertThat(resumePositionMs(94_999L, duration)).isEqualTo(94_999L)
    }

    @Test
    fun `a video all but finished starts again from the top`() {
        assertThat(resumePositionMs(95_000L, duration)).isEqualTo(0L)
        assertThat(resumePositionMs(100_000L, duration)).isEqualTo(0L)
        assertThat(resumePositionMs(120_000L, duration)).isEqualTo(0L)
    }

    @Test
    fun `nothing saved is the top`() {
        assertThat(resumePositionMs(0L, duration)).isEqualTo(0L)
        assertThat(resumePositionMs(-1L, duration)).isEqualTo(0L)
    }

    @Test
    fun `an unknown duration resumes as saved and is never completed`() {
        assertThat(resumePositionMs(30_000L, 0L)).isEqualTo(30_000L)
        assertThat(isCompleted(30_000L, 0L)).isFalse()
    }

    @Test
    fun `completed at ninety-five percent exactly`() {
        assertThat(isCompleted(94_999L, duration)).isFalse()
        assertThat(isCompleted(95_000L, duration)).isTrue()
        assertThat(isCompleted(duration, duration)).isTrue()
        assertThat(COMPLETION_FRACTION).isEqualTo(0.95)
    }
}
