package com.us.android.core.realtime

import kotlin.math.max
import kotlin.math.min
import kotlin.math.roundToLong
import kotlin.random.Random

/**
 * Reconnect delays: 1 s, 2 s, 4 s … capped at 30 s, each with ±20 % jitter.
 *
 * The cap is a hard ceiling — jitter is applied and the result clamped — so a
 * delay never exceeds [capMillis]; at the cap the spread is 24–30 s.
 *
 * Jitter matters more here than anywhere else in the app: a notification-service
 * restart drops every open stream at the same instant, and un-jittered clients
 * would all return on the same second, which is the rate limiter's
 * `RATE_LIMIT_OPENS` waiting to happen.
 *
 * [random] is injected so tests can pin the jitter.
 */
class ReconnectBackoff(
    private val baseMillis: Long = 1_000,
    private val capMillis: Long = 30_000,
    private val jitter: Double = 0.2,
    private val random: Random = Random.Default,
) {
    /**
     * The delay before the next attempt after [failures] consecutive failures
     * (0 for the first retry). [serverHintMillis] is an SSE `retry:` value,
     * used as a floor.
     */
    fun delayMillis(failures: Int, serverHintMillis: Long? = null): Long {
        val shift = failures.coerceIn(0, MAX_SHIFT)
        val exponential = min(baseMillis shl shift, capMillis)
        val floor = max(exponential, serverHintMillis ?: 0L).coerceAtMost(capMillis)
        val factor = 1.0 + (random.nextDouble() * 2.0 - 1.0) * jitter
        return (floor * factor).roundToLong().coerceIn(1L, capMillis)
    }

    private companion object {
        // 1 s shl 20 is far past any cap; stops the shift overflowing a Long.
        const val MAX_SHIFT = 20
    }
}

/** The one place the client waits. Injected so tests record delays instead of sleeping. */
fun interface RealtimeClock {
    suspend fun delay(millis: Long)

    companion object {
        val System: RealtimeClock = RealtimeClock { kotlinx.coroutines.delay(it) }
    }
}
