package com.us.android.core.auth

import com.us.android.core.auth.dto.AccountDeactivatedDto
import com.us.android.core.auth.dto.AccountDeletionScheduledDto
import com.us.android.core.auth.dto.AuthResponseDto
import com.us.android.core.auth.dto.ForgotPasswordRequestDto
import com.us.android.core.auth.dto.LoginRequestDto
import com.us.android.core.auth.dto.MessageDto
import com.us.android.core.auth.dto.OAuthCompleteSignupRequestDto
import com.us.android.core.auth.dto.OAuthTokenRequestDto
import com.us.android.core.auth.dto.OAuthVerifySignupRequestDto
import com.us.android.core.auth.dto.PasswordRequestDto
import com.us.android.core.auth.dto.RefreshRequestDto
import com.us.android.core.auth.dto.RegisterRequestDto
import com.us.android.core.auth.dto.ResendVerificationRequestDto
import com.us.android.core.auth.dto.ResetPasswordRequestDto
import com.us.android.core.auth.dto.StepUpVerifyRequestDto
import com.us.android.core.auth.dto.Verify2FARequestDto
import com.us.android.core.auth.dto.VerifyEmailRequestDto
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.retry.Retryable
import kotlinx.serialization.json.JsonObject
import retrofit2.http.Body
import retrofit2.http.HTTP
import retrofit2.http.Header
import retrofit2.http.POST
import retrofit2.http.Path

interface AuthApi {

    @POST("v1/auth/partner/phone/request")
    suspend fun requestPartnerPhone(@Body body: com.us.android.core.auth.dto.PartnerPhoneRequestDto): ApiEnvelope<com.us.android.core.auth.dto.PartnerPhoneChallengeDto>

    @POST("v1/auth/partner/phone/verify")
    suspend fun verifyPartnerPhone(@Body body: com.us.android.core.auth.dto.PartnerPhoneVerifyDto): ApiEnvelope<AuthResponseDto>

    @POST("v1/auth/login")
    suspend fun login(@Body body: LoginRequestDto): ApiEnvelope<AuthResponseDto>

    /**
     * Returns **201 with no session**. The account is created PENDING and a
     * verification email is sent; the response carries a `verification_token`
     * that authorises finishing signup. Treating 201 as "signed in" is wrong.
     *
     * [idempotencyKey] makes a retry safe: the server replays the original
     * response for the same key and body rather than colliding on the email
     * unique constraint and reporting `USER_EXISTS` to someone who never saw a
     * success. It is what earns the [Retryable] annotation — without the key
     * this call must NOT be replayed.
     */
    @Retryable
    @POST("v1/auth/register")
    suspend fun register(
        @Header("Idempotency-Key") idempotencyKey: String,
        @Body body: RegisterRequestDto,
    ): ApiEnvelope<AuthResponseDto>

    /**
     * The server tries the refresh cookie first and falls back to this JSON
     * body (handler.go:650). The body path is the documented mobile path and
     * the one this client uses — bearer is authoritative, cookies are not
     * session state (blocker B4).
     */
    @POST("v1/auth/refresh")
    suspend fun refresh(@Body body: RefreshRequestDto): ApiEnvelope<AuthResponseDto>

    /**
     * Confirms the emailed 6-digit code.
     *
     * Returns **no session**. A verified account still has to sign in — this
     * endpoint only flips `email_verified`.
     *
     * Every failure is one response (400 `VERIFY_FAILED`): wrong code,
     * expired, replayed, unknown token. Deliberately indistinguishable, so a
     * caller cannot probe which tokens or accounts exist.
     */
    @POST("v1/auth/verify-email")
    suspend fun verifyEmail(@Body body: VerifyEmailRequestDto): ApiEnvelope<MessageDto>

    /** Re-sends the code. Works with no session, scoped by the token alone. */
    @POST("v1/auth/resend-verification")
    suspend fun resendVerification(
        @Body body: ResendVerificationRequestDto,
    ): ApiEnvelope<MessageDto>

    @POST("v1/auth/logout")
    suspend fun logout(): ApiEnvelope<Unit>

    // ── Sign-in security (web parity, founder 21 Oct 2026) ──────────────

