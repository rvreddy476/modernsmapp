package com.us.android.feature.kitchen.queue

import com.us.android.core.food.network.KitchenOrderDto
import java.time.Duration
import java.time.Instant
import java.time.OffsetDateTime

/** The one place the kitchen reads the time. Injected so tests move it by hand. */
fun interface KitchenClock {
    fun now(): Instant

    companion object {
        val System: KitchenClock = KitchenClock { Instant.now() }
    }
}

/** Where a CONFIRMED order stands against its accept deadline. */
sealed interface AcceptWindow {
    /** [remainingSeconds] rounded UP, so "1 s" shows until the deadline itself. */
    data class Open(val remainingSeconds: Long, val urgent: Boolean) : AcceptWindow

    /** The server gave no deadline. Accept and reject stay available. */
    data object NoDeadline : AcceptWindow

    /** The deadline passed; the server auto-rejects within its 15 s sweep. Accept is refused. */
    data object Expired : AcceptWindow

    data object Accepted : AcceptWindow

    data object Rejected : AcceptWindow

    val canRespond: Boolean get() = this is Open || this == NoDeadline

    val isTerminal: Boolean get() = this == Expired || this == Accepted || this == Rejected
}

/**
 * The accept countdown for one order, as a small state machine.
 *
 * ```
 * Open ──tick past deadline──▶ Expired
 * Open / NoDeadline ──confirmAccepted──▶ Accepted
 * Open / NoDeadline ──confirmRejected──▶ Rejected
 * Expired ──reschedule(server says time remains)──▶ Open
 * ```
 *
 * Accepted and Rejected are final. Expired is final for the partner, but a
 * fresh server deadline may reopen it: the server's `seconds_to_breach` is the
 * truth, and a device clock that jumped must not strand an order it would
 * still accept.
 */
class AcceptCountdown(
    deadline: Instant?,
    now: Instant,
    private val urgentThresholdSeconds: Long = URGENT_THRESHOLD_SECONDS,
) {
    var deadline: Instant? = deadline
        private set

    var window: AcceptWindow = AcceptWindow.NoDeadline
        private set

    init {
        tick(now)
    }

    fun tick(now: Instant): AcceptWindow {
        if (window == AcceptWindow.Accepted || window == AcceptWindow.Rejected) return window
        val due = deadline
        window = if (due == null) {
            AcceptWindow.NoDeadline
        } else {
            val remainingMillis = Duration.between(now, due).toMillis()
            if (remainingMillis <= 0) {
                AcceptWindow.Expired
            } else {
                val seconds = (remainingMillis + MILLIS_PER_SECOND - 1) / MILLIS_PER_SECOND
                AcceptWindow.Open(seconds, urgent = seconds <= urgentThresholdSeconds)
            }
        }
        return window
    }

    /** Whether the partner may act right now. The UI disables the buttons when false. */
    fun canRespond(now: Instant): Boolean = tick(now).canRespond

    /** A newer server deadline. Ignored once the order is accepted or rejected. */
    fun reschedule(newDeadline: Instant?, now: Instant): AcceptWindow {
        if (window == AcceptWindow.Accepted || window == AcceptWindow.Rejected) return window
        deadline = newDeadline
        return tick(now)
    }

    /** The server accepted. Only from a window that allowed it. */
    fun confirmAccepted(now: Instant): Boolean {
        if (!canRespond(now)) return false
        window = AcceptWindow.Accepted
        return true
    }

    fun confirmRejected(now: Instant): Boolean {
        if (!canRespond(now)) return false
        window = AcceptWindow.Rejected
        return true
    }

    companion object {
        const val URGENT_THRESHOLD_SECONDS = 30L
        private const val MILLIS_PER_SECOND = 1_000L
    }
}

/** Reading a queue row's deadline. */
object KitchenDeadline {

    /**
     * The deadline for [order] as fetched at [fetchedAt] (device time).
     *
     * Prefers `seconds_to_breach`: the server computed it at response time, so
     * adding it to the moment the response arrived is immune to the device
     * clock being wrong. `accept_deadline_at` is Postgres text and only a
     * fallback.
     */
    fun of(order: KitchenOrderDto, fetchedAt: Instant): Instant? =
        order.secondsToBreach?.let { fetchedAt.plusSeconds(it.toLong()) }
            ?: order.acceptDeadlineAt?.let(::parseServerInstant)

    /** RFC 3339, or Postgres `timestamptz::text` (`2026-09-13 06:35:00.5+00`). Null when neither. */
    fun parseServerInstant(raw: String): Instant? {
        var text = raw.trim()
        if (text.isEmpty()) return null
        text = text.replaceFirst(' ', 'T')
        if (SHORT_OFFSET.containsMatchIn(text)) text += ":00"
        return runCatching { OffsetDateTime.parse(text).toInstant() }.getOrNull()
    }

    private val SHORT_OFFSET = Regex("T.*[+-][0-9]{2}$")
}
