package com.us.android.feature.doorsteppro

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.doorsteppro.data.PhotosRequiredDto
import com.us.android.feature.doorsteppro.data.ProError
import com.us.android.feature.doorsteppro.domain.DeclineReason
import com.us.android.feature.doorsteppro.domain.JobsForDate
import com.us.android.feature.doorsteppro.domain.OfferCountdown
import com.us.android.feature.doorsteppro.domain.OfferWindow
import com.us.android.feature.doorsteppro.domain.OtpEntry
import com.us.android.feature.doorsteppro.domain.OtpKind
import com.us.android.feature.doorsteppro.domain.OtpRules
import com.us.android.feature.doorsteppro.domain.PhotoCounts
import com.us.android.feature.doorsteppro.domain.PhotoGate
import com.us.android.feature.doorsteppro.domain.PhotoPhase
import com.us.android.feature.doorsteppro.domain.VisitFlow
import com.us.android.feature.doorsteppro.domain.VisitStep
import com.us.android.feature.doorsteppro.ui.countdownText
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import org.junit.Test
import java.time.Instant
import java.time.LocalDate

class VisitRulesTest {

    // ── Offer countdown ──

    @Test
    fun `the countdown rounds up, turns urgent in the last minute and refuses at the expiry`() {
        val countdown = OfferCountdown.of("2026-10-04T10:10:00Z")
        val expiry = Instant.parse("2026-10-04T10:10:00Z")
        assertThat(countdown.at(expiry.minusSeconds(600))).isEqualTo(OfferWindow.Open(600, urgent = false))
        assertThat(countdown.at(expiry.minusMillis(60_500))).isEqualTo(OfferWindow.Open(61, urgent = false))
        assertThat(countdown.at(expiry.minusSeconds(60))).isEqualTo(OfferWindow.Open(60, urgent = true))
        assertThat(countdown.at(expiry.minusMillis(1))).isEqualTo(OfferWindow.Open(1, urgent = true))
        assertThat(countdown.at(expiry)).isEqualTo(OfferWindow.Expired)
        assertThat(countdown.canRespond(expiry)).isFalse()
        assertThat(countdown.canRespond(expiry.minusSeconds(5))).isTrue()
        assertThat(countdownText(600)).isEqualTo("10:00")
        assertThat(countdownText(61)).isEqualTo("1:01")
        assertThat(countdownText(7_205)).isEqualTo("2:00:05")
    }

    @Test
    fun `an unreadable expiry leaves the server to decide`() {
        assertThat(OfferCountdown.of(null).at(Instant.EPOCH)).isEqualTo(OfferWindow.Unknown)
        assertThat(OfferCountdown.of("tomorrow").canRespond(Instant.EPOCH)).isTrue()
        assertThat(DeclineReason.entries.map { it.wire }).containsExactly("too_far", "busy", "not_my_skill", "other").inOrder()
    }

    // ── OTP entry and lockout ──

    private fun refused(status: Int, code: String, vararg details: Pair<String, Any>) = ProError.Refused(
        status = status,
        code = code,
        message = "server text",
        details = JsonObject(details.associate { (k, v) -> k to if (v is Int) JsonPrimitive(v) else JsonPrimitive(v.toString()) }),
    )

    @Test
    fun `only four digits are taken, and a wrong code says how many attempts are left`() {
        val now = Instant.parse("2026-10-04T10:00:00Z")
        var entry = OtpRules.onTyped(OtpEntry(OtpKind.START), "12a3-45")
        assertThat(entry.code).isEqualTo("1234")
        assertThat(OtpRules.canSubmit(entry, now)).isTrue()
        assertThat(OtpRules.message(entry, now)).isNull()

        entry = OtpRules.onRefused(entry, refused(422, "DOORSTEP_OTP_INVALID", "attempts_left" to 2))!!
        assertThat(entry.code).isEmpty()
        assertThat(OtpRules.message(entry, now)).isEqualTo("That start code is wrong. 2 attempts left.")
        assertThat(OtpRules.message(entry.copy(attemptsLeft = 1), now)).isEqualTo("That start code is wrong. 1 attempt left before entry locks.")
        assertThat(OtpRules.message(entry.copy(attemptsLeft = 0), now)).isEqualTo("That start code is wrong. The next wrong code locks entry.")
        assertThat(OtpRules.message(entry.copy(attemptsLeft = null), now)).isEqualTo("That start code is wrong. Ask the customer to read it again.")
        // Typing again clears the "wrong" line.
        assertThat(OtpRules.message(OtpRules.onTyped(entry, "9"), now)).isNull()
    }

