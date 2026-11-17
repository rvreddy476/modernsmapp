package com.us.android.feature.tube.ui.watch

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The 90% rule, both faces: where a video resumes, and when it counts as
 * watched. 90% is post-service's `watchProgressState` and the web's (lv-46);
 * Android used 95% until 2026-11-17.
 */
class ResumeRuleTest {

    private val duration = 100_000L

    @Test
    fun `resumes where the viewer left off`() {
        assertThat(resumePositionMs(30_000L, duration)).isEqualTo(30_000L)
        assertThat(resumePositionMs(89_999L, duration)).isEqualTo(89_999L)
    }

    @Test
    fun `a video all but finished starts again from the top`() {
        assertThat(resumePositionMs(90_000L, duration)).isEqualTo(0L)
        assertThat(resumePositionMs(92_000L, duration)).isEqualTo(0L)
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
    fun `completed at ninety percent exactly, the server's threshold`() {
        assertThat(isCompleted(89_999L, duration)).isFalse()
        assertThat(isCompleted(90_000L, duration)).isTrue()
        assertThat(isCompleted(92_000L, duration)).isTrue()
        assertThat(isCompleted(duration, duration)).isTrue()
        assertThat(COMPLETION_FRACTION).isEqualTo(0.90)
    }
}
