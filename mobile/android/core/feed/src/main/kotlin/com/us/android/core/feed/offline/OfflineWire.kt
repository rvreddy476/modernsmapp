package com.us.android.core.feed.offline

import java.time.Instant
import java.time.OffsetDateTime
import java.time.format.DateTimeParseException

/*
 * The ONE place the offline routes' wire shapes become the app's types
 * (2026-10-02). post-service is Go: it sends `""`, `0` and `null` where it
 * has nothing to say, and may omit a key altogether. All four read as "not
 * said" here, and each has a stated fallback, so no caller ever branches on
 * a wire value.
 */

/** What the server granted: everything needed to store one copy. */
data class OfflineGrant(
    val postId: String,
    /** Null when the server did not say; the caller then knows the kind from the post it asked about. */
    val kind: OfflineKind?,
    val expiresAtMs: Long,
    val recheckAfterSeconds: Long,
    val title: String,
    val channelName: String,
    val durationMs: Long,
    /** Absolute, or null when there is no poster. */
    val posterUrl: String?,
    val video: OfflineGrantStream,
    val captions: List<OfflineGrantCaption>,
    val sound: OfflineGrantSound?,
)

data class OfflineGrantStream(val url: String, val mime: String, val sizeBytes: Long)

data class OfflineGrantCaption(val language: String, val label: String, val url: String)

data class OfflineGrantSound(
    val stream: OfflineGrantStream,
    val startMs: Long,
    val originalVolume: Double,
    val overlayVolume: Double,
)

/** One post's answer from a check. */
sealed interface OfflineCheckAnswer {
    /**
     * The copy may be kept. [expiresAtMs] is null when the server did not
     * repeat the expiry. [renewable] is false only when the server said so:
     * a grant repeated now would be refused, so it is not tried.
     */
    data class Valid(val expiresAtMs: Long?, val renewable: Boolean = true) : OfflineCheckAnswer

    /** The copy must go. [reason] is the server's token, kept for the notice. */
    data class Invalid(val reason: String) : OfflineCheckAnswer
}

/**
 * The grant as the app stores it, or null when it grants nothing storable:
 * no post id, or no rendition path (a list row has none, by contract).
 *
 * [resolve] turns a gateway-relative path into an absolute address, or null
 * for a blank one. [nowMs] is the device's clock, for the two fallbacks:
 * an expiry the server did not send is [DEFAULT_LIFETIME_MS] from now (the
 * contract's thirty days, never "forever"), and a recheck interval it did
 * not send is [DEFAULT_RECHECK_SECONDS].
 */
internal fun OfflineGrantDto.toGrant(resolve: (String) -> String?, nowMs: Long): OfflineGrant? {
    val id = postId.trim()
    val videoUrl = media?.path?.trim()?.takeIf { it.isNotEmpty() }?.let(resolve)
    if (id.isEmpty() || videoUrl == null) return null
    return OfflineGrant(
        postId = id,
        kind = offlineKindOf(contentType),
        expiresAtMs = parseInstantMs(expiresAt) ?: (nowMs + DEFAULT_LIFETIME_MS),
        recheckAfterSeconds = recheckAfterSeconds.takeIf { it > 0L } ?: DEFAULT_RECHECK_SECONDS,
        title = title.trim(),
        channelName = channelName.trim(),
        durationMs = durationMs.coerceAtLeast(0L),
        posterUrl = posterPath.trim().takeIf { it.isNotEmpty() }?.let(resolve),
        video = OfflineGrantStream(
            url = videoUrl,
            mime = media?.mime.orEmpty().trim(),
            sizeBytes = media?.sizeBytes?.coerceAtLeast(0L) ?: 0L,
        ),
        captions = captions.orEmpty().mapNotNull { it.toCaption(resolve) }.distinctBy { it.language },
        sound = sound?.toSound(resolve),
    )
}

private fun OfflineCaptionDto.toCaption(resolve: (String) -> String?): OfflineGrantCaption? {
    val language = lang.trim()
    val url = path.trim().takeIf { it.isNotEmpty() }?.let(resolve)
    if (language.isEmpty() || url == null) return null
    return OfflineGrantCaption(language = language, label = label.trim().ifEmpty { language }, url = url)
}

/**
 * A sound with no path is no sound. A volume the server did not send is
 * full; one it sent as 0 is the creator's mute and stays 0 (the same rule
 * as a reel's own levels).
 */
private fun OfflineSoundDto.toSound(resolve: (String) -> String?): OfflineGrantSound? {
    val url = path.trim().takeIf { it.isNotEmpty() }?.let(resolve) ?: return null
    return OfflineGrantSound(
        stream = OfflineGrantStream(url = url, mime = mime.trim(), sizeBytes = sizeBytes.coerceAtLeast(0L)),
        startMs = startMs.coerceAtLeast(0L),
        originalVolume = (originalVolume ?: FULL_VOLUME).coerceIn(0.0, FULL_VOLUME),
        overlayVolume = (overlayVolume ?: FULL_VOLUME).coerceIn(0.0, FULL_VOLUME),
    )
}

/**
 * The check's answers by post id. A row with no post id is dropped; a post
 * the server did not answer for is simply absent, which the caller reads as
 * "no answer", never as "revoked".
 */
internal fun List<OfflineCheckDto>.toAnswers(): Map<String, OfflineCheckAnswer> =
    filter { it.postId.isNotBlank() }.associate { row ->
        row.postId.trim() to if (row.valid) {
            // Absent is "try": only an explicit `false` holds a renewal back.
            OfflineCheckAnswer.Valid(parseInstantMs(row.expiresAt), renewable = row.renewable != false)
        } else {
            OfflineCheckAnswer.Invalid(row.reason.trim().ifEmpty { REASON_UNKNOWN })
        }
    }

/** The post ids of the copies the server holds for this device. */
internal fun List<OfflineGrantDto>.postIds(): List<String> =
    map { it.postId.trim() }.filter { it.isNotEmpty() }.distinct()

/** `flick`, `reel` and `short` are a reel; `long_video` and `video` a video; anything else is not said. */
internal fun offlineKindOf(contentType: String): OfflineKind? = when (contentType.trim().lowercase()) {
    "flick", "reel", "short" -> OfflineKind.REEL
    "long_video", "video" -> OfflineKind.VIDEO
    else -> null
}

/** RFC 3339 as epoch milliseconds, with `Z` or an offset; null for blank or anything else. */
internal fun parseInstantMs(value: String): Long? {
    val text = value.trim()
    if (text.isEmpty()) return null
    return try {
        Instant.parse(text).toEpochMilli()
    } catch (_: DateTimeParseException) {
        try {
            OffsetDateTime.parse(text).toInstant().toEpochMilli()
        } catch (_: DateTimeParseException) {
            null
        }
    }
}

/** The contract's thirty days. */
internal const val DEFAULT_LIFETIME_MS = 30L * 24 * 60 * 60 * 1000

/** The contract's two days. */
internal const val DEFAULT_RECHECK_SECONDS = 172_800L

internal const val REASON_UNKNOWN = "unknown"
internal const val REASON_EXPIRED = "expired"
private const val FULL_VOLUME = 1.0
