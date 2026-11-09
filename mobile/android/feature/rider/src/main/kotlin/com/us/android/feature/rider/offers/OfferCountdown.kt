package com.us.android.feature.rider.offers

import com.us.android.core.food.network.DeliveryOfferDto
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

    /** The offer expired on the server's clock; it has moved to the next rider. Accept is greyed. */
    data object Expired : OfferWindow

    /** The expiry could not be read. Accept and reject stay available; the server decides. */
    data object Unknown : OfferWindow

    val canRespond: Boolean get() = this !is Expired
}

/**
 * The countdown to an offer's expiry.
 *
 * The expiry comes from [OfferExpiry]: the offer's `seconds_to_expiry` when
 * the server sent one, else its absolute `expires_at`. Either is read against
 * the SERVER's clock: callers pass [ServerClockOffset.serverNow], never the
 * bare device time (food audit L1). A device clock 20 s fast would otherwise grey every offer. The server stays
 * the authority on accept either way; nothing here refuses a tap.
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

/**
 * The server's clock as this device sees it: the gap between a response's
 * `Date` header and the device clock when that response arrived. The header
 * has one-second resolution, so the estimate is up to a second behind: a
 * countdown may show a second more than the server allows, never less.
 */
data class ServerClockOffset(val offset: Duration) {
    fun serverNow(deviceNow: Instant): Instant = deviceNow.plus(offset)

    companion object {
        val NONE = ServerClockOffset(Duration.ZERO)

        fun from(serverTime: Instant?, receivedAt: Instant): ServerClockOffset =
            serverTime?.let { ServerClockOffset(Duration.between(receivedAt, it)) } ?: NONE
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

/**
 * Each offer's expiry, on the server's clock, as the countdown should read it.
 *
 * An offer that carries `seconds_to_expiry` (food server lane, 9 Nov 2026)
 * expires that many seconds after it was READ: `serverNow(receivedAt) + s`.
 * Measured against `serverNow(now)` that is `receivedAt + s - now`, so the
 * phone's clock and the `Date` header's one-second resolution drop out. An
 * offer without it (an older server, a pushed offer) falls back to its
 * `expires_at` on the server clock (food audit L1).
 */
object OfferExpiry {

    /** The expiries the offers read at [serverNowAtReceipt] carry, by offer id. */
    fun fromCountdowns(offers: List<DeliveryOfferDto>, serverNowAtReceipt: Instant): Map<String, Instant> =
        offers.mapNotNull { offer ->
            offer.secondsToExpiry?.takeIf { it >= 0 }?.let { offer.id to serverNowAtReceipt.plusSeconds(it) }
        }.toMap()

    /** [offer]'s expiry: its countdown when one was read, else its `expires_at`. */
    fun of(offer: DeliveryOfferDto, countdowns: Map<String, Instant>): Instant? =
        countdowns[offer.id] ?: ServerTime.parse(offer.expiresAt)
}
