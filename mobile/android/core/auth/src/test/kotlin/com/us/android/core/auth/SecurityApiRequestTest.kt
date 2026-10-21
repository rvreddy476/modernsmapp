package com.us.android.core.auth

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.SessionState
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.cookie.CsrfCookieStore
import com.us.android.core.telemetry.NoOpTelemetry
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import javax.inject.Provider

/**
 * The sign-in security routes (web parity, founder 21 Oct 2026), on the wire.
 *
 * Request side: the exact path and the exact JSON body, because every one of
 * these handlers binds its fields `required` and a renamed key is a 400 for
 * every user. Response side: fixtures are copied from the handlers
 * (twofa.go, anomaly.go, oauth.go, handler.go) — the statuses and codes are
 * the ones the service writes, including the 202 and the 201 that a client
 * expecting 200 would mishandle.
 */
class SecurityApiRequestTest {

    private lateinit var server: MockWebServer
    private lateinit var tokenStore: FakeTokenStore

    /** Mirrors NetworkModule.provideJson(): no encodeDefaults. */
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        tokenStore = FakeTokenStore()
    }

    @After
    fun tearDown() = server.close()

    private fun repository(scope: kotlinx.coroutines.CoroutineScope): AuthRepository {
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(AuthApi::class.java)
        val sessionManager = SessionManager(
            tokenStore = tokenStore,
            authApi = Provider { api },
            cookieStore = CsrfCookieStore(),
            telemetry = NoOpTelemetry,
            scope = scope,
        )
        return AuthRepository(api, sessionManager, ErrorMapper(json), emptySet(), json)
    }

    private fun enqueue(code: Int, body: String) {
        server.enqueue(MockResponse.Builder().code(code).body(body).build())
    }

    private fun RecordedRequest.bodyText(): String = body!!.utf8()

    // ── 2FA ────────────────────────────────────────────────────────────

    @Test
    fun `2fa verify sends user_id, code and pending_token and a 200 is a session`() = runTest {
        enqueue(200, SESSION_BODY)

        val result = repository(this).verifyTwoFactor(userId = "u1", pendingToken = "pt-1", code = " 123456 ")

        val request = server.takeRequest()
        assertThat(request.target).isEqualTo("/v1/auth/2fa/verify")
        assertThat(request.bodyText()).isEqualTo("""{"user_id":"u1","code":"123456","pending_token":"pt-1"}""")
        assertThat((result as AppResult.Success).data).isEqualTo(SessionState.Authenticated("u1", "s1"))
        assertThat(tokenStore.readRefreshToken()).isEqualTo("rt")
    }

    @Test
    fun `2fa verify keeps the one code the server gives for every failure`() = runTest {
        enqueue(401, """{"error":{"code":"AUTH_FAILED","message":"2FA verification failed"}}""")

        val error = (repository(this).verifyTwoFactor("u1", "pt-1", "000000") as AppResult.Failure).error

        assertThat(error).isEqualTo(AppError.AuthFailed(requestId = null, code = "AUTH_FAILED"))
        assertThat(tokenStore.hasRefreshToken()).isFalse()
    }

    @Test
    fun `2fa verify lockout is the login limiter's 429`() = runTest {
        server.enqueue(
            MockResponse.Builder().code(429).addHeader("Retry-After", "900")
                .body("""{"error":{"code":"RATE_LIMITED","message":"too many"}}""").build(),
        )

        val error = (repository(this).verifyTwoFactor("u1", "pt-1", "000000") as AppResult.Failure).error

        assertThat((error as AppError.RateLimited).retryAfterSeconds).isEqualTo(900)
    }

    // ── Step-up ────────────────────────────────────────────────────────

    @Test
    fun `step-up by email posts the shared body to verify-email`() = runTest {
        enqueue(200, SESSION_BODY)

        val result = repository(this).verifyStepUp(AuthRepository.STEP_UP_METHOD_EMAIL, "pt-2", "654321")

        val request = server.takeRequest()
        assertThat(request.target).isEqualTo("/v1/auth/anomaly/verify-email")
        assertThat(request.bodyText()).isEqualTo("""{"pending_token":"pt-2","code":"654321"}""")
        assertThat((result as AppResult.Success).data).isInstanceOf(SessionState.Authenticated::class.java)
    }

    @Test
    fun `step-up by authenticator posts the same body to verify-2fa`() = runTest {
        enqueue(200, SESSION_BODY)

        repository(this).verifyStepUp(AuthRepository.STEP_UP_METHOD_TOTP, "pt-2", "654321")

        val request = server.takeRequest()
        assertThat(request.target).isEqualTo("/v1/auth/anomaly/verify-2fa")
        assertThat(request.bodyText()).isEqualTo("""{"pending_token":"pt-2","code":"654321"}""")
    }

    @Test
    fun `step-up 401s keep their specific code so the screen can say what to do`() = runTest {
        enqueue(
            401,
            """{"error":{"code":"STEP_UP_EXPIRED","message":"Your verification session has expired. Please sign in again."}}""",
        )

        val error = (repository(this).verifyStepUp("email_otp", "pt-2", "111111") as AppResult.Failure).error

        assertThat((error as AppError.AuthFailed).code).isEqualTo("STEP_UP_EXPIRED")
    }

    // ── Forgot / reset ─────────────────────────────────────────────────

    @Test
    fun `forgot password sends the identifier and a 200 says nothing about the account`() = runTest {
        enqueue(200, """{"data":{"message":"If the account exists, a reset code has been sent"}}""")

        val result = repository(this).forgotPassword("  raghu@example.com ")

        val request = server.takeRequest()
        assertThat(request.target).isEqualTo("/v1/auth/forgot-password")
        assertThat(request.bodyText()).isEqualTo("""{"identifier":"raghu@example.com"}""")
        assertThat(result).isInstanceOf(AppResult.Success::class.java)
    }

    @Test
    fun `reset password sends new_password with an underscore and does not touch the session`() = runTest {
        tokenStore = FakeTokenStore(userId = "u1", sessionId = "s1", refreshToken = "rt")
        enqueue(200, """{"data":{"message":"Password reset successfully"}}""")

        val result = repository(this).resetPassword("raghu@example.com", "123456", "Str0ng!pass")

        val request = server.takeRequest()
        assertThat(request.target).isEqualTo("/v1/auth/reset-password")
        assertThat(request.bodyText())
            .isEqualTo("""{"identifier":"raghu@example.com","code":"123456","new_password":"Str0ng!pass"}""")
        assertThat(result).isInstanceOf(AppResult.Success::class.java)
        // Held back for the Done step: endSessionAfterPasswordReset clears it.
        assertThat(tokenStore.readRefreshToken()).isEqualTo("rt")
    }

    @Test
    fun `ending the session after a reset clears the local session once`() = runTest {
        tokenStore = FakeTokenStore(userId = "u1", sessionId = "s1", refreshToken = "rt")
        val repository = repository(this)

        repository.endSessionAfterPasswordReset()
        repository.endSessionAfterPasswordReset()

        assertThat(tokenStore.hasRefreshToken()).isFalse()
        assertThat(tokenStore.clearCount).isEqualTo(1)
        assertThat(server.requestCount).isEqualTo(0)
    }

    @Test
    fun `a weak password is 422 WEAK_PASSWORD, anything else 400 RESET_FAILED`() = runTest {
        enqueue(422, """{"error":{"code":"WEAK_PASSWORD","message":"password must contain an uppercase letter"}}""")
        enqueue(400, """{"error":{"code":"RESET_FAILED","message":"Password reset failed"}}""")
        val repository = repository(this)

        val weak = (repository.resetPassword("a@b.c", "123456", "weak") as AppResult.Failure).error
        val failed = (repository.resetPassword("a@b.c", "123456", "Str0ng!pass") as AppResult.Failure).error

        assertThat((weak as AppError.Unknown).code).isEqualTo("WEAK_PASSWORD")
        assertThat((failed as AppError.Unknown).code).isEqualTo("RESET_FAILED")
    }

    // ── OAuth ──────────────────────────────────────────────────────────

    @Test
    fun `oauth token posts the access token to the provider route and a 200 is a session`() = runTest {
        enqueue(200, SESSION_BODY)

        val result = repository(this).signInWithOAuth("google", "ya29.access")

        val request = server.takeRequest()
        assertThat(request.target).isEqualTo("/v1/auth/oauth/google/token")
        assertThat(request.bodyText()).isEqualTo("""{"token":"ya29.access"}""")
        val outcome = (result as AppResult.Success).data as OAuthSignInOutcome.SignedIn
        assertThat(outcome.state).isEqualTo(SessionState.Authenticated("u1", "s1"))
    }

    @Test
    fun `oauth token 200 with requires_2fa is a pending second step, not a session`() = runTest {
        enqueue(200, """{"data":{"requires_2fa":true,"pending_token":"pt-9","user":{"id":"u1"}}}""")

        val outcome = (repository(this).signInWithOAuth("google", "t") as AppResult.Success).data

        assertThat((outcome as OAuthSignInOutcome.SignedIn).state)
            .isEqualTo(SessionState.PendingTwoFactor(pendingToken = "pt-9", userId = "u1"))
        assertThat(tokenStore.hasRefreshToken()).isFalse()
    }

    @Test
    fun `oauth token 202 pending_signup is a half-made account, never a session`() = runTest {
        // service.OAuthPendingResponse (oauth.go:48), as the handler writes it with 202.
        enqueue(
            202,
            """{"data":{"status":"pending_signup","pending_token":"op-1","provider":"google",""" +
                """"email":"raghu@example.com","name":"Raghu","next_step":"complete_signup",""" +
                """"message":"Verify a phone number to finish"}}""",
        )

        val outcome = (repository(this).signInWithOAuth("google", "t") as AppResult.Success).data

        assertThat(outcome).isEqualTo(
            OAuthSignInOutcome.PendingSignup(
                pendingToken = "op-1",
                provider = "google",
                email = "raghu@example.com",
                name = "Raghu",
            ),
        )
        assertThat(tokenStore.hasRefreshToken()).isFalse()
    }

    @Test
    fun `oauth token refusal is AUTH_FAILED`() = runTest {
        enqueue(401, """{"error":{"code":"AUTH_FAILED","message":"OAuth authentication failed"}}""")

        val error = (repository(this).signInWithOAuth("google", "bad") as AppResult.Failure).error

        assertThat(error).isInstanceOf(AppError.AuthFailed::class.java)
    }

    @Test
    fun `complete-signup sends pending_token and phone`() = runTest {
        enqueue(200, """{"data":{"message":"otp sent"}}""")

        val result = repository(this).completeOAuthSignup("op-1", " +919876543210 ")

        val request = server.takeRequest()
        assertThat(request.target).isEqualTo("/v1/auth/oauth/complete-signup")
        assertThat(request.bodyText()).isEqualTo("""{"pending_token":"op-1","phone":"+919876543210"}""")
        assertThat(result).isInstanceOf(AppResult.Success::class.java)
    }

    @Test
    fun `verify-signup sends otp (not code) with platform, and a 201 is a session`() = runTest {
        enqueue(201, SESSION_BODY)

        val result = repository(this).verifyOAuthSignup("op-1", "123456", "dev-1")

        val request = server.takeRequest()
        assertThat(request.target).isEqualTo("/v1/auth/oauth/verify-signup")
        assertThat(request.bodyText())
            .isEqualTo("""{"pending_token":"op-1","otp":"123456","device_id":"dev-1","platform":"android"}""")
        assertThat((result as AppResult.Success).data).isEqualTo(SessionState.Authenticated("u1", "s1"))
    }

    @Test
    fun `verify-signup refusal keeps its code`() = runTest {
        enqueue(401, """{"error":{"code":"OAUTH_VERIFY_FAILED","message":"invalid otp"}}""")

        val error = (repository(this).verifyOAuthSignup("op-1", "000000", "dev-1") as AppResult.Failure).error

        assertThat((error as AppError.AuthFailed).code).isEqualTo("OAUTH_VERIFY_FAILED")
    }

    private companion object {
        const val SESSION_BODY =
            """{"data":{"tokens":{"access_token":"at","refresh_token":"rt","expires_at":"2026-12-01T00:00:00Z"},""" +
                """"user":{"id":"u1"},"session_id":"s1"}}"""
    }
}
