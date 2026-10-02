package com.us.android.feature.dating.home

import com.us.android.feature.dating.network.PulseMetaDto
import com.us.android.feature.dating.network.RateLimitDetailsDto
import java.time.Instant
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

/** How a card leaves the deck. The screen maps each to a side; the server decides whether it leaves at all. */
enum class DeckExit { PASS, SPARK, SUPER_SPARK, STASH }

/** The one action in flight: [userId]'s card is on its way out, pending the server's answer. */
data class DeckLeaving(val userId: String, val exit: DeckExit)

/** `SPARK_RATE_LIMITED`, in display terms. A 0 means the server did not say. */
data class SparkLimitUi(val limit: Int, val windowHours: Int, val resetsAt: Instant?)

/**
 * The deck beyond its cards: the daily allowance (mechanic M1), the action in
 * flight and the mechanic switches.
 *
 * [dailyLimit] is 0 when the server sent none — its refill flag is off — and
 * then nothing here shows and the deck never refetches on its own.
 */
data class DeckUi(
    val dailyLimit: Int = 0,
    /** Cards left today: the server's number, counted down as cards are sparked or passed. */
    val remaining: Int = 0,
    val resetsAt: Instant? = null,
    /** The next batch is being fetched because the stack ran empty. */
    val refilling: Boolean = false,
    val leaving: DeckLeaving? = null,
    /** Set by a refused spark; the screen shows the out-of-sparks pane until dismissed. */
    val sparkLimit: SparkLimitUi? = null,
    /** Super Spark is not available yet. A later change flips this; the gesture and button follow. */
    val superSparkEnabled: Boolean = false,
) {
    /** The server is counting cards for this viewer. */
    val metered: Boolean get() = dailyLimit > 0

    /** The allowance is spent: no cards until [resetsAt]. */
    val outOfCards: Boolean get() = metered && remaining <= 0
}

/** The allowance a `GET /pulse/today` meta carried, onto [current]. Absent, null, "" and 0 are all "none". */
internal fun PulseMetaDto?.onto(current: DeckUi): DeckUi {
    val limit = this?.dailyLimit?.coerceAtLeast(0) ?: 0
    return current.copy(
        dailyLimit = limit,
        remaining = if (limit > 0) this?.remainingToday?.coerceIn(0, limit) ?: 0 else 0,
        resetsAt = if (limit > 0) parseInstant(this?.resetsAt) else null,
    )
}

internal fun RateLimitDetailsDto?.toUi(): SparkLimitUi = SparkLimitUi(
    limit = this?.limit?.coerceAtLeast(0) ?: 0,
    windowHours = this?.windowHours?.coerceAtLeast(0) ?: 0,
    resetsAt = parseInstant(this?.resetsAt),
)

/** An RFC 3339 timestamp, or null for absent, blank and anything that does not parse. */
internal fun parseInstant(raw: String?): Instant? {
    val text = raw?.trim()?.takeIf { it.isNotEmpty() } ?: return null
    return runCatching { OffsetDateTime.parse(text).toInstant() }.getOrNull()
}

/** The deck's own words. Pure, so the tests can read them. */
object DeckCopy {

    const val OUT_OF_CARDS_TITLE = "That's today's cards"
    const val OUT_OF_SPARKS_TITLE = "You've used your sparks"

    /** The line above the deck, or null when the server is not counting or nothing is left. */
    fun cardsLeft(deck: DeckUi): String? = when {
        !deck.metered || deck.remaining <= 0 -> null
        deck.remaining == 1 -> "1 card left today"
        else -> "${deck.remaining} cards left today"
    }

    fun outOfCardsBody(deck: DeckUi, now: Instant, zone: ZoneId): String {
        val seen = if (deck.dailyLimit > 0) "You've been through all ${deck.dailyLimit} of today's cards. " else ""
        val back = deck.resetsAt?.let { whenLabel(it, now, zone) }
        return seen + if (back != null) "More arrive from $back." else "More arrive over the next day."
    }

    fun outOfSparksBody(limit: SparkLimitUi, now: Instant, zone: ZoneId): String {
        val allowance = when {
            limit.limit > 0 && limit.windowHours > 0 -> "You can send ${limit.limit} sparks every ${limit.windowHours} hours. "
            limit.limit > 0 -> "You can send ${limit.limit} sparks a day. "
            else -> ""
        }
        val back = limit.resetsAt?.let { whenLabel(it, now, zone) }
        val again = when {
            back != null -> "You can spark again from $back."
            limit.windowHours > 0 -> "They come back within ${limit.windowHours} hours."
            else -> "They come back soon."
        }
        return allowance + again + " You can still pass or save people for later."
    }

    /**
     * A reset time on the viewer's own clock: "6:30 PM today", "6:30 PM
     * tomorrow", or a dated form further out. Null once the moment has passed —
     * the caller then offers a refresh rather than a time that is behind us.
     */
    fun whenLabel(at: Instant, now: Instant, zone: ZoneId): String? {
        if (!at.isAfter(now)) return null
        val local = at.atZone(zone)
        val today = now.atZone(zone).toLocalDate()
        val time = TIME.format(local)
        return when (local.toLocalDate()) {
            today -> "$time today"
            today.plusDays(1) -> "$time tomorrow"
            else -> "$time on ${DAY.format(local)}"
        }
    }

    private val TIME = DateTimeFormatter.ofPattern("h:mm a", Locale.ENGLISH)
    private val DAY = DateTimeFormatter.ofPattern("d MMM", Locale.ENGLISH)
}
