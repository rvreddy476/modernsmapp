package com.us.android.core.auth.dto

import kotlinx.serialization.EncodeDefault
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * Mirrors Verify2FARequest in
 * identity-platform/services/auth-service/internal/http/twofa.go:98.
 *
 * All three fields bind `required`. [userId] is checked against the pending
 * session the token names, so a token alone is not enough — the login response
 * supplies both (auth.go:1549). [code] is a 6-digit TOTP or a recovery code;
 * the server tries TOTP first and falls back to the recovery list, so the
 * client sends whatever was typed and does not pick a route.
 */
@Serializable
data class Verify2FARequestDto(
    @SerialName("user_id") val userId: String,
    val code: String,
    @SerialName("pending_token") val pendingToken: String,
)

/**
 * Mirrors AnomalyStepUpRequest (anomaly.go:15) — one body for both
 * `/anomaly/verify-email` and `/anomaly/verify-2fa`; only the URL differs.
 */
@Serializable
data class StepUpVerifyRequestDto(
    @SerialName("pending_token") val pendingToken: String,
    val code: String,
)

/** Mirrors ForgotPasswordRequest (handler.go:1080). Email or phone, server collapses it. */
@Serializable
data class ForgotPasswordRequestDto(val identifier: String)

/** Mirrors ResetPasswordRequest (handler.go:1099). */
@Serializable
data class ResetPasswordRequestDto(
    val identifier: String,
    val code: String,
    @SerialName("new_password") val newPassword: String,
)

/**
 * Mirrors OAuthTokenRequest (oauth.go:75). [token] is a provider ACCESS token,
 * not an ID token: the service hands it straight to Google's userinfo endpoint
 * (oauth.go:568), which an ID token cannot open.
 */
@Serializable
data class OAuthTokenRequestDto(val token: String)

/**
 * The 202 body of `POST /v1/auth/oauth/:provider/token` — service.OAuthPendingResponse
 * (oauth.go:48). The provider did not assert the email as verified and no
 * account exists, so the server stalls and asks for a phone number before it
 * creates anything. Deliberately a separate type from [AuthResponseDto]: a
 * pending signup has no tokens and must never be read as one.
 */
@Serializable
data class OAuthPendingSignupDto(
    val status: String = "",
    @SerialName("pending_token") val pendingToken: String = "",
    val provider: String = "",
    val email: String = "",
    val name: String = "",
    @SerialName("next_step") val nextStep: String = "",
    val message: String = "",
) {
    /** Exactly what the service writes (oauth.go:258). */
    val isPendingSignup: Boolean get() = status == STATUS_PENDING_SIGNUP && pendingToken.isNotBlank()

    companion object {
        const val STATUS_PENDING_SIGNUP = "pending_signup"
    }
}

/** Mirrors OAuthCompleteSignupRequest (oauth.go:111). */
@Serializable
data class OAuthCompleteSignupRequestDto(
    @SerialName("pending_token") val pendingToken: String,
    val phone: String,
)

/**
 * Mirrors OAuthVerifySignupRequest (oauth.go:132). The wire field is `otp`,
 * not `code`. `platform` is defaulted and therefore `@EncodeDefault`, for the
 * same reason as [ResendVerificationRequestDto.type]: the shared `Json` does
 * not encode defaults, and a session without a platform is unlabelled in the
 * sessions list.
 */
@Serializable
data class OAuthVerifySignupRequestDto(
    @SerialName("pending_token") val pendingToken: String,
    val otp: String,
    @SerialName("device_id") val deviceId: String,
    @EncodeDefault(EncodeDefault.Mode.ALWAYS)
    val platform: String = "android",
)
