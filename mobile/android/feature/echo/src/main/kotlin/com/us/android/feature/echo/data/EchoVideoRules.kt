package com.us.android.feature.echo.data

/**
 * Video in notes (echo-v2-contract.md "Video in notes", 15 Nov 2026): one
 * video on its own, its limits decided by the poster. Without Premium (a
 * reply by someone whose eligibility says `can_post: false`) 140 seconds
 * and 512 MB; with Premium 10 minutes and the platform's own upload cap.
 * The composer checks the local file first; the server stays the authority
 * (`400 VIDEO_TOO_LONG`, `400 VIDEO_TOO_LARGE`).
 */
object EchoVideoRules {
    /** 2 min 20 s: the longest video a poster without Premium may add. */
    const val SHORT_MAX_MS = 140_000L

    /** 10 minutes: the longest video with Premium. */
    const val PREMIUM_MAX_MS = 600_000L

    /** 512 MB: the largest file a poster without Premium may add. */
    const val SHORT_MAX_BYTES = 512L * 1024 * 1024

    const val SHORT_MAX_WORDS = "2 min 20 s"
    const val PREMIUM_MAX_WORDS = "10 minutes"
    const val SHORT_SIZE_WORDS = "512 MB"
}

/** How long and how large a video the poster may add; [maxBytes] null means the platform's own cap. */
data class VideoLimit(val maxMs: Long, val maxBytes: Long?, val lengthWords: String) {
    /**
     * Why a video of [durationMs] and [sizeBytes] (from the local file) may
     * not be added, or null when it may. An unknown length (0 or less) is
     * left to the server.
     */
    fun check(durationMs: Long, sizeBytes: Long): String? = when {
        durationMs > maxMs -> "Videos can be up to $lengthWords."
        maxBytes != null && sizeBytes > maxBytes -> "Videos can be up to ${EchoVideoRules.SHORT_SIZE_WORDS}."
        else -> null
    }
}

/** The limit for a poster: [premium] is `can_post` (every new note, thread and quote; a reply by a member). */
fun videoLimitFor(premium: Boolean): VideoLimit = if (premium) {
    VideoLimit(EchoVideoRules.PREMIUM_MAX_MS, maxBytes = null, lengthWords = EchoVideoRules.PREMIUM_MAX_WORDS)
} else {
    VideoLimit(
        EchoVideoRules.SHORT_MAX_MS,
        maxBytes = EchoVideoRules.SHORT_MAX_BYTES,
        lengthWords = EchoVideoRules.SHORT_MAX_WORDS,
    )
}

/**
 * The author's notice under a video that is longer than its note allows
 * (`video_too_long`). The limit is read from the length: past 10 minutes
 * it can only be the Premium limit, otherwise the 2 min 20 s one.
 */
fun videoTooLongNotice(durationMs: Long): String {
    val limit = if (durationMs > EchoVideoRules.PREMIUM_MAX_MS) {
        EchoVideoRules.PREMIUM_MAX_WORDS
    } else {
        EchoVideoRules.SHORT_MAX_WORDS
    }
    return "This video is longer than $limit and is only visible to you."
}

/** "0:07", "2:20", "1:02:03": a video's length chip. Empty while the length is unknown. */
fun videoDurationLabel(durationMs: Long): String {
    if (durationMs <= 0) return ""
    val total = durationMs / MS_PER_SECOND
    val hours = total / SECONDS_PER_HOUR
    val minutes = (total % SECONDS_PER_HOUR) / SECONDS_PER_MINUTE
    val seconds = total % SECONDS_PER_MINUTE
    return if (hours > 0) {
        "$hours:${twoDigits(minutes)}:${twoDigits(seconds)}"
    } else {
        "$minutes:${twoDigits(seconds)}"
    }
}

private fun twoDigits(n: Long): String = n.toString().padStart(2, '0')

const val VIDEO_UNREADABLE = "That video couldn't be read."
const val VIDEO_TOO_LARGE_TO_UPLOAD = "This video is too large."
const val VIDEO_UPLOAD_FAILED = "The video didn't upload. Try again."
const val VIDEO_ALONE_MESSAGE = "A video goes on its own, without photos, a GIF or a poll."

private const val MS_PER_SECOND = 1_000L
private const val SECONDS_PER_MINUTE = 60L
private const val SECONDS_PER_HOUR = 3_600L
