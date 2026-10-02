package com.us.android.feature.dating.home

import com.us.android.feature.dating.network.AllowanceDto
import com.us.android.feature.dating.network.FairTurnDetailsDto
import com.us.android.feature.dating.network.FairTurnDto
import com.us.android.feature.dating.network.PulseMetaDto
import com.us.android.feature.dating.network.RateLimitDetailsDto
import com.us.android.feature.dating.network.SuperSparkAllowanceDto
import com.us.android.feature.dating.travel.TripUi
import java.time.Instant
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

/** How a card leaves the deck. The screen maps each to a side; the server decides whether it leaves at all. */
enum class DeckExit { PASS, SPARK, SUPER_SPARK, STASH }

/** The one action in flight: [userId]'s card is on its way out, pending the server's answer. */
data class DeckLeaving(val userId: String, val exit: DeckExit)

/**
 * An allowance refusal in display terms: `SPARK_RATE_LIMITED`,
 * `REWIND_LIMIT_REACHED` or `SUPER_SPARK_LIMIT_REACHED`, which all carry the
 * same details. A 0 means the server did not say.
 */
data class SparkLimitUi(val limit: Int, val windowHours: Int, val resetsAt: Instant?)

/**
 * One daily allowance from `GET /allowances`, in display terms. [unlimited]
 * (a pass holder) carries no counts. Absent, null and 0 all read as none left.
 */
data class AllowanceUi(
    val unlimited: Boolean = false,
    val dailyLimit: Int = 0,
    val remaining: Int = 0,
    val resetsAt: Instant? = null,
)

/**
 * The deck beyond its cards: the daily allowance (mechanic M1), the action in
 * flight, and the mechanics `GET /allowances` switched on — undoing a pass
 * (M2) and Super Spark (M3).
 *
 * [dailyLimit] is 0 when the server sent none — its refill flag is off — and
 * then nothing here shows and the deck never refetches on its own.
 *
 * [rewind] and [superSpark] are null while their mechanic is off: absent from
 * the allowances read, not read yet, or refused `MECHANIC_NOT_ENABLED` in this
 * session. Null means the control is not drawn at all.
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
    /** The daily Super Spark allowance, or null while the mechanic is off. */
    val superSpark: AllowanceUi? = null,
    /** Super Sparks bought in packs, spent once the daily allowance is used. */
    val superSparkBalance: Int = 0,
    /** Set by `SUPER_SPARK_LIMIT_REACHED`; the out-of-Super-Sparks pane shows until dismissed. */
    val superSparkLimit: SparkLimitUi? = null,
    /** The undo allowance, or null while the mechanic is off. */
    val rewind: AllowanceUi? = null,
    /**
     * Whom the last deck action in this session passed on, while that pass is
     * still the last action. A spark, a Super Spark or a save clears it, and so
     * does an undo: the server undoes one step only.
     */
    val rewindable: String? = null,
    /** Set by `REWIND_LIMIT_REACHED`; the out-of-undos pane shows until dismissed. */
    val rewindLimit: SparkLimitUi? = null,
    /** Mechanic M8 is on: the top bar offers Travel. False until `GET /travel` answers. */
    val travelEnabled: Boolean = false,
    /** The viewer's own trip in effect: the deck says whose city it is showing. */
    val trip: TripUi? = null,
    /**
     * Mechanic M11: replies the viewer owes, from `GET /allowances` or a
     * `409 FAIR_TURN_LIMIT`. Null while the server sends none (its flag off).
     */
    val fairTurn: FairTurnUi? = null,
) {
    /** The server is counting cards for this viewer. */
    val metered: Boolean get() = dailyLimit > 0

    /** The allowance is spent: no cards until [resetsAt]. */
    val outOfCards: Boolean get() = metered && remaining <= 0

    /** Super Spark is switched on: the button, the upward swipe and the TalkBack action follow this. */
    val superSparkEnabled: Boolean get() = superSpark != null

    /** The undo control is drawn: the mechanic is on and the last action was a pass the server took. */
    val canRewind: Boolean get() = rewind != null && rewindable != null

    /**
     * Mechanic M11: new sparks are paused until the viewer replies to some
     * matches. Spark and Super Spark are held back on the deck; passing,
     * saving and the Sparks tab (sparking back) carry on.
     */
    val sparksPaused: Boolean get() = fairTurn?.paused == true
}

/** Fair turn (mechanic M11) in display terms. A 0 means the server did not say. */
data class FairTurnUi(val owed: Int, val limit: Int, val paused: Boolean)

internal fun FairTurnDto.toUi(): FairTurnUi =
    FairTurnUi(owed = owed.coerceAtLeast(0), limit = limit.coerceAtLeast(0), paused = paused)

