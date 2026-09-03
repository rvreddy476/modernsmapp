package com.us.android.core.auth

import com.us.android.core.auth.dto.AccountDeactivatedDto
import com.us.android.core.auth.dto.AccountDeletionScheduledDto
import com.us.android.core.auth.dto.AuthResponseDto
import com.us.android.core.auth.dto.LoginRequestDto
import com.us.android.core.auth.dto.MessageDto
import com.us.android.core.auth.dto.PasswordRequestDto
import com.us.android.core.auth.dto.RefreshRequestDto
import com.us.android.core.auth.dto.RegisterRequestDto
import com.us.android.core.auth.dto.ResendVerificationRequestDto
import com.us.android.core.auth.dto.VerifyEmailRequestDto
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.retry.Retryable
import retrofit2.http.Body
import retrofit2.http.HTTP
import retrofit2.http.Header
import retrofit2.http.POST

interface AuthApi {

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
