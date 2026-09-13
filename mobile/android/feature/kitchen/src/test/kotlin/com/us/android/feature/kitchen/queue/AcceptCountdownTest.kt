package com.us.android.feature.kitchen.queue

import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.network.KitchenOrderDto
import org.junit.Test
import java.time.Duration
import java.time.Instant

class AcceptCountdownTest {

    /** A clock that only moves when told to. */
    private class FakeClock(var current: Instant = Instant.parse("2026-09-13T06:30:00Z")) : KitchenClock {
        override fun now(): Instant = current
        fun advance(seconds: Long) {
            current = current.plusSeconds(seconds)
        }
        fun advanceMillis(millis: Long) {
            current = current.plus(Duration.ofMillis(millis))
        }
    }

    @Test
    fun `counts down in whole seconds, rounding up, and turns urgent at thirty`() {
        val clock = FakeClock()
        val countdown = AcceptCountdown(clock.now().plusSeconds(90), clock.now())

        assertThat(countdown.window).isEqualTo(AcceptWindow.Open(90, urgent = false))
        clock.advanceMillis(500)
        assertThat(countdown.tick(clock.now())).isEqualTo(AcceptWindow.Open(90, urgent = false))
        clock.advance(59)
        assertThat(countdown.tick(clock.now())).isEqualTo(AcceptWindow.Open(31, urgent = false))
        clock.advance(1)
        assertThat(countdown.tick(clock.now())).isEqualTo(AcceptWindow.Open(30, urgent = true))
    }

    @Test
    fun `expires exactly at the deadline and refuses accept after it`() {
        val clock = FakeClock()
        val countdown = AcceptCountdown(clock.now().plusSeconds(10), clock.now())

        clock.advanceMillis(9_999)
        assertThat(countdown.tick(clock.now())).isEqualTo(AcceptWindow.Open(1, urgent = true))
        assertThat(countdown.canRespond(clock.now())).isTrue()

        clock.advanceMillis(1)
        assertThat(countdown.tick(clock.now())).isEqualTo(AcceptWindow.Expired)
        assertThat(countdown.canRespond(clock.now())).isFalse()
        assertThat(countdown.confirmAccepted(clock.now())).isFalse()
        assertThat(countdown.window).isEqualTo(AcceptWindow.Expired)
    }

    @Test
    fun `an accepted order stays accepted after its deadline passes`() {
        val clock = FakeClock()
        val countdown = AcceptCountdown(clock.now().plusSeconds(10), clock.now())

        assertThat(countdown.confirmAccepted(clock.now())).isTrue()
        clock.advance(600)
        assertThat(countdown.tick(clock.now())).isEqualTo(AcceptWindow.Accepted)
        assertThat(countdown.reschedule(clock.now().plusSeconds(60), clock.now())).isEqualTo(AcceptWindow.Accepted)
        assertThat(countdown.confirmRejected(clock.now())).isFalse()
    }

    @Test
    fun `no deadline means the partner can always respond`() {
        val clock = FakeClock()
        val countdown = AcceptCountdown(null, clock.now())
        clock.advance(3_600)
        assertThat(countdown.tick(clock.now())).isEqualTo(AcceptWindow.NoDeadline)
        assertThat(countdown.confirmRejected(clock.now())).isTrue()
        assertThat(countdown.window).isEqualTo(AcceptWindow.Rejected)
    }

    @Test
    fun `a fresh server deadline reopens a locally expired order`() {
        val clock = FakeClock()
        val countdown = AcceptCountdown(clock.now().minusSeconds(1), clock.now())
        assertThat(countdown.window).isEqualTo(AcceptWindow.Expired)

        assertThat(countdown.reschedule(clock.now().plusSeconds(45), clock.now())).isEqualTo(AcceptWindow.Open(45, urgent = false))
    }

    @Test
    fun `the new-order alert rings only while an order can be answered`() {
        assertThat(NewOrderAlertPolicy.shouldRing(listOf(AcceptWindow.Expired, AcceptWindow.Accepted))).isFalse()
        assertThat(NewOrderAlertPolicy.shouldRing(listOf(AcceptWindow.Expired, AcceptWindow.Open(5, true)))).isTrue()
        assertThat(NewOrderAlertPolicy.shouldRing(listOf(AcceptWindow.NoDeadline))).isTrue()
        assertThat(NewOrderAlertPolicy.shouldRing(emptyList())).isFalse()
    }

    @Test
    fun `the deadline prefers seconds_to_breach over the server's timestamp text`() {
        val fetchedAt = Instant.parse("2026-09-13T06:30:00Z")
        val order = KitchenOrderDto(id = "o", secondsToBreach = 120, acceptDeadlineAt = "2020-01-01 00:00:00+00")
        assertThat(KitchenDeadline.of(order, fetchedAt)).isEqualTo(fetchedAt.plusSeconds(120))
    }

    @Test
    fun `server timestamps parse from RFC 3339 and Postgres text`() {
        val want = Instant.parse("2026-09-13T06:35:00Z")
        assertThat(KitchenDeadline.parseServerInstant("2026-09-13T06:35:00Z")).isEqualTo(want)
        assertThat(KitchenDeadline.parseServerInstant("2026-09-13 06:35:00+00")).isEqualTo(want)
        assertThat(KitchenDeadline.parseServerInstant("2026-09-13 12:05:00+05:30")).isEqualTo(want)
        assertThat(KitchenDeadline.parseServerInstant("2026-09-13 06:35:00.250+00"))
            .isEqualTo(want.plusMillis(250))
        assertThat(KitchenDeadline.parseServerInstant("soon")).isNull()
        assertThat(KitchenDeadline.of(KitchenOrderDto(id = "o", acceptDeadlineAt = "2026-09-13 06:35:00+00"), Instant.EPOCH))
            .isEqualTo(want)
    }
}
