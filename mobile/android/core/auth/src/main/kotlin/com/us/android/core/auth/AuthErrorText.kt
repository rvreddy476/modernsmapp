package com.us.android.core.auth

import com.us.android.core.common.error.AppError

/**
 * Turns backend failures into sentences a person can act on.
 *
 * Three rules, learned from what the raw responses actually look like:
 *
 * 1. **Never show the server's own wording.** It is written for API consumers
 *    and sometimes for auditors — "you must be at least 18 years old to
 *    create an account. This platform has no verifiable parental-consent
 *    flow, so it cannot lawfully process the data of a user under 18" is
 *    accurate and unusable in a form.
 * 2. **Say what to do next**, not just what went wrong. "Authentication
 *    failed" tells the user nothing they can act on.
 * 3. **Never leak internals.** No status codes, no request ids, no service
 *    names in user-facing text — those go to logs, keyed by `meta.request_id`.
 */
object AuthErrorText {

    /** Message for an error with no specific field to attach it to. */
    fun forLogin(error: AppError): String = when (error) {
        is AppError.AuthFailed -> "That email or password isn't right. Please try again."
        is AppError.NoNetwork -> "You're offline. Check your connection and try again."
        is AppError.Timeout -> "That took too long. Check your connection and try again."
        is AppError.RateLimited ->
            error.retryAfterSeconds
                ?.let { "Too many attempts. Try again in ${it.humanSeconds()}." }
                ?: "Too many attempts. Wait a moment and try again."
        is AppError.Forbidden -> forbiddenText(error.code)
        is AppError.InvalidRequest -> "Check your details and try again."
        is AppError.Server -> "Something went wrong on our end. Please try again shortly."
        is AppError.NotFound -> "We couldn't find that account."
        is AppError.Malformed -> "We got an unexpected response. Please try again."
        is AppError.Unknown -> "Something went wrong. Please try again."
    }

    fun forRegister(error: AppError): String = when (error) {
        is AppError.NoNetwork -> "You're offline. Check your connection and try again."
        is AppError.Timeout -> "That took too long. Check your connection and try again."
        is AppError.RateLimited -> "Too many attempts. Wait a moment and try again."
        is AppError.Server -> "Something went wrong on our end. Please try again shortly."
        is AppError.Forbidden -> forbiddenText(error.code)
        is AppError.Unknown -> error.code?.let(::registrationCodeText)
            ?: "We couldn't create your account. Please try again."
        is AppError.InvalidRequest -> "Some details need fixing — see the fields above."
        else -> "We couldn't create your account. Please try again."
    }

    /**
     * Field-level text for a registration rejection.
     *
     * Returns null when the code has no obvious home, so the caller can fall
     * back to a message rather than pinning it on the wrong input.
     */
    fun registrationCodeText(code: String): String? = when (code) {
        "EMAIL_REQUIRED" -> "Enter your email address."
        "USER_EXISTS" -> "That email is already registered. Try signing in instead."
        "FIRST_NAME_REQUIRED" -> "Enter your first name."
        "LAST_NAME_REQUIRED" -> "Enter your last name."
        "NAME_INVALID" ->
            "Names can use letters, spaces, hyphens and apostrophes — no numbers or symbols."
        "WEAK_PASSWORD" -> "Pick a stronger password — longer, with a mix of characters."
        "INVALID_DOB" -> "Choose a valid date of birth."
        "UNDERAGE" -> "You need to be ${RegistrationRules.MINIMUM_AGE_YEARS} or older to sign up."
        "GENDER_REQUIRED" -> "Select your gender."
        // Reachable if the client's vocabulary ever drifts from the server's
        // closed set. The server returns the allowed values in
        // `error.details.allowed`, so this is diagnosable from a bug report.
        "GENDER_INVALID" -> "Select one of the available options."
        "CONSENT_REQUIRED" -> "Please accept the terms to continue."
        "REGISTRATION_FAILED" -> "We couldn't create your account. Please try again."
        else -> null
    }

    private fun forbiddenText(code: String): String = when (code) {
        ErrorMapperCodes.EMAIL_NOT_VERIFIED ->
            "Your email isn't verified yet. Check your inbox to finish setting up."
        "STEP_UP_UNAVAILABLE" ->
            "This sign-in looks unusual and your account has no recovery option set up. " +
                "Please contact support."
        else -> "You don't have access to that right now."
    }

    private fun Long.humanSeconds(): String =
        if (this < SECONDS_PER_MINUTE) "$this seconds" else "${this / SECONDS_PER_MINUTE} minutes"

    private const val SECONDS_PER_MINUTE = 60
}

/** Codes referenced here but owned by `:core:network`'s ErrorMapper. */
internal object ErrorMapperCodes {
    const val EMAIL_NOT_VERIFIED = "EMAIL_NOT_VERIFIED"
}
