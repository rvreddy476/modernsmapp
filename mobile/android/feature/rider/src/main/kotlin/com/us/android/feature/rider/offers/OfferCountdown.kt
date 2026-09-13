package com.us.android.feature.rider.offers

import java.time.Duration
import java.time.Instant
import java.time.OffsetDateTime

/** The one place the rider screens read wall time. Injected so tests move it by hand. */
fun interface RiderClock {
    fun now(): Instant

    companion object {
        val System: RiderClock = RiderClock { Instant.now() }
    }
}

sealed interface OfferWindow {
    /** [remainingSeconds] rounded UP, so "1 s" shows until the expiry itself. */
    data class Open(val remainingSeconds: Long, val urgent: Boolean) : OfferWindow

    /** The offer expired; the server has already moved it to the next rider. Accept is refused. */
    data object Expired : OfferWindow

    /** The expiry could not be read. Accept and reject stay available; the server decides. */
    data object Unknown : OfferWindow

    val canRespond: Boolean get() = this !is Expired
}

/**
 * The countdown to an offer's `expires_at`.
 *
 * Unlike the kitchen queue there is no server-computed "seconds left" on an
 * offer, so this reads the absolute expiry against the device clock. A device
 * clock that runs fast shows less time than there is; the server stays the
 * authority on accept either way.
 */
class OfferCountdown(
    val expiresAt: Instant?,
    private val urgentThresholdSeconds: Long = URGENT_THRESHOLD_SECONDS,
) {
    fun at(now: Instant): OfferWindow {
        val due = expiresAt ?: return OfferWindow.Unknown
        val remainingMillis = Duration.between(now, due).toMillis()
        if (remainingMillis <= 0) return OfferWindow.Expired
        val seconds = (remainingMillis + MILLIS_PER_SECOND - 1) / MILLIS_PER_SECOND
        return OfferWindow.Open(seconds, urgent = seconds <= urgentThresholdSeconds)
    }

    fun canRespond(now: Instant): Boolean = at(now).canRespond

    companion object {
        const val URGENT_THRESHOLD_SECONDS = 10L
        private const val MILLIS_PER_SECOND = 1_000L

        fun of(rawExpiresAt: String?): OfferCountdown = OfferCountdown(rawExpiresAt?.let(ServerTime::parse))
    }
}

/** Reading food-service timestamps. */
object ServerTime {

    /**
     * RFC 3339 (the push payload's `expires_at`), or Postgres `timestamptz::text`
     * (`2026-09-13 06:35:00.5+00`, the offers route). Null when neither.
     */
    fun parse(raw: String): Instant? {
        var text = raw.trim()
        if (text.isEmpty()) return null
        text = text.replaceFirst(' ', 'T')
        if (SHORT_OFFSET.containsMatchIn(text)) text += ":00"
        return runCatching { OffsetDateTime.parse(text).toInstant() }.getOrNull()
    }

    private val SHORT_OFFSET = Regex("T.*[+-][0-9]{2}$")
}
