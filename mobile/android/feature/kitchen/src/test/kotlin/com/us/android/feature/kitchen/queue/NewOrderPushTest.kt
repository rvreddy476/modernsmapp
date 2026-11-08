package com.us.android.feature.kitchen.queue

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import java.time.Duration
import java.time.Instant

/** The push-driven new-order alarm (food audit M4): how long it rings, and who may stop it. */
class NewOrderPushTest {

    private val now = Instant.parse("2026-11-08T06:30:00Z")
    private val push = mapOf("type" to "food_order_new", "order_id" to "o-1")

    @Test
    fun `a push with no deadline rings for the default accept window`() {
        assertThat(NewOrderPush.ringFor(push, now)).isEqualTo(Duration.ofSeconds(180))
    }

    @Test
    fun `a push with an accept deadline rings until it`() {
        val data = push + ("accept_deadline_at" to "2026-11-08T06:31:30Z")

        assertThat(NewOrderPush.ringFor(data, now)).isEqualTo(Duration.ofSeconds(90))
    }

    @Test
    fun `expires_at is read when accept_deadline_at is absent`() {
        val data = push + ("expires_at" to "2026-11-08T12:00:40+05:30")

        assertThat(NewOrderPush.ringFor(data, now)).isEqualTo(Duration.ofSeconds(40))
    }

    @Test
    fun `a push whose deadline has passed does not ring`() {
        assertThat(NewOrderPush.ringFor(push + ("accept_deadline_at" to "2026-11-08T06:29:59Z"), now)).isNull()
        assertThat(NewOrderPush.ringFor(push + ("accept_deadline_at" to "2026-11-08T06:30:00Z"), now)).isNull()
    }

    @Test
    fun `a far or unreadable deadline is bounded`() {
        assertThat(NewOrderPush.ringFor(push + ("accept_deadline_at" to "2026-11-09T06:30:00Z"), now))
            .isEqualTo(Duration.ofMinutes(10))
        assertThat(NewOrderPush.ringFor(push + ("accept_deadline_at" to "soon"), now))
            .isEqualTo(Duration.ofSeconds(180))
    }

    @Test
    fun `a stale queue cannot silence a push-rung alarm during the hold`() {
        val hold = PushRingHold(Duration.ofSeconds(20))
        assertThat(hold.queueMaySilence(now)).isTrue()

        hold.pushed(now)

        assertThat(hold.queueMaySilence(now.plusSeconds(19))).isFalse()
        assertThat(hold.queueMaySilence(now.plusSeconds(20))).isTrue()
    }

    @Test
    fun `the queue ringing takes the alarm over, so it may silence it later`() {
        val hold = PushRingHold(Duration.ofSeconds(20))
        hold.pushed(now)

        hold.queueRang()

        assertThat(hold.queueMaySilence(now.plusSeconds(1))).isTrue()
    }
}
