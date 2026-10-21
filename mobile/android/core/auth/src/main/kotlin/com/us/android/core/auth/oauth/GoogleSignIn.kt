package com.us.android.core.auth.oauth

import com.us.android.core.common.result.AppResult
import kotlinx.coroutines.flow.StateFlow

/**
 * Google sign-in for the app, as the Login screen sees it.
 *
 * The app ships no Google library (nothing in the offline Gradle cache, and
 * the server wants a provider ACCESS token that Credential Manager's ID token
 * is not), so this is the plain OAuth 2.0 authorization-code flow with PKCE,
 * run through the device's browser:
 *
 *  1. [begin] mints a verifier and a `state`, and returns the authorization
 *     URL the screen opens with the system browser.
 *  2. Google redirects to `com.googleusercontent.apps.<id>:/oauth2redirect`,
 *     which the manifest routes to `MainActivity`; the activity hands the
 *     URI to [offerRedirect], whichever instance received it.
 *  3. The Login screen, watching [pendingRedirect], calls [complete], which
 *     checks `state`, exchanges the code for an access token and returns it
 *     for `AuthRepository.signInWithOAuth`.
 *
 * Only the access token leaves this interface. The verifier, the state and
 * the code never reach a screen, a log or the message host.
 */
interface GoogleSignIn {

    /** False when the build carries no client id: the button is not shown. */
    val isAvailable: Boolean

    /** The redirect the browser handed back and nobody has consumed yet. */
    val pendingRedirect: StateFlow<String?>

    /** Starts an attempt, replacing any earlier one. Returns the URL to open. */
    fun begin(): String

    /**
     * Offers an incoming intent's data. Returns true when it was this flow's
     * redirect (so the caller can stop treating it as a deep link), false for
     * anything else — the caller passes every link through without checking.
     */
    fun offerRedirect(uri: String?): Boolean

    /** Finishes the attempt that [redirect] belongs to. Consumes [pendingRedirect]. */
    suspend fun complete(redirect: String): AppResult<String>

    companion object {
        /** The person closed the consent page or pressed Cancel. Not an error worth a message. */
        const val CODE_CANCELLED = "OAUTH_CANCELLED"

        /** The redirect's `state` did not match the attempt in flight (or there was none). */
        const val CODE_STATE_MISMATCH = "OAUTH_STATE_MISMATCH"

        /** Google refused the code-for-token exchange. */
        const val CODE_TOKEN_EXCHANGE = "OAUTH_TOKEN_EXCHANGE"
    }
}
