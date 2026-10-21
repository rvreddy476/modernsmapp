package com.us.android.core.auth

import com.us.android.core.common.error.AppError
import com.us.android.core.network.ErrorMapper

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

    // ── Sign-in security (web parity, founder 21 Oct 2026) ──────────────

    /**
     * `POST /v1/auth/2fa/verify`. The server gives ONE code for every
     * verification failure — wrong, replayed, expired pending session, user
     * mismatch (twofa.go:124) — so this names the likely causes without
     * claiming to know which applies. A 429 from the login limiter is the
     * lockout: five tries per account per fifteen minutes.
     */
    fun forTwoFactor(error: AppError): String = when (error) {
        is AppError.AuthFailed ->
            "That code didn't work. Check your authenticator app and try again, " +
                "or sign in again if it has been a while."
        is AppError.RateLimited -> lockedOutText(error)
        else -> forLogin(error)
    }

    /**
     * `POST /v1/auth/anomaly/verify-*`. Here the 401 carries a specific code
     * (anomaly.go:76), so the sentence can say what to do next.
     */
    fun forStepUp(error: AppError): String = when (error) {
        is AppError.AuthFailed -> when (error.code) {
            STEP_UP_EXPIRED -> "This verification has expired. Please sign in again."
            STEP_UP_CODE_REPLAYED -> "That code has already been used. Wait for a new one and try again."
            ErrorMapper.CODE_STEP_UP_UNAVAILABLE ->
                "That way of confirming isn't available for your account. Try the other option."
            else -> "That code isn't right. Please try again."
        }
        is AppError.RateLimited -> lockedOutText(error)
        else -> forLogin(error)
    }

    /** `POST /v1/auth/forgot-password`. Always 200 on a reachable server; the rest is transport. */
    fun forForgotPassword(error: AppError): String = when (error) {
        is AppError.RateLimited -> lockedOutText(error)
        is AppError.InvalidRequest -> "Enter the email address you signed up with."
        else -> forLogin(error)
    }

    /**
     * `POST /v1/auth/reset-password`. 422 `WEAK_PASSWORD` leaves the code
     * usable, so the sentence points at the password; 400 `RESET_FAILED` is
     * deliberately unspecific server-side (wrong code, expired code, unknown
     * account) and stays unspecific here.
     */
    fun forResetPassword(error: AppError): String = when (error) {
        is AppError.Unknown -> when (error.code) {
            WEAK_PASSWORD -> "That password is too weak. ${PasswordRules.RULES_TEXT}"
            else -> RESET_FAILED_TEXT
        }
        is AppError.InvalidRequest -> RESET_FAILED_TEXT
        is AppError.RateLimited -> lockedOutText(error)
        else -> forLogin(error)
    }

    /** The Google button and the two OAuth signup steps. */
    fun forOAuth(error: AppError): String = when (error) {
        is AppError.AuthFailed -> when (error.code) {
            OAUTH_VERIFY_FAILED -> "That code didn't work. Check the message and try again."
            else -> "We couldn't sign you in with Google. Please try again."
        }
        is AppError.Unknown -> when (error.code) {
            OAUTH_SIGNUP_FAILED ->
                "We couldn't use that phone number. Check it, or try a different one."
            else -> "We couldn't sign you in with Google. Please try again."
        }
        is AppError.RateLimited -> lockedOutText(error)
        else -> forLogin(error)
    }

    private fun lockedOutText(error: AppError.RateLimited): String =
        error.retryAfterSeconds
            ?.let { "Too many attempts. For your security, try again in ${it.humanSeconds()}." }
            ?: "Too many attempts. For your security, wait a few minutes and try again."

    private const val STEP_UP_EXPIRED = "STEP_UP_EXPIRED"
    private const val STEP_UP_CODE_REPLAYED = "STEP_UP_CODE_REPLAYED"
    private const val WEAK_PASSWORD = "WEAK_PASSWORD"
    private const val OAUTH_VERIFY_FAILED = "OAUTH_VERIFY_FAILED"
    private const val OAUTH_SIGNUP_FAILED = "OAUTH_SIGNUP_FAILED"
    private const val RESET_FAILED_TEXT =
        "That code didn't work. It may have expired — request a new one and try again."

    private fun forbiddenText(code: String): String = when (code) {
        ErrorMapperCodes.EMAIL_NOT_VERIFIED ->
            "Your email isn't verified yet. Check your inbox to finish setting up."
        "STEP_UP_UNAVAILABLE" ->
            "This sign-in looks unusual and your account has no recovery option set up. " +
                "Please contact support."
        ErrorMapperCodes.ACCOUNT_PENDING_PURGE ->
            "This account is being deleted. It can't be used until the deletion is cancelled."
        ErrorMapperCodes.ACCOUNT_PURGED ->
            "This account has been deleted. Create a new account to continue."
        else -> "You don't have access to that right now."
    }

    private fun Long.humanSeconds(): String =
        if (this < SECONDS_PER_MINUTE) "$this seconds" else "${this / SECONDS_PER_MINUTE} minutes"

    private const val SECONDS_PER_MINUTE = 60
}

/** Codes referenced here but owned by `:core:network`'s ErrorMapper. */
internal object ErrorMapperCodes {
    const val EMAIL_NOT_VERIFIED = "EMAIL_NOT_VERIFIED"
    const val ACCOUNT_PENDING_PURGE = "ACCOUNT_PENDING_PURGE"
    const val ACCOUNT_PURGED = "ACCOUNT_PURGED"
}
