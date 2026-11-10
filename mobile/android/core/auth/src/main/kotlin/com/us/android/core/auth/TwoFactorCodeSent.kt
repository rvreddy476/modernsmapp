package com.us.android.core.auth

/**
 * A new emailed two-step code is on its way (`POST /v1/auth/2fa/resend`).
 * [resendAfterSeconds] is the server's cooldown before another resend; 0
 * when the server did not say, and the screen falls back to its own default.
 */
data class TwoFactorCodeSent(
    val emailMasked: String,
    val resendAfterSeconds: Int,
)
