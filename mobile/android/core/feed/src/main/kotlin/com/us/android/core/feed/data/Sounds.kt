package com.us.android.core.feed.data

import com.us.android.core.common.error.AppError
import com.us.android.core.feed.data.dto.FeedItemDto
import com.us.android.core.media.publish.playsInReels
import com.us.android.core.model.FeedItem
import com.us.android.core.model.ReelSound
import com.us.android.core.model.ReelSoundWire
import com.us.android.core.model.toReelSound
import com.us.android.core.network.ApiMeta
import java.util.Locale

/*
 * Original sounds (2026-09-30): what the screens share — what a sound is
 * called, what a refusal says, and the shape of a page of the reels that play
 * one. All pure, so the rules are pinned by tests. The web's `sounds.ts`,
 * rule for rule; the addresses in that file have no place here, because on
 * Android a sound page is a route and the studio is a handoff holder.
 */

// ── words ───────────────────────────────────────────────────────────────

/** What a reel with no added sound calls its own audio: "Original sound - Asha", with a plain hyphen. */
fun originalSoundLabel(authorName: String): String {
    val name = authorName.trim()
    return if (name.isEmpty()) ORIGINAL_SOUND else "$ORIGINAL_SOUND - $name"
}

/** "1 reel", "12 reels", "1,200 reels"; "No reels yet" at zero. */
fun soundReelCount(count: Int): String = when {
    count <= 0 -> "No reels yet"
    count == 1 -> "1 reel"
    else -> "${String.format(Locale.US, "%,d", count)} reels"
}

/** What the sound page says for a sound that is gone, or not this viewer's to hear: the two are one answer. */
const val SOUND_GONE_MESSAGE = "This sound is no longer available."

/**
 * The line shown when "use this sound" is refused, by the server's code —
 * never by its message. The ONE wording for every surface that offers the
 * action (the reel's sound line, the More sheet's row, the sound page's
 * button), so they cannot drift.
 */
fun soundRefusalMessage(code: String?): String = when (code) {
    "SOUND_REUSE_NOT_ALLOWED" -> "The creator has turned off reuse for this reel."
    "NOT_READY" -> "This reel is still processing. Try again in a moment."
    "TOO_LONG", "NOT_A_REEL", "NOT_A_VIDEO" -> "Only reels up to 5 minutes can be used as a sound."
    "NO_AUDIO" -> "This reel has no sound to use."
    "NOT_FOUND", "404" -> "This reel is no longer available."
    "RATE_LIMITED" -> "That is a lot of sounds in one hour. Try again later."
    else -> "Please try again."
}

/**
 * The server's code for a failed sound call, whichever case the mapper put it
 * in: a 403 keeps its code, a 404 and a 429 are named by their status, and a
 * 422 (`NOT_READY`, `TOO_LONG`, `NO_AUDIO`…) arrives as an unmodelled code.
 * Null when there is no code to branch on (no network, a timeout).
 */
fun AppError.soundRefusalCode(): String? = when (this) {
    is AppError.Forbidden -> code
    is AppError.NotFound -> "NOT_FOUND"
    is AppError.RateLimited -> "RATE_LIMITED"
    is AppError.Server -> code
    is AppError.Unknown -> code
    else -> null
}

/** [soundRefusalMessage] for an error as the repository answers it. */
fun AppError.soundRefusalMessage(): String = soundRefusalMessage(soundRefusalCode())

// ── the answers ─────────────────────────────────────────────────────────

/** A sound row that may be played: an absent or empty status is taken as ready. */
private val USABLE_STATUS = setOf("", "ready", "active")

/**
 * media-service's row as a [ReelSound], or null when it is no sound, or one
 * that is not ready to be played. The row has no start of its own: a sound
 * chosen for a new reel starts at 0.
 */
fun SoundRowDto.toReelSound(): ReelSound? {
    if (status.trim().lowercase() !in USABLE_STATUS) return null
    return ReelSoundWire(
        id = id,
        title = title,
        artist = artist,
        durationMs = durationMs,
        usageCount = usageCount,
        sourcePostId = sourcePostId,
        sourceReelId = sourceReelId,
        creatorUserId = creatorUserId,
    ).toReelSound()
}

/** One page of the reels that play a sound. */
data class SoundReelsPage(
    /** The sound itself; on every page. Null only when the answer is malformed. */
    val sound: ReelSound?,
    /** The reel the sound was taken from: first page only, and only when this viewer may read it. */
    val origin: FeedItem?,
    /** Never holds [origin]. */
    val items: List<FeedItem>,
    /** Null at the end; an empty cursor is the end too. */
    val nextCursor: String?,
)

/**
 * `GET v1/posts/by-sound/{id}` as a page: rows that are not reels are
 * dropped, so is a soft-deleted one, the origin is never repeated among the
 * items, and an empty cursor (a Go zero value) is the end like an absent one.
 */
internal fun SoundReelsDto.toPage(meta: ApiMeta?): SoundReelsPage {
    val originRow = origin?.toSoundReel()
    return SoundReelsPage(
        sound = sound?.toWire().toReelSound(),
        origin = originRow,
        items = items.mapNotNull { it.toSoundReel() }.filter { it.id != originRow?.id },
        nextCursor = meta?.nextCursor?.takeIf { it.isNotBlank() },
    )
}

/** A by-sound row as a reel, or null when it is not one: no id, deleted, not short-form, no video, too long. */
private fun FeedItemDto.toSoundReel(): FeedItem? {
    if (id.isBlank() || deletedAt.isNotBlank()) return null
    return toDomain().takeIf { it.isSoundReel() }
}

private fun FeedItem.isSoundReel(): Boolean =
    feedContentType.lowercase() in SHORT_FORM_TYPES &&
        hasVideo() &&
        playsInReels(feedContentType, media.maxOfOrNull { it.durationMs } ?: 0L)

/** One tile of the sound page's grid. */
data class SoundReelTile(val reel: FeedItem, val isOrigin: Boolean)

/** Every page's tiles in order, the origin first and marked, nothing twice. */
fun soundReelTiles(pages: List<SoundReelsPage>): List<SoundReelTile> {
    val seen = mutableSetOf<String>()
    val out = mutableListOf<SoundReelTile>()
    pages.firstOrNull()?.origin?.let { origin ->
        seen += origin.id
        out += SoundReelTile(origin, isOrigin = true)
    }
    pages.forEach { page ->
        page.items.forEach { reel -> if (seen.add(reel.id)) out += SoundReelTile(reel, isOrigin = false) }
    }
    return out
}

private const val ORIGINAL_SOUND = "Original sound"
private val SHORT_FORM_TYPES = setOf("flick", "reel", "short")
