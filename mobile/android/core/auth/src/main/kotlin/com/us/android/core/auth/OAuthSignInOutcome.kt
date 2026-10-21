package com.us.android.core.auth

import com.us.android.core.model.SessionState

/**
 * What `POST /v1/auth/oauth/:provider/token` came back with.
 *
 * Two bodies, two HTTP statuses (oauth.go:84): a 200 that is an ordinary
 * sign-in — including the `requires_2fa` case, which lands in [SignedIn] as
 * [SessionState.PendingTwoFactor] — and a 202 that is a half-made account
 * waiting for a phone number (A5). They are not the same thing, so they are
 * not the same type.
 */
sealed interface OAuthSignInOutcome {
    data class SignedIn(val state: SessionState) : OAuthSignInOutcome

    /**
     * The provider did not vouch for the email and no account exists. The
     * [pendingToken] authorises `complete-signup` and `verify-signup` for this
     * one attempt and nothing else; [email] and [name] are for display.
     */
    data class PendingSignup(
        val pendingToken: String,
        val provider: String,
        val email: String,
        val name: String,
    ) : OAuthSignInOutcome
}
