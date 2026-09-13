package com.us.android.core.realtime

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import kotlin.random.Random

/** Pins `nextDouble` so the jitter factor is exact: 0.5 → ×1.0, 0.0 → ×0.8, 1.0 → ×1.2. */
internal class FixedRandom(private val value: Double) : Random() {
    override fun nextBits(bitCount: Int): Int = 0
    override fun nextDouble(): Double = value
}

class ReconnectBackoffTest {

    @Test
    fun `delays double from one second and stop at thirty`() {
        val backoff = ReconnectBackoff(random = FixedRandom(0.5))

        val delays = (0..7).map { backoff.delayMillis(it) }

        assertThat(delays).containsExactly(1_000L, 2_000L, 4_000L, 8_000L, 16_000L, 30_000L, 30_000L, 30_000L)
            .inOrder()
    }

    @Test
    fun `jitter spreads each delay by twenty percent either way, never past the cap`() {
        assertThat(ReconnectBackoff(random = FixedRandom(0.0)).delayMillis(2)).isEqualTo(3_200L)
        assertThat(ReconnectBackoff(random = FixedRandom(1.0)).delayMillis(2)).isEqualTo(4_800L)
        assertThat(ReconnectBackoff(random = FixedRandom(1.0)).delayMillis(10)).isEqualTo(30_000L)
        assertThat(ReconnectBackoff(random = FixedRandom(0.0)).delayMillis(10)).isEqualTo(24_000L)
    }

    @Test
    fun `a huge failure count does not overflow`() {
        assertThat(ReconnectBackoff(random = FixedRandom(0.5)).delayMillis(Int.MAX_VALUE)).isEqualTo(30_000L)
    }

    @Test
    fun `a server retry hint is a floor, still capped`() {
        val backoff = ReconnectBackoff(random = FixedRandom(0.5))

        assertThat(backoff.delayMillis(0, serverHintMillis = 5_000)).isEqualTo(5_000L)
        assertThat(backoff.delayMillis(4, serverHintMillis = 5_000)).isEqualTo(16_000L)
        assertThat(backoff.delayMillis(0, serverHintMillis = 120_000)).isEqualTo(30_000L)
    }
}
