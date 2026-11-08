package com.us.android.feature.kitchen.queue

import java.time.Duration
import java.time.Instant
import java.time.OffsetDateTime

/**
 * A `food_order_new` push, read for the new-order alarm (food audit M4).
 *
 * The push is the background path: once Android caches or freezes the kitchen
 * process, the in-app queue stops, and only FCM can wake it. The alarm then
 * loops until the order's accept deadline, so it never rings for an order the
 * server has already auto-rejected.
 */
object NewOrderPush {
    const val TYPE = "food_order_new"

    private const val DEFAULT_SLA_ACCEPT_SECONDS = 180L
    private const val MAX_RING_MINUTES = 10L

    /** food-service's default accept window (`sla_accept_seconds`), used when a push names no deadline. */
    val DEFAULT_RING: Duration = Duration.ofSeconds(DEFAULT_SLA_ACCEPT_SECONDS)

    /** A deadline further out than this is a clock or payload error; the alarm stops here regardless. */
    val MAX_RING: Duration = Duration.ofMinutes(MAX_RING_MINUTES)

    /**
     * How long the alarm may loop for [data], or null when its deadline has
     * already passed (nothing left to accept, so nothing rings). The deadline
     * is read from `accept_deadline_at`, else `expires_at`, as RFC 3339.
     */
    fun ringFor(data: Map<String, String>, now: Instant): Duration? {
        val raw = (data["accept_deadline_at"] ?: data["expires_at"])?.trim()?.takeIf { it.isNotEmpty() }
            ?: return DEFAULT_RING
        val deadline = runCatching { OffsetDateTime.parse(raw).toInstant() }.getOrNull() ?: return DEFAULT_RING
        val left = Duration.between(now, deadline)
        return when {
            left <= Duration.ZERO -> null
            left > MAX_RING -> MAX_RING
            else -> left
        }
    }
}

/**
 * Keeps a push-rung alarm from being silenced by a queue that has not caught up.
 *
 * The queue calls "silence" whenever ITS list holds nothing to accept, and
 * right after a push that list is stale: it has not fetched the new order yet.
 * So for [hold] after a push, the queue may not silence the alarm. The queue
 * ringing (it now has the order and owns the alarm) or the hold running out
 * ends it; opening the notification dismisses it as before.
 */
class PushRingHold(private val hold: Duration = DEFAULT_HOLD) {

    private var heldUntil: Instant? = null

    fun pushed(now: Instant) {
        heldUntil = now.plus(hold)
    }

    fun queueRang() {
        heldUntil = null
    }

    fun queueMaySilence(now: Instant): Boolean = heldUntil?.let { !now.isBefore(it) } ?: true

    companion object {
        /** Long enough for the queue to refetch after the push; short enough that a stale ring stops soon. */
        val DEFAULT_HOLD: Duration = Duration.ofSeconds(20)
    }
}
