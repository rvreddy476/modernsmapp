package com.us.android.feature.commerce.seller

import com.us.android.core.common.error.AppError

/*
 * What a seller reads when media-service refuses an upload (audit M4, M5 and
 * the 31 Oct private-document addendum).
 *
 * Every init failure used to say "The upload could not start.", which reads
 * as a network fault, so sellers retried, and every retry spent the same
 * upload budget the refusal was about.
 */

internal const val UPLOAD_SIGNED_OUT = "You've been signed out. Sign in again to upload."
internal const val UPLOAD_OFFLINE = "Check your connection and try again."

/** A KYC document: `file_type=document`, PDF/JPEG/PNG, 10 MiB, 20 a day. */
internal fun documentUploadCopy(error: AppError, step: UploadStep, fallback: String): String = when {
    error is AppError.RateLimited ->
        "You've sent as many documents as we accept in a day. Try again ${waitFor(error.retryAfterSeconds)}."
    error.isCode(HTTP_UNSUPPORTED_MEDIA_TYPE, CODE_UNSUPPORTED_MEDIA_TYPE) -> DOCUMENT_WRONG_TYPE
    error.isCode(HTTP_PAYLOAD_TOO_LARGE, CODE_FILE_TOO_LARGE) -> DOCUMENT_TOO_LARGE
    // The confirm sniffs the bytes: a file renamed to .pdf is refused here.
    step == UploadStep.Confirm && error.hasCode(CODE_UPLOAD_INTEGRITY_ERROR) -> DOCUMENT_NOT_WHAT_IT_SAYS
    // The same upload confirmed twice, or after it was refused: only a
    // fresh pick can go on.
    step == UploadStep.Confirm && error.hasCode(CODE_UPLOAD_STATE_CONFLICT) -> DOCUMENT_PICK_AGAIN
    else -> common(error) ?: fallback
}

/** A product photo: the general image budget, counted per hour. */
internal fun productImageUploadCopy(error: AppError, fallback: String): String = when {
    error is AppError.RateLimited ->
        "You've uploaded a lot in the last hour. Try again ${waitFor(error.retryAfterSeconds)}."
    error.isCode(HTTP_UNSUPPORTED_MEDIA_TYPE, CODE_UNSUPPORTED_MEDIA_TYPE) ->
        "Product photos must be JPEG, PNG or WebP."
    error.isCode(HTTP_PAYLOAD_TOO_LARGE, CODE_FILE_TOO_LARGE) -> "That photo is too large."
    else -> common(error) ?: fallback
}

private fun common(error: AppError): String? = when (error) {
    is AppError.AuthFailed -> UPLOAD_SIGNED_OUT
    is AppError.NoNetwork, is AppError.Timeout -> UPLOAD_OFFLINE
    else -> null
}

/** "in 5 minutes", "in 2 hours", "tomorrow": from the server's Retry-After. */
internal fun waitFor(retryAfterSeconds: Long?): String {
    val seconds = retryAfterSeconds?.takeIf { it > 0 } ?: return "later"
    return when {
        seconds <= SECONDS_PER_MINUTE -> "in a minute"
        seconds < SECONDS_PER_HOUR -> "in ${ceilDiv(seconds, SECONDS_PER_MINUTE)} minutes"
        seconds < TOMORROW_SECONDS -> hours(ceilDiv(seconds, SECONDS_PER_HOUR))
        else -> "tomorrow"
    }
}

private fun ceilDiv(a: Long, b: Long): Long = (a + b - 1) / b

private fun hours(n: Long): String = if (n == 1L) "in an hour" else "in $n hours"

/**
 * The server's code, or, when a proxy answered with no envelope, the status
 * alone (a gateway's own 413 carries no code).
 */
private fun AppError.isCode(status: Int, code: String): Boolean =
    this is AppError.Unknown && (this.code == code || (this.code.isNullOrBlank() && statusCode == status))

/** Only the code: one status (409) carries several meanings. */
private fun AppError.hasCode(code: String): Boolean = this is AppError.Unknown && this.code == code

internal const val DOCUMENT_WRONG_TYPE = "Send a PDF, JPEG or PNG."
internal const val DOCUMENT_TOO_LARGE = "That file is larger than 10 MB."
internal const val DOCUMENT_NOT_WHAT_IT_SAYS =
    "That file isn't the kind of file its name says. Save it again as a PDF, JPEG or PNG and send that."
internal const val DOCUMENT_PICK_AGAIN = "That upload can't be used any more. Please choose the file again."

private const val HTTP_PAYLOAD_TOO_LARGE = 413
private const val HTTP_UNSUPPORTED_MEDIA_TYPE = 415
private const val CODE_UNSUPPORTED_MEDIA_TYPE = "UNSUPPORTED_MEDIA_TYPE"
private const val CODE_FILE_TOO_LARGE = "FILE_TOO_LARGE"
private const val CODE_UPLOAD_INTEGRITY_ERROR = "UPLOAD_INTEGRITY_ERROR"
private const val CODE_UPLOAD_STATE_CONFLICT = "UPLOAD_STATE_CONFLICT"
private const val SECONDS_PER_MINUTE = 60L
private const val SECONDS_PER_HOUR = 3_600L

/** Twelve hours or more: say "tomorrow", not "in 24 hours". */
private const val TOMORROW_SECONDS = 12 * SECONDS_PER_HOUR