    @Test
    fun `a lockout shuts entry until the server's time and says when, in India time`() {
        val now = Instant.parse("2026-10-04T10:00:00Z")
        val locked = OtpRules.onRefused(
            OtpEntry(OtpKind.END, code = "1111"),
            refused(423, "DOORSTEP_OTP_LOCKED", "locked_until" to "2026-10-04T10:14:30Z"),
        )!!
        assertThat(OtpRules.isLocked(locked, now)).isTrue()
        assertThat(OtpRules.canSubmit(locked.copy(code = "1234"), now)).isFalse()
        // 10:14:30 UTC is 3:44 PM IST; 14.5 minutes rounds up to 15.
        assertThat(OtpRules.message(locked, now)).isEqualTo("Too many wrong codes. Try again at 3:44 PM (in 15 min).")
        val later = Instant.parse("2026-10-04T10:15:00Z")
        assertThat(OtpRules.isLocked(locked, later)).isFalse()
        assertThat(OtpRules.canSubmit(locked.copy(code = "1234"), later)).isTrue()
    }

    @Test
    fun `a lockout without a readable time stays shut, and other refusals are not about the code`() {
        val locked = OtpRules.onRefused(OtpEntry(OtpKind.START), refused(423, "DOORSTEP_OTP_LOCKED"))!!
        assertThat(locked.lockedIndefinitely).isTrue()
        assertThat(OtpRules.isLocked(locked, Instant.MAX)).isTrue()
        assertThat(OtpRules.message(locked, Instant.EPOCH)).startsWith("Too many wrong codes. Entry is locked")
        assertThat(OtpRules.onRefused(OtpEntry(OtpKind.START), refused(422, "DOORSTEP_PHOTOS_REQUIRED"))).isNull()
        assertThat(OtpRules.onRefused(OtpEntry(OtpKind.START), ProError.Network(null))).isNull()
    }

    // ── Photo gate ──

    private val salon = PhotosRequiredDto(before = 2, after = 2, kitSeal = 1)

    @Test
    fun `start needs the minimum before photos and the sealed kit, complete needs the after photos`() {
        var counts = PhotoCounts()
        assertThat(PhotoGate.missingToStart(salon, counts)).containsExactly(PhotoPhase.BEFORE, 2, PhotoPhase.KIT_SEAL, 1)
        assertThat(PhotoGate.missingText(PhotoGate.missingToStart(salon, counts))).isEqualTo("Take 2 more before photos and 1 sealed kit photo.")

        counts = counts.plus(PhotoPhase.BEFORE).plus(PhotoPhase.BEFORE)
        assertThat(PhotoGate.canStart(salon, counts)).isFalse()
        counts = counts.plus(PhotoPhase.KIT_SEAL)
        assertThat(PhotoGate.canStart(salon, counts)).isTrue()
        assertThat(PhotoGate.missingText(PhotoGate.missingToStart(salon, counts))).isNull()

        // Before photos never count towards completing.
        assertThat(PhotoGate.canComplete(salon, counts)).isFalse()
        assertThat(PhotoGate.missingText(PhotoGate.missingToComplete(salon, counts.plus(PhotoPhase.AFTER)))).isEqualTo("Take 1 more after photo.")
        counts = counts.plus(PhotoPhase.AFTER).plus(PhotoPhase.AFTER)
        assertThat(PhotoGate.canComplete(salon, counts)).isTrue()
        // A job needing no photos is open from the start.
        assertThat(PhotoGate.canStart(PhotosRequiredDto(0, 0, 0), PhotoCounts())).isTrue()
    }

    @Test
    fun `the server's count wins after a photos-required refusal`() {
        val local = PhotoCounts().plus(PhotoPhase.BEFORE).plus(PhotoPhase.BEFORE)
        val reconciled = PhotoGate.reconcile(local, refused(422, "DOORSTEP_PHOTOS_REQUIRED", "phase" to "before", "required" to 2, "uploaded" to 1))
        assertThat(reconciled[PhotoPhase.BEFORE]).isEqualTo(1)
        assertThat(PhotoGate.canStart(PhotosRequiredDto(2, 2, 0), reconciled)).isFalse()
        // Anything else changes nothing.
        assertThat(PhotoGate.reconcile(local, refused(422, "DOORSTEP_OTP_INVALID", "uploaded" to 0))).isEqualTo(local)
    }

