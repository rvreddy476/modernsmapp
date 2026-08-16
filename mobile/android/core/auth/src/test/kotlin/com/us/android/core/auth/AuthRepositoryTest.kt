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
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import javax.inject.Provider

class AuthRepositoryTest {

    private lateinit var server: MockWebServer
    private lateinit var repository: AuthRepository
    private lateinit var tokenStore: FakeTokenStore
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
    }

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        tokenStore = FakeTokenStore()
    }

    @After
    fun tearDown() = server.close()

    private fun buildRepository(scope: kotlinx.coroutines.CoroutineScope): AuthRepository {
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
        return AuthRepository(api, sessionManager, ErrorMapper(json))
    }

    private fun enqueue(code: Int, body: String) {
        server.enqueue(MockResponse.Builder().code(code).body(body).build())
    }

    @Test
    fun `successful login yields Authenticated`() = runTest {
        repository = buildRepository(this)
        enqueue(
            200,
            """{"data":{"tokens":{"access_token":"at","refresh_token":"rt",""" +
                """"expires_at":"2026-08-16T12:00:00Z"},"user":{"id":"u1"},"session_id":"s1"}}""",
        )

        val result = repository.login("raghu@example.com", "hunter2")

        assertThat((result as AppResult.Success).data)
            .isEqualTo(SessionState.Authenticated("u1", "s1"))
    }

    @Test
    fun `a 403 EMAIL_NOT_VERIFIED becomes a usable PendingVerification, not a dead end`() =
        runTest {
            // Modification M2, and the single most consequential mapping in
            // Phase 1. The password already matched, so only the account
            // owner reaches here. The verification token lives in
            // error.details and is the ONLY way a user who closed the app
            // mid-signup can finish. Treating this 403 as terminal strands
            // the account permanently.
            repository = buildRepository(this)
            enqueue(
                403,
                """{"error":{"code":"EMAIL_NOT_VERIFIED","message":"email not verified",""" +
                    """"details":{"verification_token":"vt-xyz",""" +
                    """"verify_via":"POST /v1/auth/verify-email"}}}""",
            )

            val result = repository.login("raghu@example.com", "hunter2")

            assertThat(result).isInstanceOf(AppResult.Success::class.java)
            val state = (result as AppResult.Success).data as SessionState.PendingVerification
            assertThat(state.token).isEqualTo("vt-xyz")
        }

    @Test
    fun `a 401 stays a failure`() = runTest {
        repository = buildRepository(this)
        enqueue(401, """{"error":{"code":"AUTH_FAILED","message":"Authentication failed"}}""")

        val result = repository.login("raghu@example.com", "wrong")

        assertThat((result as AppResult.Failure).error)
            .isInstanceOf(AppError.AuthFailed::class.java)
    }

    @Test
    fun `a 403 that is not EMAIL_NOT_VERIFIED stays a failure`() = runTest {
        repository = buildRepository(this)
        enqueue(
            403,
            """{"error":{"code":"STEP_UP_UNAVAILABLE","message":"no recovery channel"}}""",
        )

        val error = (repository.login("a", "b") as AppResult.Failure).error
        assertThat((error as AppError.Forbidden).code).isEqualTo("STEP_UP_UNAVAILABLE")
    }

    @Test
    fun `logout clears the local session even when the server fails`() = runTest {
        // A user who taps "log out" must end up logged out. Offline, 500, DNS
        // failure — none of it should leave a live session on the device.
        tokenStore = FakeTokenStore(userId = "u1", refreshToken = "rt")
        repository = buildRepository(this)
        enqueue(500, """{"error":{"code":"INTERNAL","message":"boom"}}""")

        val result = repository.logout()

        assertThat(result).isInstanceOf(AppResult.Success::class.java)
        assertThat(repository.sessionState.value).isEqualTo(SessionState.Unauthenticated)
        assertThat(tokenStore.hasRefreshToken()).isFalse()
    }
}
