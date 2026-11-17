package com.us.android.core.media.upload

import com.us.android.core.common.error.AppError

/*
 * media-service's upload refusals (lane media-upload, 2026-11-17), transcribed
 * from `internal/http/upload_limit_errors.go` and `resumable_handler.go`:
 *
 *   413 VIDEO_TOO_LONG              details.max_duration_seconds; final for this file
 *   429 UPLOAD_QUOTA_EXCEEDED       details.limit/max/used/requested; Retry-After 3600
 *   429 LONG_VIDEO_IN_PROGRESS      details.limit/max/used;           Retry-After 600
 *   503 UPLOAD_LIMITER_UNAVAILABLE  Retry-After 30; transient, nothing charged
 *   410 UPLOAD_EXPIRED              the resumable session is gone; start again
 *   409 UPLOAD_INCOMPLETE           complete before every part landed
 *   429 RATE_LIMITED                the per-hour count of inits; Retry-After 3600
 */
const val CODE_VIDEO_TOO_LONG = "VIDEO_TOO_LONG"
const val CODE_UPLOAD_QUOTA_EXCEEDED = "UPLOAD_QUOTA_EXCEEDED"
const val CODE_LONG_VIDEO_IN_PROGRESS = "LONG_VIDEO_IN_PROGRESS"
const val CODE_UPLOAD_LIMITER_UNAVAILABLE = "UPLOAD_LIMITER_UNAVAILABLE"
const val CODE_UPLOAD_EXPIRED = "UPLOAD_EXPIRED"
const val CODE_UPLOAD_INCOMPLETE = "UPLOAD_INCOMPLETE"
const val CODE_RATE_LIMITED = "RATE_LIMITED"

/** `details.max_duration_seconds` of a [CODE_VIDEO_TOO_LONG]. */
const val DETAIL_MAX_DURATION_SECONDS = "max_duration_seconds"

/**
 * The `Retry-After` header, folded into [AppError.Unknown.details] under this
 * key by [uploadCall]. Client-side only: the server never sends a field of
 * this name.
 */
const val DETAIL_RETRY_AFTER_SECONDS = "retry_after_seconds"

/**
 * One upload refusal, and the sentence the upload tile shows for it.
 *
 * [serverMessage] is media-service's own sentence (every one of these codes
 * carries a message "a client can show as is"); when it is missing the client
 * says the same thing in its own words. [retryAfterSeconds] turns into "Try
 * again in about an hour" — never a bare "failed".
 */
data class UploadRefusal(
    val code: String,
    val serverMessage: String? = null,
    val details: Map<String, String> = emptyMap(),
    val retryAfterSeconds: Long? = null,
) {
    /** The same file is refused again whatever the creator waits for: no Retry. */
    val final: Boolean get() = code == CODE_VIDEO_TOO_LONG

    val message: String
        get() = when (code) {
            CODE_VIDEO_TOO_LONG -> serverMessage ?: videoTooLongFallback()
            CODE_UPLOAD_QUOTA_EXCEEDED -> withRetry(serverMessage ?: "You've reached today's video upload limit.")
            CODE_LONG_VIDEO_IN_PROGRESS -> withRetry(serverMessage ?: LONG_IN_PROGRESS_FALLBACK)
            CODE_UPLOAD_LIMITER_UNAVAILABLE -> "Uploads are paused for a moment. Try again in a minute."
            CODE_UPLOAD_EXPIRED -> "The upload took too long and expired. Start it again."
            CODE_UPLOAD_INCOMPLETE -> "Part of the upload is missing. Tap Retry to send the rest."
            CODE_RATE_LIMITED -> withRetry("You've started too many uploads for now.")
            else -> serverMessage ?: "The server wouldn't take this upload."
        }

    private fun videoTooLongFallback(): String {
        val max = details[DETAIL_MAX_DURATION_SECONDS]?.toDoubleOrNull()?.toLong()?.takeIf { it > 0L }
            ?: return "This video is too long to upload. Trim it or split it into parts."
        return "This video is longer than ${formatDuration(max)}, the longest we can process. " +
            "Trim it or split it into parts."
    }

    private fun withRetry(sentence: String): String {
        val wait = retryAfterSeconds?.takeIf { it > 0L } ?: return sentence
        return "$sentence You can try again in ${formatWait(wait)}."
    }
}