    // ── The visit flow ──

    @Test
    fun `each status has one next step, and the off-ramps open only where the contract allows them`() {
        val now = Instant.parse("2026-10-04T05:00:00Z")
        fun step(status: String, finished: Boolean = false) = VisitFlow.of(Jobs.job(status = status), now, finished = finished).step
        assertThat(step("assigned")).isEqualTo(VisitStep.GO_EN_ROUTE)
        assertThat(step("en_route")).isEqualTo(VisitStep.MARK_ARRIVED)
        assertThat(step("arrived")).isEqualTo(VisitStep.START_WITH_OTP)
        assertThat(step("in_progress")).isEqualTo(VisitStep.WORK)
        assertThat(step("in_progress", finished = true)).isEqualTo(VisitStep.COMPLETE_WITH_OTP)
        assertThat(step("awaiting_extras_payment")).isEqualTo(VisitStep.COMPLETE_WITH_OTP)
        assertThat(step("completed")).isEqualTo(VisitStep.RATE)
        assertThat(step("customer_no_show")).isEqualTo(VisitStep.RATE)
        assertThat(step("cancelled")).isEqualTo(VisitStep.NONE)
        assertThat(step("something_new")).isEqualTo(VisitStep.NONE)

        val assigned = VisitFlow.of(Jobs.job(status = "assigned"), now)
        assertThat(assigned.canCancel).isTrue()
        assertThat(assigned.canUnsafeExit).isFalse()
        assertThat(assigned.canNavigate).isTrue()
        val working = VisitFlow.of(Jobs.job(status = "in_progress"), now)
        assertThat(working.canCancel).isFalse() // no giving back after the start code
        assertThat(working.canUnsafeExit).isTrue()
        assertThat(working.canProposeExtras).isTrue()
        assertThat(working.canSos).isTrue()
        assertThat(VisitFlow.of(Jobs.job(status = "in_progress"), now, finished = true).canProposeExtras).isFalse()
        assertThat(VisitFlow.of(Jobs.job(status = "completed"), now).canSos).isFalse()
    }

    @Test
    fun `no-show opens fifteen minutes after the later of arrival and the slot start`() {
        val job = Jobs.job(status = "arrived", slotStart = "2026-10-04T05:00:00Z")
        val arrivedEarly = Instant.parse("2026-10-04T04:50:00Z")
        val waiting = VisitFlow.of(job, Instant.parse("2026-10-04T05:10:00Z"), arrivedAt = arrivedEarly)
        assertThat(waiting.noShowAvailable).isFalse()
        assertThat(waiting.noShowInSeconds).isEqualTo(300)
        assertThat(VisitFlow.of(job, Instant.parse("2026-10-04T05:15:00Z"), arrivedAt = arrivedEarly).noShowAvailable).isTrue()

        val arrivedLate = Instant.parse("2026-10-04T05:20:00Z")
        assertThat(VisitFlow.of(job, Instant.parse("2026-10-04T05:30:00Z"), arrivedAt = arrivedLate).noShowAvailable).isFalse()
        assertThat(VisitFlow.of(job, Instant.parse("2026-10-04T05:35:00Z"), arrivedAt = arrivedLate).noShowAvailable).isTrue()
        // Only while arrived.
        assertThat(VisitFlow.of(Jobs.job(status = "in_progress"), Instant.MAX.minusSeconds(1)).noShowAvailable).isFalse()
    }

    @Test
    fun `jobs for a date group by the India-time day of the slot`() {
        val lateNight = Jobs.job(bookingId = "b1", slotStart = "2026-10-04T19:00:00Z") // 00:30 IST on the 5th
        val morning = Jobs.job(bookingId = "b2", slotStart = "2026-10-04T03:30:00Z") // 09:00 IST on the 4th
        val noon = Jobs.job(bookingId = "b3", slotStart = "2026-10-04T06:30:00Z")
        val jobs = listOf(noon, lateNight, morning, noon)
        assertThat(JobsForDate.on(jobs, LocalDate.parse("2026-10-04")).map { it.bookingId }).containsExactly("b2", "b3").inOrder()
        assertThat(JobsForDate.on(jobs, LocalDate.parse("2026-10-05")).map { it.bookingId }).containsExactly("b1")
        assertThat(JobsForDate.daysWithJobs(jobs)).containsExactly(LocalDate.parse("2026-10-04"), LocalDate.parse("2026-10-05"))
    }
}
