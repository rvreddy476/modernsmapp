package com.us.android.feature.doorsteppro

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.doorsteppro.domain.AreaRules
import com.us.android.feature.doorsteppro.domain.HoursRules
import com.us.android.feature.doorsteppro.domain.HoursWindow
import com.us.android.feature.doorsteppro.domain.KycFormats
import org.junit.Test

/** The weekly-hours editor mirrors prokyc.ValidateHours; the area radius is 1–15 km. */
class ScheduleRulesTest {

    private fun w(day: Int, from: String, to: String) = HoursWindow(day, HoursRules.minutes(from)!!, HoursRules.minutes(to)!!)

    @Test
    fun `several windows a day are fine, touching included`() {
        assertThat(HoursRules.validate(listOf(w(1, "09:00", "13:00"), w(1, "13:00", "18:00"), w(2, "09:00", "18:00")))).isNull()
        assertThat(HoursRules.validate(emptyList())).isNull()
    }

    @Test
    fun `overlaps, backwards windows and a seventh window are refused, naming the window`() {
        val overlap = HoursRules.validate(listOf(w(1, "09:00", "13:00"), w(1, "12:30", "18:00")))
        assertThat(overlap?.index).isEqualTo(1)
        assertThat(overlap?.message).isEqualTo("Windows on one day may not overlap")

        assertThat(HoursRules.validate(listOf(w(3, "18:00", "09:00")))?.message).isEqualTo("End must be after start on the same day")
        assertThat(HoursRules.validate(listOf(w(3, "09:00", "09:00")))?.message).isEqualTo("End must be after start on the same day")
        assertThat(HoursRules.validate(listOf(HoursWindow(7, 0, 60)))?.message).isEqualTo("Pick a day of the week")

        val seven = (0 until 7).map { HoursWindow(4, it * 60, it * 60 + 30) }
        assertThat(HoursRules.validate(seven)?.index).isEqualTo(6)
        assertThat(HoursRules.newWindow(seven.take(6), 4)).isNull()
    }

    @Test
    fun `times are HH colon MM in India time, 24 00 refused`() {
        assertThat(HoursRules.minutes("09:30")).isEqualTo(570)
        assertThat(HoursRules.minutes("24:00")).isNull()
        assertThat(HoursRules.minutes("9:30")).isNull()
        assertThat(HoursRules.hhmm(570)).isEqualTo("09:30")
        assertThat(HoursRules.label(0)).isEqualTo("12:00 AM")
        assertThat(HoursRules.label(12 * 60)).isEqualTo("12:00 PM")
        assertThat(HoursRules.label(13 * 60 + 30)).isEqualTo("1:30 PM")
        assertThat(HoursRules.newWindow(emptyList(), 0)).isEqualTo(HoursWindow(0, 9 * 60, 18 * 60))
        assertThat(HoursRules.newWindow(listOf(w(0, "09:00", "13:00")), 0)).isEqualTo(HoursWindow(0, 13 * 60, 17 * 60))
        assertThat(HoursRules.sorted(listOf(w(2, "10:00", "11:00"), w(1, "12:00", "13:00"), w(1, "08:00", "09:00"))).map { it.weekday to it.startMinutes })
            .containsExactly(1 to 480, 1 to 720, 2 to 600).inOrder()
    }

    @Test
    fun `the radius is clamped to 1 to 15 km`() {
        assertThat(AreaRules.radiusMeters(0)).isEqualTo(1_000)
        assertThat(AreaRules.radiusMeters(8)).isEqualTo(8_000)
        assertThat(AreaRules.radiusMeters(30)).isEqualTo(15_000)
        assertThat(AreaRules.kmOf(8_000)).isEqualTo(8)
        assertThat(AreaRules.kmOf(30_000)).isEqualTo(15)
    }

    @Test
    fun `bank and PAN formats mirror shared kyc`() {
        assertThat(KycFormats.ifsc(" sbin0001234 ")).isEqualTo("SBIN0001234")
        assertThat(KycFormats.ifsc("SBIN1001234")).isNull()
        assertThat(KycFormats.accountNumber("123456789")).isEqualTo("123456789")
        assertThat(KycFormats.accountNumber("12345678")).isNull()
        assertThat(KycFormats.accountNumber("1234567890123456789")).isNull()
        assertThat(KycFormats.pan("abcpe1234f")).isEqualTo("ABCPE1234F")
        // A company PAN (fourth letter C) is not an individual's.
        assertThat(KycFormats.pan("ABCCE1234F")).isNull()
    }
}
