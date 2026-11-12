package com.us.android.feature.echo.data

import java.time.Duration
import java.time.Instant
import java.time.format.DateTimeParseException

/**
 * Echo's limits, as the server enforces them (echo-contract.md "Rules
 * enforced by the server"). The client checks the same numbers first so the
 * Post button says why it is off; the server stays the authority.
 */
object EchoRules {
    /** Characters per note, counted as code points (the server counts runes). */
    const val MAX_CHARS = 500

    /** Images per note; a GIF is alone. */
    const val MAX_IMAGES = 4

    /** A thread is 2 to 25 notes. */
    const val THREAD_MIN = 2
    const val THREAD_MAX = 25

    /** The author may change a note's text for this many minutes. */
    const val EDIT_MINUTES = 30L

    /** The author may change a note's text for [EDIT_MINUTES]. */
    val EDIT_WINDOW: Duration = Duration.ofMinutes(EDIT_MINUTES)

    /** The remaining-characters counter turns to a warning at this many left. */
    const val WARN_AT_REMAINING = 20

    /** Code points after trimming, the way the server measures. */
    fun length(text: String): Int {
        val trimmed = text.trim()
        return trimmed.codePointCount(0, trimmed.length)
    }

    fun remaining(text: String): Int = MAX_CHARS - length(text)
}

/** One staged picture on a composer entry. [mediaId] is set once the upload is ready. */
data class StagedMedia(
    val localKey: String,
    val isGif: Boolean,
    val mediaId: String? = null,
    val failed: Boolean = false,
) {
    val uploading: Boolean get() = mediaId == null && !failed
}

/** Why an entry cannot be posted yet, or null when it can. Pure, so the words are tests. */
fun entryProblem(text: String, media: List<StagedMedia>): String? {
    val length = EchoRules.length(text)
    return when {
        length > EchoRules.MAX_CHARS -> "Notes can be up to ${EchoRules.MAX_CHARS} characters."
        length == 0 && media.isEmpty() -> "Write something or add a photo."
        media.any { it.failed } -> "A photo didn't upload. Remove it and try again."
        media.any { it.uploading } -> "Your photos are still uploading."
        mediaProblem(media) != null -> mediaProblem(media)
        else -> null
    }
}

/** The media rule: up to 4 images, or exactly 1 GIF on its own. */
fun mediaProblem(media: List<StagedMedia>): String? = when {
    media.count { it.isGif } > 1 || (media.any { it.isGif } && media.size > 1) -> MEDIA_LIMIT_MESSAGE
    media.size > EchoRules.MAX_IMAGES -> MEDIA_LIMIT_MESSAGE
    else -> null
}

/**
 * How many of [incomingGifs] (true for a GIF, in pick order) may join
 * [current] without breaking the media rule. Picks past the limit are left
 * out, and the composer says so.
 */
fun acceptedPicks(current: List<StagedMedia>, incomingGifs: List<Boolean>): Int {
    var staged = current.map { it.isGif }
    var accepted = 0
    for (gif in incomingGifs) {
        val next = staged + gif
        val ok = if (next.any { it }) next.size == 1 else next.size <= EchoRules.MAX_IMAGES
        if (!ok) break
        staged = next
        accepted++
    }
    return accepted
}

const val MEDIA_LIMIT_MESSAGE = "Add up to 4 photos or 1 GIF."

/**
 * Whether [viewerId] may still change [note]'s text at [now]: the author,
 * within 30 minutes of creation. A note whose time does not parse is never
 * editable here; the server is the authority either way.
 */
fun canEditText(note: Note, viewerId: String, now: Instant): Boolean {
    if (viewerId.isBlank() || note.authorId != viewerId) return false
    val created = parseInstant(note.createdAt) ?: return false
    return !now.isBefore(created) && Duration.between(created, now) < EchoRules.EDIT_WINDOW
}

/** Only the author deletes, or changes who can reply. */
fun isAuthor(note: Note, viewerId: String): Boolean = viewerId.isNotBlank() && note.authorId == viewerId

fun parseInstant(value: String?): Instant? = try {
    value?.takeIf { it.isNotBlank() }?.let(Instant::parse)
} catch (_: DateTimeParseException) {
    null
}

/** "now", "5m", "3h", "2d", then the date: the timeline's short time. */
fun shortAge(createdAt: String, now: Instant): String {
    val created = parseInstant(createdAt) ?: return ""
    val age = Duration.between(created, now)
    return when {
        age.isNegative || age.toMinutes() < 1 -> "now"
        age.toHours() < 1 -> "${age.toMinutes()}m"
        age.toDays() < 1 -> "${age.toHours()}h"
        age.toDays() < WEEK_DAYS -> "${age.toDays()}d"
        else -> created.toString().substring(0, DATE_LENGTH)
    }
}

/** "1.2K", "3M": the counts under a note. Zero shows nothing. */
fun compactCount(count: Long): String = when {
    count <= 0 -> ""
    count < THOUSAND -> count.toString()
    count < MILLION -> oneDecimal(count, THOUSAND) + "K"
    else -> oneDecimal(count, MILLION) + "M"
}

private fun oneDecimal(count: Long, unit: Long): String {
    val tenths = count * TEN / unit
    return if (tenths % TEN == 0L) "${tenths / TEN}" else "${tenths / TEN}.${tenths % TEN}"
}

private const val WEEK_DAYS = 7
private const val DATE_LENGTH = 10
private const val THOUSAND = 1_000L
private const val MILLION = 1_000_000L
private const val TEN = 10L
