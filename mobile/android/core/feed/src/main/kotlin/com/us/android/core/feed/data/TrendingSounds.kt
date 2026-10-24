package com.us.android.core.feed.data

import com.us.android.core.media.ChosenSound
import com.us.android.core.model.ReelSound
import com.us.android.core.model.ReelSoundWire
import com.us.android.core.model.toReelSound
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.doubleOrNull
import kotlinx.serialization.json.longOrNull
import java.util.Locale

/*
 * Trending sounds (2026-10-24): media-service ranks our own sounds — the
 * ones taken from reels and the curated library's — by how many people used
 * each in the last seven days, every 15 minutes, and serves the stored list.
 *
 * What a ranked row is, how the answer is read, what the screens say, and
 * which badge a sound page wears. All pure, so the rules are pinned by
 * tests. The read is TOLERANT on purpose: the route is new and built beside
 * this client, so an answer in the route's old shape (`{tracks}`), a row
 * that is not an object, a number sent as a string or a missing field never
 * fails the list — a row that cannot be read is dropped, and nothing else.
 */

/** Which ranking the list shows: `GET v1/audio/trending?filter=`. */
enum class TrendingFilter(val wire: String, val label: String) {
    ALL("all", "All"),
    RISING("rising", "Rising"),
}

/** One ranked sound. */
data class TrendingSound(
    /** 1-based, the server's; null when it sent none, and then no number is drawn. */
    val rank: Int?,
    /** An `audio_tracks` sound: playable by id, and what a reel is made with. */
    val sound: ReelSound,
    /**
     * A cover when the server sends one, as sent until the repository resolves
     * it ([resolveSoundCover]); the row draws a music tile otherwise.
     */
    val coverUrl: String?,
    /** Distinct (author, day) uses in the last seven days. */
    val uses7d: Long,
    /** The same, the seven days before. */
    val usesPrev7d: Long,
    val rising: Boolean,
)

/** One page of the ranking. */
data class TrendingSoundsPage(
    val sounds: List<TrendingSound>,
    /** Null at the end; an empty cursor (a Go zero value) is the end too. */
    val nextCursor: String?,
    /** When the stored list was computed, as the server wrote it; null when it did not say. */
    val updatedAt: String?,
)

/** The `trending` block of a single sound's read: `{"rank": 3, "rising": false} | null`. */
data class TrendingMark(val rank: Int?, val rising: Boolean)

// ── reading the answer ──────────────────────────────────────────────────

/**
 * `data` of `GET v1/audio/trending` as ranked sounds. Anything but an array
 * is no rows (the route's old `{tracks}` shape included, which was never
 * ranked by these rules); a row that is not an object, has no `sound`, or
 * whose sound has no id or is not ready is dropped. A sound twice keeps its
 * first row.
 */
fun decodeTrendingSounds(data: JsonElement?): List<TrendingSound> {
    val rows = data as? JsonArray ?: return emptyList()
    return rows.mapNotNull { (it as? JsonObject)?.toTrendingSound() }.distinctBy { it.sound.id }
}

/** One page: the rows, and `meta`'s cursor and time. */
fun trendingSoundsPage(data: JsonElement?, nextCursor: String?, updatedAt: String?): TrendingSoundsPage =
    TrendingSoundsPage(
        sounds = decodeTrendingSounds(data),
        nextCursor = nextCursor?.trim()?.takeIf { it.isNotEmpty() },
        updatedAt = updatedAt?.trim()?.takeIf { it.isNotEmpty() },
    )

private fun JsonObject.toTrendingSound(): TrendingSound? {
    val row = this["sound"] as? JsonObject ?: return null
    if (row.text("status").lowercase() !in USABLE_STATUS) return null
    val sound = ReelSoundWire(
        id = row.text("id"),
        title = row.text("title"),
        artist = row.text("artist"),
        durationMs = row.long("duration_ms"),
        // media-service writes usage_count; a post read's spelling is use_count.
        useCount = row.long("use_count"),
        usageCount = row.long("usage_count"),
        sourcePostId = row.text("source_post_id"),
        sourceReelId = row.text("source_reel_id"),
        creatorUserId = row.text("creator_user_id"),
    ).toReelSound() ?: return null
    val uses = (long("uses_7d") ?: 0L).coerceAtLeast(0L)
    val previous = (long("uses_prev_7d") ?: 0L).coerceAtLeast(0L)
    return TrendingSound(
        rank = long("rank")?.takeIf { it in 1..Int.MAX_VALUE }?.toInt(),
        sound = sound,
        coverUrl = row.text("cover_url").ifEmpty { null },
        uses7d = uses,
        usesPrev7d = previous,
        // The server's word; the contract's own rule only when it said nothing.
        rising = bool("rising") ?: isRising(uses, previous),
    )
}

