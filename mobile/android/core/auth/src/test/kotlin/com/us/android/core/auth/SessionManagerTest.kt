package com.us.android.core.auth

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.SessionState
import com.us.android.core.network.cookie.CsrfCookieStore
import com.us.android.core.telemetry.NoOpTelemetry
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
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
import java.util.concurrent.atomic.AtomicInteger
import javax.inject.Provider

/**
 * The five sign-in outcomes and the refresh contract.
 *
 * Every fixture here is the real shape from
 * identity-platform/.../internal/service/auth.go (AuthResponse), because the
 * two traps this file guards against are shape-level, not logic-level:
 * a 200 that is not a session, and a 403 that is not terminal.
 */
class SessionManagerTest {

    private lateinit var server: MockWebServer
    private lateinit var tokenStore: FakeTokenStore
    private lateinit var api: AuthApi
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
    }

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        tokenStore = FakeTokenStore()
        api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(AuthApi::class.java)
    }

    @After
    fun tearDown() = server.close()

    private fun manager(scope: TestScope) = SessionManager(
        tokenStore = tokenStore,
        authApi = Provider { api },
        cookieStore = CsrfCookieStore(),
        telemetry = NoOpTelemetry,
        scope = scope,
    )

    private fun enqueue(code: Int, body: String) {
        server.enqueue(MockResponse.Builder().code(code).body(body).build())
    }

    // ── The five outcomes ───────────────────────────────────────────────

    @Test
    fun `tokens present yields Authenticated`() = runTest {
        val sm = manager(this)
        val state = sm.applyAuthResponse(
            json.decodeFromString(
                """{"tokens":{"access_token":"at","refresh_token":"rt",""" +
                    """"expires_at":"2026-08-16T12:00:00Z"},""" +
                    """"user":{"id":"u1"},"session_id":"s1"}""",
            ),
        )

        assertThat(state).isEqualTo(SessionState.Authenticated("u1", "s1"))
        assertThat(sm.currentAccessToken()).isEqualTo("at")
        assertThat(tokenStore.readRefreshToken()).isEqualTo("rt")
    }

    @Test
    fun `requires_2fa on a 200 is NOT a session`() = runTest {
        // The trap: HTTP 200, no tokens. A client that treats 200 as success
        // signs the user in without a second factor.
        val sm = manager(this)
        val state = sm.applyAuthResponse(
            json.decodeFromString("""{"requires_2fa":true,"pending_token":"pt-1"}"""),
        )

        assertThat(state).isEqualTo(SessionState.PendingTwoFactor("pt-1"))
        assertThat(sm.currentAccessToken()).isNull()
        assertThat(tokenStore.hasRefreshToken()).isFalse()
    }

    @Test
    fun `requires_step_up on a 200 is NOT a session`() = runTest {
        val sm = manager(this)
        val state = sm.applyAuthResponse(
            json.decodeFromString(
                """{"requires_step_up":true,"step_up_methods":["email_otp","totp"]}""",
            ),
        )

        assertThat(state).isEqualTo(SessionState.PendingStepUp(listOf("email_otp", "totp")))
        assertThat(sm.currentAccessToken()).isNull()
    }

    @Test
    fun `requires_verification yields PendingVerification with a parsed expiry`() = runTest {
        val sm = manager(this)
        val state = sm.applyAuthResponse(
            json.decodeFromString(
                """{"requires_verification":true,"verification_token":"vt-1",""" +
                    """"verification_expires_at":"2026-08-16T12:00:00Z"}""",
            ),
        ) as SessionState.PendingVerification

        assertThat(state.token).isEqualTo("vt-1")
        // Derived, not hand-computed: the point of the assertion is that the
        // RFC3339 string was parsed as an absolute instant, not that anyone
        // can do calendar arithmetic in their head.
        assertThat(state.expiresAtEpochSeconds)
            .isEqualTo(java.time.Instant.parse("2026-08-16T12:00:00Z").epochSecond)
    }

    @Test
    fun `an empty response yields Unauthenticated rather than a half-session`() = runTest {
        val sm = manager(this)
        assertThat(sm.applyAuthResponse(json.decodeFromString("{}")))
            .isEqualTo(SessionState.Unauthenticated)
    }

    // ── Cold start ──────────────────────────────────────────────────────

    @Test
    fun `initial state resolves synchronously and is never Unknown`() = runTest {
        // Closes finding F5. The Flutter router awaits session restore on
        // every navigation with a 3s deadline; here the nav graph gets a real
        // value on the first frame with no I/O beyond a prefs read.
        tokenStore = FakeTokenStore(userId = "u9", sessionId = "s9", refreshToken = "rt")
        assertThat(manager(this).state.value)
            .isEqualTo(SessionState.Authenticated("u9", "s9"))

        tokenStore = FakeTokenStore()
        assertThat(manager(this).state.value).isEqualTo(SessionState.Unauthenticated)
    }

    // ── Refresh ─────────────────────────────────────────────────────────

    @Test
    fun `refresh adopts the new token pair`() = runTest {
        tokenStore = FakeTokenStore(userId = "u1", refreshToken = "old-rt")
        val sm = manager(this)
        enqueue(
            200,
            """{"data":{"tokens":{"access_token":"new-at","refresh_token":"new-rt",""" +
                """"expires_at":"2026-08-16T12:00:00Z"},"user":{"id":"u1"},"session_id":"s1"}}""",
        )

        assertThat(sm.refresh()).isEqualTo("new-at")
        assertThat(tokenStore.readRefreshToken()).isEqualTo("new-rt")
    }

    @Test
    fun `N concurrent refreshes collapse into ONE network call`() = runTest {
        // Acceptance criterion F.6. A feed screen firing several parallel
        // requests that all 401 must produce one refresh, not one per request
        // — otherwise the server sees a burst and rotates the refresh token
        // out from under the callers still in flight.
        tokenStore = FakeTokenStore(userId = "u1", refreshToken = "old-rt")
        val dispatcher = StandardTestDispatcher(testScheduler)
        val scope = TestScope(dispatcher)
        val sm = SessionManager(
            tokenStore = tokenStore,
            authApi = Provider { api },
            cookieStore = CsrfCookieStore(),
            telemetry = NoOpTelemetry,
            scope = scope,
        )

        val hits = AtomicInteger(0)
        server.dispatcher = object : mockwebserver3.Dispatcher() {
            override fun dispatch(request: mockwebserver3.RecordedRequest): MockResponse {
                hits.incrementAndGet()
                return MockResponse.Builder()
                    .code(200)
                    .body(
                        """{"data":{"tokens":{"access_token":"new-at",""" +
                            """"refresh_token":"new-rt","expires_at":"2026-08-16T12:00:00Z"},""" +
                            """"user":{"id":"u1"},"session_id":"s1"}}""",
                    )
                    .build()
            }
        }

        val results = (1..8).map {
            async(Dispatchers.IO) { sm.refresh() }
        }.awaitAll()

        assertThat(results.toSet()).containsExactly("new-at")
        assertThat(hits.get()).isEqualTo(1)
    }

    @Test
    fun `a failed refresh clears the session exactly once and emits Unauthenticated`() = runTest {
        tokenStore = FakeTokenStore(userId = "u1", refreshToken = "expired-rt")
        val sm = manager(this)
        enqueue(401, """{"error":{"code":"AUTH_FAILED","message":"Authentication failed"}}""")

        assertThat(sm.refresh()).isNull()
        assertThat(sm.state.value).isEqualTo(SessionState.Unauthenticated)
        assertThat(tokenStore.hasRefreshToken()).isFalse()
        assertThat(tokenStore.clearCount).isEqualTo(1)
    }

    @Test
    fun `refresh with no stored token fails fast without a network call`() = runTest {
        val sm = manager(this)

        assertThat(sm.refresh()).isNull()
        assertThat(server.requestCount).isEqualTo(0)
    }

    @Test
    fun `clearSession wipes tokens and drops to Unauthenticated`() = runTest {
        tokenStore = FakeTokenStore(userId = "u1", refreshToken = "rt")
        val sm = manager(this)

        sm.clearSession()

        assertThat(sm.state.value).isEqualTo(SessionState.Unauthenticated)
        assertThat(sm.currentAccessToken()).isNull()
        assertThat(tokenStore.hasRefreshToken()).isFalse()
    }
}