/** The upload refusal in [this], when it is one of the codes above. */
fun AppError.uploadRefusal(): UploadRefusal? = when (this) {
    is AppError.Unknown -> code?.takeIf { it in UPLOAD_REFUSAL_CODES }?.let {
        UploadRefusal(
            code = it,
            serverMessage = message?.takeIf { text -> text.isNotBlank() },
            details = details - DETAIL_RETRY_AFTER_SECONDS,
            retryAfterSeconds = details[DETAIL_RETRY_AFTER_SECONDS]?.toLongOrNull(),
        )
    }
    // The limiter's 503 stays a Server error so the uploaders still retry it.
    is AppError.Server ->
        UploadRefusal(CODE_UPLOAD_LIMITER_UNAVAILABLE).takeIf { code == CODE_UPLOAD_LIMITER_UNAVAILABLE }
    else -> null
}

private const val LONG_IN_PROGRESS_FALLBACK =
    "You already have long videos processing. Upload this one when one of them is finished."

private val UPLOAD_REFUSAL_CODES = setOf(
    CODE_VIDEO_TOO_LONG,
    CODE_UPLOAD_QUOTA_EXCEEDED,
    CODE_LONG_VIDEO_IN_PROGRESS,
    CODE_UPLOAD_EXPIRED,
    CODE_UPLOAD_INCOMPLETE,
    CODE_RATE_LIMITED,
)

/** The codes that are a wait, not a blip: never retried in place. */
private val WAIT_CODES = setOf(CODE_UPLOAD_QUOTA_EXCEEDED, CODE_LONG_VIDEO_IN_PROGRESS)

/** A 429 asking for longer than this is not worth retrying inside one run. */
private const val LONGEST_INLINE_WAIT_SECONDS = 60L

/** What [uploadCall] peeked off a refused response. */
internal data class RawUploadRefusal(
    val status: Int,
    val code: String?,
    val message: String?,
    val details: Map<String, String>,
    val retryAfterSeconds: Long?,
) {
    /**
     * [mapped] as [ErrorMapper] made it, with what it dropped put back:
     *  - an [AppError.Unknown] keeps its details and the Retry-After;
     *  - a 429 that is a quota, a long video in progress, or a wait over a
     *    minute becomes an [AppError.Unknown] with its code, so it is shown
     *    rather than retried;
     *  - anything else is left exactly as mapped.
     */
    fun reshape(mapped: AppError): AppError {
        val withRetry = retryAfterSeconds?.let { details + (DETAIL_RETRY_AFTER_SECONDS to it.toString()) } ?: details
        return when {
            mapped is AppError.Unknown -> if (withRetry.isEmpty()) mapped else mapped.copy(details = withRetry)
            mapped is AppError.RateLimited && isAWait() -> AppError.Unknown(
                code = code ?: CODE_RATE_LIMITED,
                statusCode = status,
                requestId = mapped.requestId,
                message = message,
                details = withRetry,
            )
            else -> mapped
        }
    }

    private fun isAWait(): Boolean =
        code in WAIT_CODES || (retryAfterSeconds ?: 0L) > LONGEST_INLINE_WAIT_SECONDS
}

/** "about an hour", "about 10 minutes", "a minute": the wait in words. */
internal fun formatWait(seconds: Long): String = when {
    seconds <= SECONDS_PER_MINUTE + SECONDS_PER_MINUTE / 2 -> "a minute"
    seconds < SECONDS_PER_HOUR -> "about ${ceilDiv(seconds, SECONDS_PER_MINUTE)} minutes"
    seconds < SECONDS_PER_HOUR + SECONDS_PER_HOUR / 2 -> "about an hour"
    else -> "about ${ceilDiv(seconds, SECONDS_PER_HOUR)} hours"
}

/** A length cap in words, as the server writes it: "4 hours", "1 h 30 min", "10 minutes". */
internal fun formatDuration(seconds: Long): String {
    val hours = seconds / SECONDS_PER_HOUR
    val minutes = (seconds % SECONDS_PER_HOUR) / SECONDS_PER_MINUTE
    return when {
        hours > 0L && minutes > 0L -> "$hours h $minutes min"
        hours == 1L -> "1 hour"
        hours > 0L -> "$hours hours"
        else -> "$minutes minutes"
    }
}

private fun ceilDiv(value: Long, by: Long): Long = (value + by - 1L) / by

private const val SECONDS_PER_MINUTE = 60L
private const val SECONDS_PER_HOUR = 3_600L

internal const val HTTP_CONFLICT = 409
internal const val HTTP_GONE = 410
internal const val HTTP_TOO_MANY_REQUESTS = 429
internal const val HTTP_SERVICE_UNAVAILABLE = 503
