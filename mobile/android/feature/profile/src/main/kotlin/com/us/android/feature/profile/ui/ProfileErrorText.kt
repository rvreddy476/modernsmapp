package com.us.android.feature.profile.ui

import com.us.android.core.common.error.AppError

/**
 * Turns a typed [AppError] into something a person can act on.
 *
 * Kept beside the screen rather than in `:core:common` because the right words
 * depend on what the user was doing: the same `NotFound` means "this profile
 * doesn't exist" here and "that post was deleted" on a post screen. A single
 * app-wide error-string table produces text that fits nowhere.
 *
 * What IS shared is the classification — [AppError] itself, and the retryable
 * decision below — so no screen has to re-derive which failures are worth a
 * retry button.
 */
internal object ProfileErrorText {

    fun forLoad(error: AppError): String = when (error) {
        is AppError.NoNetwork -> "You're offline. Check your connection and try again."
        is AppError.Timeout -> "That took too long. Try again."
        is AppError.NotFound -> "This profile isn't available."
        is AppError.AuthFailed -> "Please sign in again to see this."
        is AppError.Server -> "Something went wrong on our end. Try again shortly."
        else -> "We couldn't load this profile."
    }

    fun forRelationshipAction(error: AppError): String = when (error) {
        is AppError.NoNetwork -> "You're offline. That didn't go through."
        is AppError.Timeout -> "That took too long. Try again."
        is AppError.AuthFailed -> "Please sign in again."
        is AppError.RateLimited -> "You're doing that too quickly. Wait a moment."
        else -> "That didn't go through. Try again."
    }

    /**
     * Whether re-running the same request could plausibly succeed.
     *
     * `NotFound` and `Forbidden` are excluded deliberately — retrying either
     * produces the identical response, and a button that always fails teaches
     * users to distrust every button.
     */
    fun isRetryable(error: AppError): Boolean = when (error) {
        is AppError.NotFound,
        is AppError.Forbidden,
        is AppError.InvalidRequest,
        -> false
        else -> true
    }
}
