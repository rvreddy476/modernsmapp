package com.us.android.core.media.publish

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import java.time.Instant
import java.time.ZoneId

/** The schedule window's edges, the wire form, and the button's wording. */
class ScheduleWindowTest {

    private val now: Instant = Instant.parse("2026-09-05T12:00:00Z")

    @Test
    fun `five minutes to thirty days ahead is the window, inclusive at both ends`() {
        assertThat(ScheduleWindow.check(now.plusSeconds(5 * 60), now)).isEqualTo(ScheduleWindow.Check.Ok)
        assertThat(ScheduleWindow.check(now.plusSeconds(5 * 60 - 1), now)).isEqualTo(ScheduleWindow.Check.TooSoon)
        assertThat(ScheduleWindow.check(now, now)).isEqualTo(ScheduleWindow.Check.TooSoon)
        assertThat(ScheduleWindow.check(now.minusSeconds(60), now)).isEqualTo(ScheduleWindow.Check.TooSoon)
        assertThat(ScheduleWindow.check(now.plusSeconds(30L * 24 * 3600), now)).isEqualTo(ScheduleWindow.Check.Ok)
        assertThat(ScheduleWindow.check(now.plusSeconds(30L * 24 * 3600 + 1), now))
            .isEqualTo(ScheduleWindow.Check.TooLate)
    }

    @Test
    fun `a refused instant has a sentence and an accepted one has none`() {
        assertThat(ScheduleWindow.message(ScheduleWindow.Check.Ok)).isNull()
        assertThat(ScheduleWindow.message(ScheduleWindow.Check.TooSoon)).contains("5 minutes")
        assertThat(ScheduleWindow.message(ScheduleWindow.Check.TooLate)).contains("30 days")
    }

    @Test
    fun `the wire form is RFC 3339 in UTC to the second and parses back`() {
        val at = Instant.parse("2026-09-06T13:00:00.250Z")

        assertThat(ScheduleWindow.wire(at)).isEqualTo("2026-09-06T13:00:00Z")
        assertThat(ScheduleWindow.parse("2026-09-06T13:00:00Z")).isEqualTo(Instant.parse("2026-09-06T13:00:00Z"))
        assertThat(ScheduleWindow.parse("not a time")).isNull()
        assertThat(ScheduleWindow.parse(null)).isNull()
    }

    @Test
    fun `the button says the day, the date and the time in the viewer's zone`() {
        val at = Instant.parse("2026-09-06T13:00:00Z")

        assertThat(ScheduleWindow.label(at, ZoneId.of("Asia/Kolkata"))).isEqualTo("Sun 6 Sep, 18:30")
        assertThat(ScheduleWindow.label(at, ZoneId.of("UTC"))).isEqualTo("Sun 6 Sep, 13:00")
        assertThat(ScheduleWindow.shortLabel(at, ZoneId.of("Asia/Kolkata"))).isEqualTo("6 Sep 18:30")
    }
}