/** `409 FAIR_TURN_LIMIT`'s details: paused by definition, whatever the numbers say. */
internal fun FairTurnDetailsDto?.toUi(): FairTurnUi =
    FairTurnUi(owed = this?.owed?.coerceAtLeast(0) ?: 0, limit = this?.limit?.coerceAtLeast(0) ?: 0, paused = true)

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

internal fun AllowanceDto.toUi(): AllowanceUi = AllowanceUi(
    unlimited = unlimited,
    dailyLimit = if (unlimited) 0 else dailyLimit.coerceAtLeast(0),
    remaining = if (unlimited) 0 else remainingToday.coerceAtLeast(0),
    resetsAt = parseInstant(resetsAt),
)

internal fun SuperSparkAllowanceDto.toUi(): AllowanceUi =
    AllowanceDto(unlimited, dailyLimit, remainingToday, resetsAt).toUi()

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

    // Mechanic M11 — fair turn. Calm: nobody did anything wrong.
    const val FAIR_TURN_TITLE = "Your matches are waiting on you"
    const val FAIR_TURN_ACTION = "Go to your matches"
    const val FAIR_TURN_SPARK_HELD = "New sparks open again once you've replied."

    /** The notice's body; the count only when the server sent one. */
    fun fairTurnBody(fairTurn: FairTurnUi): String {
        val waiting = when {
            fairTurn.owed == 1 -> "1 match is waiting for your reply."
            fairTurn.owed > 1 -> "${fairTurn.owed} matches are waiting for your reply."
            else -> "Some of your matches are waiting for your reply."
        }
        return "$waiting Write back to a few of them and you can send new sparks again. You can still pass and save people for later."
    }

    const val UNDO = "Undo pass"
    const val OUT_OF_UNDOS_TITLE = "No more undos today"
    const val OUT_OF_SUPER_SPARKS_TITLE = "You've used your Super Sparks"

    /** The quiet line beside the undo control, or null while the mechanic is off. */
    fun undosLeft(deck: DeckUi): String? {
        val rewind = deck.rewind ?: return null
        return when {
            rewind.unlimited -> "Unlimited undos"
            rewind.remaining <= 0 -> "No undos left today"
            else -> "${count(rewind.remaining, "undo", "undos")} left today"
        }
    }

    /** "2 Super Sparks left today · 3 from packs", or null while the mechanic is off. */
    fun superSparksLeft(deck: DeckUi): String? {
        val daily = deck.superSpark ?: return null
        val packs = deck.superSparkBalance.coerceAtLeast(0)
        return when {
            daily.unlimited -> "Unlimited Super Sparks"
            daily.remaining > 0 && packs > 0 -> "${count(daily.remaining, SUPER_SPARK, SUPER_SPARKS)} left today · $packs from packs"
            daily.remaining > 0 -> "${count(daily.remaining, SUPER_SPARK, SUPER_SPARKS)} left today"
            packs > 0 -> "${count(packs, SUPER_SPARK, SUPER_SPARKS)} from packs"
            else -> "No Super Sparks left today"
        }
    }

    fun outOfUndosBody(limit: SparkLimitUi, now: Instant, zone: ZoneId): String {
        val allowance = when {
            limit.limit > 0 && limit.windowHours > 0 -> "You can undo ${count(limit.limit, "pass", "passes")} every ${limit.windowHours} hours. "
            limit.limit > 0 -> "You can undo ${count(limit.limit, "pass", "passes")} a day. "
            else -> ""
        }
        val back = limit.resetsAt?.let { whenLabel(it, now, zone) }
        val again = when {
            back != null -> "You can undo again from $back."
            limit.windowHours > 0 -> "Undos come back within ${limit.windowHours} hours."
            else -> "Undos come back soon."
        }
        return allowance + again + " With a Premium pass you can undo as often as you like."
    }

    fun outOfSuperSparksBody(limit: SparkLimitUi, packs: Int, now: Instant, zone: ZoneId): String {
        val allowance = when {
            limit.limit > 0 && limit.windowHours > 0 -> "You get ${count(limit.limit, SUPER_SPARK, SUPER_SPARKS)} every ${limit.windowHours} hours. "
            limit.limit > 0 -> "You get ${count(limit.limit, SUPER_SPARK, SUPER_SPARKS)} a day. "
            else -> ""
        }
        val back = limit.resetsAt?.let { whenLabel(it, now, zone) }
        val again = when {
            back != null -> "More arrive from $back."
            limit.windowHours > 0 -> "More arrive within ${limit.windowHours} hours."
            else -> "More arrive soon."
        }
        return "$allowance$again Super Sparks from packs: ${packs.coerceAtLeast(0)}. A pack adds more straight away."
    }

    private fun count(n: Int, one: String, many: String): String = if (n == 1) "1 $one" else "$n $many"

    private const val SUPER_SPARK = "Super Spark"
    private const val SUPER_SPARKS = "Super Sparks"

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
