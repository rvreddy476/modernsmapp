package com.us.android.core.auth.dto

import kotlinx.serialization.EncodeDefault
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * Mirrors LoginRequest in
 * identity-platform/services/auth-service/internal/http/handler.go:557.
 *
 * The server accepts `identifier` OR `email` OR `phone` and collapses them
 * itself, so the client sends only `identifier`.
 */
@Serializable
data class LoginRequestDto(
    val identifier: String,
    val password: String,
    @SerialName("device_id") val deviceId: String? = null,
    val platform: String = "android",
)

@Serializable
data class RefreshRequestDto(
    @SerialName("refresh_token") val refreshToken: String,
)

/**
 * Mirrors RegisterRequest in
 * identity-platform/services/auth-service/internal/http/handler.go:416.
 *
 * Note there is NO display-name field, and nothing is concatenated on the way
 * out: the server takes [firstName] and [lastName] as separate, individually
 * validated, individually rejectable fields (`FIRST_NAME_REQUIRED`,
 * `LAST_NAME_REQUIRED`, `NAME_INVALID`). The UI's two inputs map 1:1 onto the
 * wire. `display_name` is a profile-service concept and is not part of signup.
 *
 * Every field below except [phone] and [gender] is mandatory server-side.
 */
@Serializable
data class RegisterRequestDto(
    /** Required. Phone-only registration is retired — no SMS delivery exists. */
    val email: String,
    val password: String,
    @SerialName("first_name") val firstName: String,
    @SerialName("last_name") val lastName: String,
    /** Required, `YYYY-MM-DD`. Gates on 18+; an absent value is a rejection. */
    val dob: String,
    /** Must be explicitly true — a consent that defaults to granted is not consent. */
    @SerialName("accepted_terms") val acceptedTerms: Boolean,
    /** Records WHICH text the user was shown, for later audit. */
    @SerialName("terms_version") val termsVersion: String,
    val gender: String? = null,
    val phone: String? = null,
)

/**
 * Mirrors service.TokenPair (auth.go:193).
 *
 * `expires_at` is an ABSOLUTE RFC3339 timestamp, not an `expires_in`
 * duration. Everything downstream must treat it that way — see
 * [com.us.android.core.auth.SessionManager].
 *
 * All three are optional because a 2FA / step-up response is an HTTP 200
 * that carries no tokens at all.
 */
@Serializable
data class TokenPairDto(
    @SerialName("access_token") val accessToken: String = "",
    @SerialName("refresh_token") val refreshToken: String = "",
    @SerialName("expires_at") val expiresAt: String? = null,
)

/**
 * Mirrors VerifyEmailRequest (handler.go:954).
 *
 * Note what is NOT here: a user id. The account is named solely by the
 * server-issued [verificationToken]. On a public route a caller-supplied id
 * would let anyone grind codes against any account they can name, so the
 * omission is the security property, not an oversight.
 */
@Serializable
data class VerifyEmailRequestDto(
    @SerialName("verification_token") val verificationToken: String,
    val code: String,
)

/**
 * Mirrors ResendVerificationRequest (handler.go:1037).
 *
 * [type] is `email` only. `phone` returns 410 SMS_UNAVAILABLE — there is no
 * SMS delivery, and reporting success would be a lie the user acts on.
 *
 * ## `@EncodeDefault` IS LOAD-BEARING
 *
 * kotlinx.serialization omits any property equal to its default unless told
 * otherwise, and the shared `Json` in `NetworkModule` deliberately leaves
 * `encodeDefaults` off. `AuthRepository` builds this DTO with only the token
 * (`AuthRepository.kt:139`), so `type` took its default and vanished from the
 * body — while the server binds it `required`
 * (`auth-service/internal/http/handler.go:1038`).
 *
 * Every resend therefore returned 400. That endpoint is the RECOVERY path for
 * someone whose first verification email never arrived, so the failure landed
 * on exactly the users who were already stuck, and it looked like the account
 * was broken rather than the request.
 *
 * Same defect and same fix as `SendMessageRequest.type` in `:core:chat`. The
 * default stays because `email` is the only kind that works; the annotation is
 * what puts it on the wire. `AuthRequestEncodingTest` pins the exact bytes.
 */
@Serializable
data class ResendVerificationRequestDto(
    @EncodeDefault(EncodeDefault.Mode.ALWAYS)
    val type: String = "email",
    @SerialName("verification_token") val verificationToken: String,
)

/** Both verification endpoints return only a human-facing message. */
@Serializable
data class MessageDto(val message: String = "")

@Serializable
data class AuthUserDto(
    val id: String = "",
    val email: String? = null,
    val phone: String? = null,
    @SerialName("first_name") val firstName: String? = null,
    @SerialName("last_name") val lastName: String? = null,
    val username: String? = null,
)

/**
 * Mirrors service.AuthResponse (auth.go:199).
 *
 * This single shape encodes FIVE different outcomes, and the traps are why
 * [com.us.android.core.model.SessionState] is a state machine rather than a
 * boolean:
 *
 *  - `requires_2fa` and `requires_step_up` are returned with **HTTP 200**
 *    and no tokens. A 200 does not mean the user is signed in.
 *  - `EMAIL_NOT_VERIFIED` is a **403** whose `error.details` carries a live
 *    `verification_token` — an error response that is the only resumption
 *    path for someone who closed the app mid-signup.
 */
@Serializable
data class AuthResponseDto(
    val tokens: TokenPairDto = TokenPairDto(),
    val user: AuthUserDto? = null,
    @SerialName("session_id") val sessionId: String = "",
    @SerialName("requires_2fa") val requires2fa: Boolean = false,
    @SerialName("pending_token") val pendingToken: String = "",
    @SerialName("requires_step_up") val requiresStepUp: Boolean = false,
    @SerialName("step_up_methods") val stepUpMethods: List<String> = emptyList(),
    @SerialName("requires_verification") val requiresVerification: Boolean = false,
    @SerialName("verification_token") val verificationToken: String = "",
    @SerialName("verification_expires_at") val verificationExpiresAt: String? = null,
)
