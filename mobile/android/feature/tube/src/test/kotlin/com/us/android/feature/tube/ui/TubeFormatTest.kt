package com.us.android.feature.tube.ui

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import java.time.Instant

/** The duration badge and the meta line, as a table. */
class TubeFormatTest {

    @Test
    fun `under an hour reads m colon ss`() {
        assertThat(formatDuration(0L)).isEqualTo("0:00")
        assertThat(formatDuration(9_000L)).isEqualTo("0:09")
        assertThat(formatDuration(65_000L)).isEqualTo("1:05")
        assertThat(formatDuration(754_321L)).isEqualTo("12:34")
        assertThat(formatDuration(3_599_000L)).isEqualTo("59:59")
    }

    @Test
    fun `from an hour on reads h colon mm colon ss`() {
        assertThat(formatDuration(3_600_000L)).isEqualTo("1:00:00")
        assertThat(formatDuration(3_661_000L)).isEqualTo("1:01:01")
        assertThat(formatDuration(10L * 3_600_000L + 5L * 60_000L)).isEqualTo("10:05:00")
    }

    @Test
    fun `rounds to the nearest second and never goes negative`() {
        assertThat(formatDuration(59_600L)).isEqualTo("1:00")
        assertThat(formatDuration(59_400L)).isEqualTo("0:59")
        assertThat(formatDuration(-5_000L)).isEqualTo("0:00")
    }

    @Test
    fun `the meta line reads author, views, age and joins only what exists`() {
        val now = Instant.parse("2026-09-05T12:00:00Z")
        val threeHoursAgo = "2026-09-05T09:00:00Z"
        assertThat(videoMetaLine("Ada", threeHoursAgo, 1_200, now)).isEqualTo("Ada · 1.2K views · 3h")
        assertThat(videoMetaLine(null, "", 0, now)).isEmpty()
        assertThat(videoMetaLine("Ada", "not-a-date", 0, now)).isEqualTo("Ada")
        assertThat(videoMetaLine("", "not-a-date", 7, now)).isEqualTo("7 views")
        assertThat(videoMetaLine(null, threeHoursAgo, 0, now)).isEqualTo("3h")
    }

    @Test
    fun `a view count is compact with its noun, and absent rather than zero`() {
        assertThat(viewsLabel(1_200)).isEqualTo("1.2K views")
        assertThat(viewsLabel(1_500_000)).isEqualTo("1.5M views")
        assertThat(viewsLabel(7)).isEqualTo("7 views")
        assertThat(viewsLabel(0)).isNull()
        assertThat(viewsLabel(-3)).isNull()
    }
}
