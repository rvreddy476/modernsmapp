package com.us.android.feature.post.createhub.studio

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** The trim and speed rules as a table: what is kept, what is exported, and the reel cap. */
class ReelEditTest {

    private val tenSeconds = 10_000_000L
    private fun edit(durationUs: Long = tenSeconds, width: Int = 1080, height: Int = 1920) =
        ReelEdit(sourceUri = "content://v/1", width = width, height = height, durationUs = durationUs)

    @Test
    fun `a fresh edit keeps the whole source at normal speed with no look and no text`() {
        val fresh = edit()
        assertThat(fresh.trimStartUs).isEqualTo(0L)
        assertThat(fresh.trimEndUs).isEqualTo(tenSeconds)
        assertThat(fresh.trimmedUs).isEqualTo(tenSeconds)
        assertThat(fresh.exportedUs).isEqualTo(tenSeconds)
        assertThat(fresh.speed).isEqualTo(ReelSpeed.NORMAL)
        assertThat(fresh.look).isEqualTo(ReelLook.NONE)
        assertThat(fresh.text).isNull()
        assertThat(fresh.mode).isEqualTo(FrameMode.FILL)
        assertThat(fresh.isUntouched).isTrue()
    }

    @Test
    fun `the exported length is the kept span divided by the speed`() {
        val trimmed = edit().withTrimStart(2_000_000L).withTrimEnd(8_000_000L)
        assertThat(trimmed.trimmedUs).isEqualTo(6_000_000L)
        assertThat(trimmed.copy(speed = ReelSpeed.DOUBLE).exportedUs).isEqualTo(3_000_000L)
        assertThat(trimmed.copy(speed = ReelSpeed.HALF).exportedUs).isEqualTo(12_000_000L)
        assertThat(trimmed.copy(speed = ReelSpeed.FASTER).exportedUs).isEqualTo(4_000_000L)
        assertThat(trimmed.isUntouched).isFalse()
    }

    @Test
    fun `the five minute cap applies to the export, so speed can bring a long clip under it`() {
        val tenMinutes = edit(durationUs = 600_000_000L)
        assertThat(tenMinutes.exceedsReelCap).isTrue()
        assertThat(tenMinutes.copy(speed = ReelSpeed.DOUBLE).exceedsReelCap).isFalse()
        assertThat(tenMinutes.withTrimEnd(300_000_000L).exceedsReelCap).isFalse()
        assertThat(tenMinutes.withTrimEnd(300_000_001L).exceedsReelCap).isTrue()
        assertThat(edit(durationUs = 200_000_000L).copy(speed = ReelSpeed.HALF).exceedsReelCap).isTrue()
    }

    @Test
    fun `the handles are clamped to the source and hold a second between them`() {
        val e = edit()
        assertThat(e.withTrimStart(-5L).trimStartUs).isEqualTo(0L)
        assertThat(e.withTrimEnd(99_000_000L).trimEndUs).isEqualTo(tenSeconds)
        // Start dragged past the end pushes the end along, never crosses it.
        val pushed = e.withTrimEnd(4_000_000L).withTrimStart(9_500_000L)
        assertThat(pushed.trimStartUs).isEqualTo(9_000_000L)
        assertThat(pushed.trimEndUs).isEqualTo(tenSeconds)
        // End dragged under the start pushes the start back.
        val pulled = e.withTrimStart(6_000_000L).withTrimEnd(500_000L)
        assertThat(pulled.trimEndUs).isEqualTo(1_000_000L)
        assertThat(pulled.trimStartUs).isEqualTo(0L)
        assertThat(pulled.trimmedUs).isEqualTo(ReelEdit.MIN_TRIM_US)
    }

    @Test
    fun `a source shorter than a second is kept whole`() {
        val short = edit(durationUs = 400_000L)
        assertThat(short.withTrimStart(300_000L).trimStartUs).isEqualTo(0L)
        assertThat(short.withTrimEnd(100_000L).trimEndUs).isEqualTo(400_000L)
    }

    @Test
    fun `changing the mode resets the pan, and only Fill pans`() {
        val wide = edit(width = 1920, height = 1080)
        assertThat(wide.mode).isEqualTo(FrameMode.FIT)
        assertThat(wide.panned(100f, 400f).pan).isEqualTo(0f)

        val filled = wide.withMode(FrameMode.FILL).panned(100f, 400f)
        assertThat(filled.pan).isLessThan(0f)
        assertThat(filled.withMode(FrameMode.FIT).pan).isEqualTo(0f)
        assertThat(filled.withMode(FrameMode.FILL)).isEqualTo(filled)
    }
}