/**
 * The contract's Rising rule, for a row that does not say: at least
 * [MIN_TRENDING_USES] uses this week, and none the week before or at least
 * twice as many as then.
 */
fun isRising(uses7d: Long, usesPrev7d: Long): Boolean =
    uses7d >= MIN_TRENDING_USES && (usesPrev7d <= 0L || uses7d >= 2 * usesPrev7d)

/**
 * The `trending` block of `GET v1/audio/{id}`, or null when the sound is not
 * on the list: absent, null, not an object, or naming neither a rank nor
 * Rising.
 */
fun trendingMark(element: JsonElement?): TrendingMark? {
    val block = element as? JsonObject ?: return null
    val rank = block.long("rank")?.takeIf { it in 1..Int.MAX_VALUE }?.toInt()
    val rising = block.bool("rising") ?: false
    if (rank == null && !rising) return null
    return TrendingMark(rank = rank, rising = rising)
}

/** The `trending` block from a sound read's whole `data`. */
fun trendingMarkOfSound(data: JsonElement?): TrendingMark? = trendingMark((data as? JsonObject)?.get("trending"))

// ── what the screens say ────────────────────────────────────────────────

/**
 * The sound page's badges, in order: "#3 trending" when it is ranked, then
 * "Rising" when it is rising. Nothing when it is not on the list.
 */
fun trendingBadges(mark: TrendingMark?): List<String> {
    if (mark == null) return emptyList()
    return listOfNotNull(
        mark.rank?.let { "#$it trending" },
        RISING_LABEL.takeIf { mark.rising },
    )
}

/** "42 reels this week", "1 reel this week", "1,200 reels this week"; nothing at zero. */
fun reelsThisWeek(uses: Long): String = when {
    uses <= 0L -> ""
    uses == 1L -> "1 reel this week"
    else -> "${String.format(Locale.US, "%,d", uses)} reels this week"
}

/** "#1"; nothing when the server sent no rank. */
fun trendingRankLabel(rank: Int?): String = rank?.let { "#$it" }.orEmpty()

/** The shelf over the sound picker: the top of the All ranking. */
fun trendingShelf(all: List<TrendingSound>): List<TrendingSound> = all.take(TRENDING_SHELF_SIZE)

/** A later page after the rows shown: nothing twice, the earlier row kept. */
fun appendTrending(shown: List<TrendingSound>, next: List<TrendingSound>): List<TrendingSound> =
    (shown + next).distinctBy { it.sound.id }

/**
 * A cover as the row can draw it: an https URL as sent (a library sound's
 * own art), a gateway path (`/v1/media/{id}/serve/thumb_150`, the source
 * reel's still) on the API origin — where the image loader adds the bearer —
 * and anything else (a scheme-relative `//host`, plain http, a stray word)
 * as no cover.
 */
fun resolveSoundCover(raw: String?, apiBaseUrl: String): String? {
    val value = raw?.trim().orEmpty()
    return when {
        value.startsWith("https://") -> value
        value.startsWith("/") && !value.startsWith("//") && apiBaseUrl.isNotBlank() ->
            apiBaseUrl.trimEnd('/') + value
        else -> null
    }
}

/** What "Use this sound" leaves in `SoundEntry`: the `audio_tracks` id, as every other door does. */
fun TrendingSound.toChosenSound(): ChosenSound = ChosenSound(
    id = sound.id,
    title = sound.title,
    artist = sound.artist,
    durationMs = sound.durationMs,
)

/** What the list says when its first page cannot be read. */
const val TRENDING_UNAVAILABLE = "Trending sounds aren't available right now."

/** The Rising chip and badge. */
const val RISING_LABEL = "Rising"

/** The contract's floor: fewer uses this week and a sound is not on the list. */
const val MIN_TRENDING_USES = 3L

/** How many the picker's shelf shows. */
const val TRENDING_SHELF_SIZE = 20

// ── tolerant field reads ────────────────────────────────────────────────

private fun JsonObject.primitive(key: String): JsonPrimitive? =
    (this[key] as? JsonPrimitive)?.takeUnless { it is JsonNull }

private fun JsonObject.text(key: String): String = primitive(key)?.content?.trim().orEmpty()

/** A whole number, sent as a number or a string; a fraction is cut. */
private fun JsonObject.long(key: String): Long? {
    val value = primitive(key) ?: return null
    return value.longOrNull ?: value.doubleOrNull?.takeIf { it.isFinite() }?.toLong()
}

private fun JsonObject.bool(key: String): Boolean? = primitive(key)?.booleanOrNull
