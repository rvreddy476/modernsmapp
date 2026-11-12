package com.us.android.feature.echo.data

import com.us.android.core.common.error.AppError

/**
 * Echo's error codes (echo-contract.md) and what each one says. Keyed on the
 * server's `error.code`, never its message. The same sentences the web uses.
 */
object EchoErrorCodes {
    const val PREMIUM_REQUIRED = "PREMIUM_REQUIRED"
    const val ACCOUNT_TOO_NEW = "ACCOUNT_TOO_NEW"
    const val REPLY_NOT_ALLOWED = "REPLY_NOT_ALLOWED"
    const val NOTE_TOO_LONG = "NOTE_TOO_LONG"
    const val EMPTY_NOTE = "EMPTY_NOTE"
    const val TOO_MANY_MEDIA = "TOO_MANY_MEDIA"
    const val MEDIA_NOT_ALLOWED = "MEDIA_NOT_ALLOWED"
    const val NOTE_NOT_FOUND = "NOTE_NOT_FOUND"
    const val EDIT_WINDOW_CLOSED = "EDIT_WINDOW_CLOSED"
    const val NOT_AUTHOR = "NOT_AUTHOR"
    const val RATE_LIMITED = "RATE_LIMITED"
    const val ECHO_NOT_ENABLED = "ECHO_NOT_ENABLED"
    const val PREMIUM_CHECK_UNAVAILABLE = "PREMIUM_CHECK_UNAVAILABLE"
    const val ACCOUNT_CHECK_UNAVAILABLE = "ACCOUNT_CHECK_UNAVAILABLE"
    const val INVALID_REQUEST = "INVALID_REQUEST"
    const val INVALID_THREAD = "INVALID_THREAD"
    const val MEDIA_NOT_OWNED = "MEDIA_NOT_OWNED"
    const val MEDIA_NOT_FOUND = "MEDIA_NOT_FOUND"
    const val MEDIA_NOT_READY = "MEDIA_NOT_READY"
}

/** The words for the eligibility sheet and the matching refusals. */
object EchoWords {
    const val PREMIUM_REQUIRED = "Posting on Echo needs Momentum Premium"
    const val PREMIUM_REQUIRED_DETAIL =
        "You can still read, like, repost and reply. Premium members can post notes, threads and quotes."
    const val ACCOUNT_TOO_NEW = "Your account is too new to reply on Echo"
    const val ACCOUNT_TOO_NEW_DETAIL = "Accounts can reply once they are 7 days old."
    const val SUSPENDED = "Your account can't post on Echo right now"
    const val SUSPENDED_DETAIL = "Your account isn't in good standing, so posting and replying are paused."
    const val REPLY_NOT_ALLOWED = "The author has limited who can reply to this note."
    const val NOTE_NOT_FOUND = "This note isn't available."
    const val GENERIC = "Something went wrong. Try again."
    const val NO_NETWORK = "No connection. Check your network and try again."
}

/** The server's code inside [error], whatever status carried it; null when there is none. */
fun echoCode(error: AppError): String? = when (error) {
    is AppError.Forbidden -> error.code
    is AppError.NotFound -> error.code
    is AppError.Server -> error.code
    is AppError.Unknown -> error.code
    is AppError.AuthFailed -> error.code
    is AppError.InvalidRequest -> EchoErrorCodes.INVALID_REQUEST
    is AppError.RateLimited -> EchoErrorCodes.RATE_LIMITED
    is AppError.NoNetwork, is AppError.Timeout, is AppError.Malformed -> null
}

