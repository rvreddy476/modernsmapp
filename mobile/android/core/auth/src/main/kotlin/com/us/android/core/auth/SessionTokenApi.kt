package com.us.android.core.auth

import com.us.android.core.auth.dto.AuthResponseDto
import com.us.android.core.auth.dto.LogoutRequestDto
import com.us.android.core.auth.dto.RefreshRequestDto
import com.us.android.core.network.ApiEnvelope
import retrofit2.http.Body
import retrofit2.http.POST

/**
 * The two calls that move a session's refresh token, on the `@SessionClient`
 * (its own dispatcher and timeouts, no authenticator — see `NetworkModule`).
 *
 * Both send the refresh token in the JSON body, `{"refresh_token": "..."}`,
 * the field auth-service's `Refresh` (handler.go:933) and `Logout`
 * (handler.go:967) bind. Never a cookie: the app's cookie jar holds only
 * `csrf_token` (H1).
 */
interface SessionTokenApi {

    /**
     * 200 with a full [AuthResponseDto] (rotated refresh token). 401
     * `AUTH_FAILED` for a refresh token that is unknown, expired, revoked or
     * reused; 401 `UNAUTHORIZED` when none was sent; 429 from the refresh
     * limiter.
     */
    @POST("v1/auth/refresh")
    suspend fun refresh(@Body body: RefreshRequestDto): ApiEnvelope<AuthResponseDto>

    /** Revokes the session the token names. 200 `{"status":"ok"}`, also for an unknown token. */
    @POST("v1/auth/logout")
    suspend fun logout(@Body body: LogoutRequestDto): ApiEnvelope<Unit>
}

/**
 * [SessionTokenApi] over a plain [AuthApi], for code that builds a
 * [SessionManager] by hand (tests in other modules) and never wired the
 * session client. Production always injects the real one. [AuthApi.logout]
 * takes no body, so this logout revokes only what a cookie would carry —
 * which, with the jar holding `csrf_token` alone, is nothing.
 */
internal class AuthApiSessionTokens(private val api: AuthApi) : SessionTokenApi {
    override suspend fun refresh(body: RefreshRequestDto): ApiEnvelope<AuthResponseDto> = api.refresh(body)

    override suspend fun logout(body: LogoutRequestDto): ApiEnvelope<Unit> = api.logout()
}
