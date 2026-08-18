package com.us.android.core.common.time

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import java.time.Instant

class RelativeTimeTest {

    private val now: Instant = Instant.parse("2026-08-18T12:00:00Z")

    private fun ago(text: String) = formatRelativeTime(text, now)

    @Test
    fun `seconds old reads as now`() {
        assertThat(ago("2026-08-18T11:59:31Z")).isEqualTo("now")
    }

    @Test
    fun `minutes, hours and days are abbreviated`() {
        assertThat(ago("2026-08-18T11:55:00Z")).isEqualTo("5m")
        assertThat(ago("2026-08-18T09:00:00Z")).isEqualTo("3h")
        assertThat(ago("2026-08-16T12:00:00Z")).isEqualTo("2d")
    }

    @Test
    fun `past a week it becomes a date`() {
        assertThat(ago("2026-08-01T12:00:00Z")).isEqualTo("1 Aug")
    }

    @Test
    fun `a different year keeps the year`() {
        assertThat(ago("2025-08-01T12:00:00Z")).isEqualTo("1 Aug 2025")
    }

    /**
     * Device and server clocks are not synchronised. A post that appears to be
     * from the near future must not render as a negative age.
     */
    @Test
    fun `a future timestamp reads as now, never negative`() {
        assertThat(ago("2026-08-18T12:00:30Z")).isEqualTo("now")
        assertThat(ago("2026-08-19T12:00:00Z")).isEqualTo("now")
    }

    /**
     * This runs inside a Composable. Throwing on a malformed string would take
     * down the whole screen over a cosmetic value.
     */
    @Test
    fun `unparseable input yields an empty string, not a crash`() {
        assertThat(ago("")).isEmpty()
        assertThat(ago("not a date")).isEmpty()
        assertThat(ago("2026-08-18")).isEmpty()
    }

    /** The exact format the gateway sends, microseconds and all. */
    @Test
    fun `the real gateway format parses`() {
        assertThat(ago("2026-08-17T10:16:51.169391Z")).isEqualTo("1d")
    }
}
