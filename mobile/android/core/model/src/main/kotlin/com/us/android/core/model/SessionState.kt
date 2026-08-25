package com.us.android.core.model

/**
 * The authoritative view of "who is signed in", consumed by the navigation
 * graph and by every repository that needs a user id.
 *
 * This is a five-state machine rather than a boolean because the backend
 * genuinely has five outcomes for a sign-in attempt. Two of them are traps
 * a naive client walks straight into:
 *
 *  1. `requires_2fa` and `requires_step_up` both come back as **HTTP 200**
 *     with no tokens. A 200 does not mean the user is logged in.
 *  2. `EMAIL_NOT_VERIFIED` comes back as **HTTP 403 carrying a live
 *     verification_token inside `error.details`** — an error response that
 *     is actually the only resumption path for someone who closed the app
 *     mid-signup. Discarding error bodies loses the account.
 *
 * Verified against identity-platform/services/auth-service/internal/service/auth.go
 * (AuthResponse) and .../internal/http/handler.go (Login).
 * See PHASE_0_1_PLAN.md §D.2.
 *
 * Phase 1 populates this. Phase 0 only defines the shape so the navigation
 * graph can be written against a stable contract.
 */
sealed interface SessionState {
    /**
     * Pre-restore. Observed only while persisted credentials are being read.
     *
     * The splash destination is the only screen allowed to render this. The
     * nav graph must never *await* a transition out of Unknown — that is the
     * cold-start stall (finding F5) this whole design exists to avoid.
     */
    data object Unknown : SessionState

    /** No credentials, or refresh failed and storage was cleared. */
    data object Unauthenticated : SessionState

    /** Password accepted; a second factor is outstanding. Not yet a session. */
    data class PendingTwoFactor(val pendingToken: String) : SessionState

    /**
     * Password accepted but the risk band demanded a second channel.
     * [methods] enumerates what the server will accept, e.g. ["email_otp", "totp"].
     */
    data class PendingStepUp(val methods: List<String>) : SessionState

    /**
     * The account exists but its email is unverified. [token] authorises
     * submitting or re-requesting a verification code for this one account
     * and nothing else — it is not a session.
     */
    data class PendingVerification(
        val token: String,
        val expiresAtEpochSeconds: Long,
    ) : SessionState

    /** A real, usable session. */
    data class Authenticated(
        val userId: String,
        val sessionId: String,
    ) : SessionState

    val isAuthenticated: Boolean get() = this is Authenticated
}