/** One clear sentence for any failure an Echo call can come back with. */
@Suppress("CyclomaticComplexMethod") // One branch per contract code: a lookup table, not logic.
fun echoErrorMessage(error: AppError): String {
    if (error is AppError.NoNetwork || error is AppError.Timeout) return EchoWords.NO_NETWORK
    if (error is AppError.RateLimited) return rateLimitedMessage(error.retryAfterSeconds)
    return when (echoCode(error)) {
        EchoErrorCodes.PREMIUM_REQUIRED -> EchoWords.PREMIUM_REQUIRED
        EchoErrorCodes.ACCOUNT_TOO_NEW -> EchoWords.ACCOUNT_TOO_NEW_DETAIL
        EchoErrorCodes.REPLY_NOT_ALLOWED -> EchoWords.REPLY_NOT_ALLOWED
        EchoErrorCodes.NOTE_TOO_LONG -> "Notes can be up to ${EchoRules.MAX_CHARS} characters."
        EchoErrorCodes.EMPTY_NOTE -> "Write something or add a photo."
        EchoErrorCodes.TOO_MANY_MEDIA -> MEDIA_LIMIT_MESSAGE
        EchoErrorCodes.MEDIA_NOT_ALLOWED -> "Echo takes photos and GIFs only."
        EchoErrorCodes.NOTE_NOT_FOUND -> EchoWords.NOTE_NOT_FOUND
        EchoErrorCodes.EDIT_WINDOW_CLOSED -> "Notes can only be edited for 30 minutes after posting."
        EchoErrorCodes.NOT_AUTHOR -> "Only the author can change this note."
        EchoErrorCodes.ECHO_NOT_ENABLED -> "Echo isn't available right now."
        EchoErrorCodes.PREMIUM_CHECK_UNAVAILABLE -> "We couldn't check your Premium membership. Try again in a moment."
        EchoErrorCodes.ACCOUNT_CHECK_UNAVAILABLE -> "We couldn't check your account. Try again in a moment."
        EchoErrorCodes.INVALID_THREAD -> "A thread needs 2 to 25 notes, each within the limits."
        EchoErrorCodes.INVALID_REQUEST -> "Something in this note isn't right. Check it and try again."
        EchoErrorCodes.MEDIA_NOT_OWNED -> "One of these photos isn't yours to post."
        EchoErrorCodes.MEDIA_NOT_FOUND -> "One of these photos is missing. Remove it and add it again."
        EchoErrorCodes.MEDIA_NOT_READY -> "Your photos are still processing. Try again in a moment."
        EchoErrorCodes.RATE_LIMITED -> rateLimitedMessage(null)
        else -> if (error is AppError.NotFound) EchoWords.NOTE_NOT_FOUND else EchoWords.GENERIC
    }
}

/** "You're posting too fast. Try again in 2 minutes." — from `Retry-After` when the server sent one. */
fun rateLimitedMessage(retryAfterSeconds: Long?): String {
    val base = "You're posting too fast."
    val seconds = retryAfterSeconds?.takeIf { it > 0 } ?: return "$base Try again later."
    val wait = when {
        seconds < SECONDS_PER_MINUTE -> plural(seconds, "second")
        else -> plural((seconds + SECONDS_PER_MINUTE - 1) / SECONDS_PER_MINUTE, "minute")
    }
    return "$base Try again in $wait."
}

/** The eligibility sheet's title and detail for [reason]; null for a reason that blocks nothing. */
fun eligibilityWords(reason: EligibilityReason): Pair<String, String>? = when (reason) {
    EligibilityReason.PREMIUM_REQUIRED -> EchoWords.PREMIUM_REQUIRED to EchoWords.PREMIUM_REQUIRED_DETAIL
    EligibilityReason.ACCOUNT_TOO_NEW -> EchoWords.ACCOUNT_TOO_NEW to EchoWords.ACCOUNT_TOO_NEW_DETAIL
    EligibilityReason.SUSPENDED -> EchoWords.SUSPENDED to EchoWords.SUSPENDED_DETAIL
    EligibilityReason.NONE -> null
}

private fun plural(n: Long, unit: String) = if (n == 1L) "1 $unit" else "$n ${unit}s"

private const val SECONDS_PER_MINUTE = 60L