    /**
     * Finishes a sign-in that returned `requires_2fa`. Public, behind the
     * login rate limiter (5/identifier + 10/IP per 15 min). Every failure —
     * wrong code, replayed code, expired pending session, user mismatch — is
     * ONE response, 401 `AUTH_FAILED` (twofa.go:124); a 429 is the lockout.
     * Success is a full session, like [login].
     */
    @POST("v1/auth/2fa/verify")
    suspend fun verifyTwoFactor(@Body body: Verify2FARequestDto): ApiEnvelope<AuthResponseDto>

    /**
     * Finishes an unusual sign-in with the emailed code. 401 with a specific
     * code: `STEP_UP_EXPIRED` (sign in again), `STEP_UP_CODE_INVALID`,
     * `STEP_UP_CODE_REPLAYED`, `STEP_UP_UNAVAILABLE` (anomaly.go:76).
     */
    @POST("v1/auth/anomaly/verify-email")
    suspend fun verifyStepUpEmail(@Body body: StepUpVerifyRequestDto): ApiEnvelope<AuthResponseDto>

    /** As [verifyStepUpEmail], with a TOTP or recovery code. Same codes. */
    @POST("v1/auth/anomaly/verify-2fa")
    suspend fun verifyStepUpTwoFactor(@Body body: StepUpVerifyRequestDto): ApiEnvelope<AuthResponseDto>

    /**
     * Always 200 — "If the account exists, a reset code has been sent" — so
     * nobody can learn which addresses are registered (handler.go:1084).
     * Recovery is EMAIL ONLY at launch: a phone identifier is accepted and
     * silently delivers nothing (auth.go:1153).
     */
    @POST("v1/auth/forgot-password")
    suspend fun forgotPassword(@Body body: ForgotPasswordRequestDto): ApiEnvelope<MessageDto>

    /**
     * Consumes the code, sets the password and revokes EVERY session in one
     * transaction (auth.go:1215). 422 `WEAK_PASSWORD` leaves the code usable;
     * 400 `RESET_FAILED` for anything else, deliberately unspecific.
     */
    @POST("v1/auth/reset-password")
    suspend fun resetPassword(@Body body: ResetPasswordRequestDto): ApiEnvelope<MessageDto>

    /**
     * Signs in with a provider ACCESS token (oauth.go:84). Two success shapes:
     * 200 with a normal [AuthResponseDto] (which may itself say
     * `requires_2fa`), or 202 with an [OAuthPendingSignupDto] when the
     * provider did not vouch for the email and no account exists. Decoded in
     * `AuthRepository` from the raw object, because one Retrofit return type
     * cannot name both. 401 `AUTH_FAILED` otherwise.
     */
    @POST("v1/auth/oauth/{provider}/token")
    suspend fun oauthToken(
        @Path("provider") provider: String,
        @Body body: OAuthTokenRequestDto,
    ): ApiEnvelope<JsonObject>

    /** Attaches a phone to a pending OAuth signup; 200 `{"message":"otp sent"}`, 400 `OAUTH_SIGNUP_FAILED`. */
    @POST("v1/auth/oauth/complete-signup")
    suspend fun oauthCompleteSignup(@Body body: OAuthCompleteSignupRequestDto): ApiEnvelope<MessageDto>

    /** Creates the account and issues a session: **201**, not 200. 401 `OAUTH_VERIFY_FAILED`. */
    @POST("v1/auth/oauth/verify-signup")
    suspend fun oauthVerifySignup(@Body body: OAuthVerifySignupRequestDto): ApiEnvelope<AuthResponseDto>

    /**
     * Deactivates the account. Every session is revoked server-side on
     * success, so the caller must clear the local one too. 401
     * `INVALID_PASSWORD`; 409 `ACCOUNT_STATE_CONFLICT` when already
     * deactivated or pending deletion.
     */
    @POST("v1/auth/account/deactivate")
    suspend fun deactivateAccount(@Body body: PasswordRequestDto): ApiEnvelope<AccountDeactivatedDto>

    /**
     * Schedules deletion 30 days out. Retrofit's `@DELETE` refuses a body, so
     * this is the explicit form. Same revocation and error codes as
     * [deactivateAccount].
     */
    @HTTP(method = "DELETE", path = "v1/auth/account", hasBody = true)
    suspend fun deleteAccount(@Body body: PasswordRequestDto): ApiEnvelope<AccountDeletionScheduledDto>
}
