package com.us.android.feature.doorsteppro.domain

import com.us.android.feature.doorsteppro.ui.instantOrNull
import java.time.Duration
import java.time.Instant

/** The one place the professional's screens read wall time. Injected so tests move it by hand. */
fun interface ProClock {
    fun now(): Instant

    companion object {
        val System: ProClock = ProClock { Instant.now() }
    }
}

sealed interface OfferWindow {
    /** [remainingSeconds] rounded UP, so "0:01" shows until the expiry itself. */
    data class Open(val remainingSeconds: Long, val urgent: Boolean) : OfferWindow

    /** The offer expired; dispatch has already moved on to the next professional. Accept is refused. */
    data object Expired : OfferWindow

    /** The expiry could not be read. Accept and decline stay available; the server decides. */
    data object Unknown : OfferWindow

    val canRespond: Boolean get() = this !is Expired
}

/**
 * The countdown to an offer's `expires_at` (Feast Rider's, copied). The offer
 * window is 10 minutes for a slot within 12 hours and 2 hours for a later one
 * (city config), so the urgent threshold is a minute, not seconds: a
 * professional needs time to read the job. The device clock is read against
 * the absolute expiry; the server stays the authority on accept.
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
        const val URGENT_THRESHOLD_SECONDS = 60L
        private const val MILLIS_PER_SECOND = 1_000L

        fun of(rawExpiresAt: String?): OfferCountdown = OfferCountdown(instantOrNull(rawExpiresAt))
    }
}

/** Why a professional turns an offer down (DeclineRequest.reason enum). */
enum class DeclineReason(val wire: String, val label: String) {
    TOO_FAR("too_far", "Too far"),
    BUSY("busy", "Busy then"),
    NOT_MY_SKILL("not_my_skill", "Not my skill"),
    OTHER("other", "Other reason"),
}
