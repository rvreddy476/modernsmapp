package com.us.android.core.auth

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.SessionState
import com.us.android.core.network.cookie.CsrfCookieStore
import com.us.android.core.telemetry.NoOpTelemetry
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.runBlocking
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
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
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
    private lateinit var sessionApi: SessionTokenApi
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
    }

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        tokenStore = FakeTokenStore()
        val retrofit = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
        api = retrofit.create(AuthApi::class.java)
        sessionApi = retrofit.create(SessionTokenApi::class.java)
    }

    @After
    fun tearDown() = server.close()

    private fun manager(scope: CoroutineScope) = SessionManager(
        tokenStore = tokenStore,
        authApi = Provider { error("refresh and logout go through the session client") },
        cookieStore = CsrfCookieStore(),
        telemetry = NoOpTelemetry,
        scope = scope,
        sessionApi = Provider { sessionApi },
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
        val request = server.takeRequest()
        assertThat(request.target).isEqualTo("/v1/auth/refresh")
        // The field Refresh binds (handler.go:938); never a cookie (H1).
        assertThat(request.body!!.utf8()).isEqualTo("""{"refresh_token":"old-rt"}""")
        assertThat(request.headers["Cookie"]).isNull()
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
        val sm = manager(scope)

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

    /**
     * A 429 says the SERVER is busy, not that the credential is bad. The
     * regression this pins: a shared refresh limiter answered 429 and the
     * client signed the user out of a perfectly valid session.
     */
    @Test
    fun `a rate-limited refresh keeps the session`() = runTest {
        tokenStore = FakeTokenStore(userId = "u1", refreshToken = "good-rt")
        val sm = manager(this)
        enqueue(429, """{"error":{"code":"RATE_LIMITED","message":"Too many refresh attempts."}}""")

        assertThat(sm.refresh()).isNull()
        assertThat(sm.state.value.isAuthenticated).isTrue()
        assertThat(tokenStore.hasRefreshToken()).isTrue()
        assertThat(tokenStore.clearCount).isEqualTo(0)
    }

    @Test
    fun `a server error during refresh keeps the session`() = runTest {
        tokenStore = FakeTokenStore(userId = "u1", refreshToken = "good-rt")
        val sm = manager(this)
        enqueue(503, """{"error":{"code":"UNAVAILABLE","message":"try later"}}""")

        assertThat(sm.refresh()).isNull()
        assertThat(sm.state.value.isAuthenticated).isTrue()
        assertThat(tokenStore.clearCount).isEqualTo(0)
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

    // ── L4: a late 401 reuses a token that is already fresh ─────────────

    @Test
    fun `a 401 for a token that is no longer current reuses the current one without refreshing`() = runTest {
        tokenStore = FakeTokenStore(userId = "u1", refreshToken = "rt")
        val sm = manager(this)
        sm.applyAuthResponse(json.decodeFromString(session("fresh-at", "rt")))

        // The failed request carried an older token, or none at all (cold start).
        assertThat(sm.refreshIfStale("old-at")).isEqualTo("fresh-at")
        assertThat(sm.refreshIfStale(null)).isEqualTo("fresh-at")
        assertThat(server.requestCount).isEqualTo(0)
    }

    @Test
    fun `a 401 for the current token does refresh`() = runTest {
        tokenStore = FakeTokenStore(userId = "u1", refreshToken = "rt")
        val sm = manager(this)
        sm.applyAuthResponse(json.decodeFromString(session("at-1", "rt")))
        enqueue(200, """{"data":${session("at-2", "rt-2")}}""")

        assertThat(sm.refreshIfStale("at-1")).isEqualTo("at-2")
        assertThat(server.requestCount).isEqualTo(1)
    }

    // ── L5: the session generation ──────────────────────────────────────

    @Test
    fun `a refresh that lands after sign-out does not sign the person back in`() = runBlocking {
        tokenStore = FakeTokenStore(userId = "u1", refreshToken = "rt")
        val gate = HeldResponse(200, """{"data":${session("resurrected-at", "resurrected-rt")}}""")
        server.dispatcher = gate
        val sm = manager(CoroutineScope(Dispatchers.IO))

        val pending = async(Dispatchers.IO) { sm.refresh() }
        gate.awaitReceived()
        sm.clearSession() // sign-out while the refresh is on the wire
        gate.release()

        assertThat(pending.await()).isNull()
        assertThat(sm.state.value).isEqualTo(SessionState.Unauthenticated)
        assertThat(sm.currentAccessToken()).isNull()
        assertThat(tokenStore.hasRefreshToken()).isFalse()
    }

    @Test
    fun `an old refresh rejected after a new sign-in does not wipe the new session`() = runBlocking {
        tokenStore = FakeTokenStore(userId = "u1", refreshToken = "old-rt")
        val gate = HeldResponse(401, """{"error":{"code":"AUTH_FAILED","message":"Authentication failed"}}""")
        server.dispatcher = gate
        val sm = manager(CoroutineScope(Dispatchers.IO))

        val pending = async(Dispatchers.IO) { sm.refresh() }
        gate.awaitReceived()
        // The person signs in again (say as someone else) before the old answer arrives.
        sm.applyAuthResponse(json.decodeFromString(session("new-at", "new-rt", userId = "u2")))
        gate.release()

        assertThat(pending.await()).isNull()
        assertThat(sm.state.value).isEqualTo(SessionState.Authenticated("u2", "s1"))
        assertThat(sm.currentAccessToken()).isEqualTo("new-at")
        assertThat(tokenStore.readRefreshToken()).isEqualTo("new-rt")
        assertThat(tokenStore.clearCount).isEqualTo(0)
    }

    @Test
    fun `an old refresh's success after a new sign-in does not overwrite the new tokens`() = runBlocking {
        tokenStore = FakeTokenStore(userId = "u1", refreshToken = "old-rt")
        val gate = HeldResponse(200, """{"data":${session("old-session-at", "old-session-rt")}}""")
        server.dispatcher = gate
        val sm = manager(CoroutineScope(Dispatchers.IO))

        val pending = async(Dispatchers.IO) { sm.refresh() }
        gate.awaitReceived()
        sm.applyAuthResponse(json.decodeFromString(session("new-at", "new-rt", userId = "u2")))
        gate.release()

        assertThat(pending.await()).isNull()
        assertThat(sm.currentAccessToken()).isEqualTo("new-at")
        assertThat(tokenStore.readRefreshToken()).isEqualTo("new-rt")
        assertThat(tokenStore.userId).isEqualTo("u2")
    }

    @Test
    fun `a refresh asked for after a new sign-in does not join the old session's refresh`() = runBlocking {
        tokenStore = FakeTokenStore(userId = "u1", refreshToken = "old-rt")
        val gate = HeldResponse(200, """{"data":${session("next-at", "next-rt", userId = "u2")}}""")
        server.dispatcher = gate
        val sm = manager(CoroutineScope(Dispatchers.IO))

        val old = async(Dispatchers.IO) { sm.refresh() }
        gate.awaitReceived()
        sm.applyAuthResponse(json.decodeFromString(session("new-at", "new-rt", userId = "u2")))
        val fresh = async(Dispatchers.IO) { sm.refresh() }
        // Its own request, not a seat on the old one (which waits to be discarded).
        gate.awaitSecondRequest()
        gate.release()

        assertThat(old.await()).isNull()
        assertThat(fresh.await()).isEqualTo("next-at")
        assertThat(sm.currentAccessToken()).isEqualTo("next-at")
    }

    // ── Server-side logout (L8 / H1) ────────────────────────────────────

    @Test
    fun `logout revokes server-side with the refresh token in the body`() = runTest {
        tokenStore = FakeTokenStore(userId = "u1", refreshToken = "rt-to-revoke")
        val sm = manager(this)
        enqueue(200, """{"data":{"status":"ok"}}""")

        sm.revokeServerSession()

        val request = server.takeRequest()
        assertThat(request.target).isEqualTo("/v1/auth/logout")
        assertThat(request.body!!.utf8()).isEqualTo("""{"refresh_token":"rt-to-revoke"}""")
    }

    @Test
    fun `logout with nothing to revoke makes no call, and a failing one never throws`() = runTest {
        val sm = manager(this)
        sm.revokeServerSession()
        assertThat(server.requestCount).isEqualTo(0)

        tokenStore = FakeTokenStore(userId = "u1", refreshToken = "rt")
        val again = manager(this)
        enqueue(500, """{"error":{"code":"INTERNAL_ERROR","message":"boom"}}""")
        again.revokeServerSession()
        assertThat(server.requestCount).isEqualTo(1)
    }

    // ── Proactive refresh (H2) ──────────────────────────────────────────

    @Test
    fun `a cold start with a stored session asks for a token before the first request`() = runTest {
        assertThat(manager(this).shouldRefreshBeforeRequest()).isFalse()

        tokenStore = FakeTokenStore(userId = "u1", refreshToken = "rt")
        assertThat(manager(this).shouldRefreshBeforeRequest()).isTrue()
    }

    @Test
    fun `a fresh token is used as is until 80 percent of its life, measured from adoption`() = runTest {
        tokenStore = FakeTokenStore(userId = "u1", refreshToken = "rt")
        val sm = manager(this)
        var elapsed = 5_000L
        sm.elapsedClock = { elapsed }
        sm.wallClock = { java.time.Instant.parse("2026-08-16T12:00:00Z").toEpochMilli() }
        // Expires in 15 minutes by the server's clock.
        sm.applyAuthResponse(json.decodeFromString(session("at", "rt", expiresAt = "2026-08-16T12:15:00Z")))

        assertThat(sm.shouldRefreshBeforeRequest()).isFalse()
        elapsed += 11 * 60_000L
        assertThat(sm.shouldRefreshBeforeRequest()).isFalse()
        elapsed += 60_000L
        assertThat(sm.shouldRefreshBeforeRequest()).isTrue()
    }

    @Test
    fun `a device clock far ahead of the server does not make every request refresh`() = runTest {
        tokenStore = FakeTokenStore(userId = "u1", refreshToken = "rt")
        val sm = manager(this)
        var elapsed = 0L
        sm.elapsedClock = { elapsed }
        // The device thinks the token expired an hour ago.
        sm.wallClock = { java.time.Instant.parse("2026-08-16T13:15:00Z").toEpochMilli() }
        sm.applyAuthResponse(json.decodeFromString(session("at", "rt", expiresAt = "2026-08-16T12:15:00Z")))

        assertThat(sm.shouldRefreshBeforeRequest()).isFalse()
        elapsed += 12 * 60_000L
        assertThat(sm.shouldRefreshBeforeRequest()).isTrue()
    }

    @Test
    fun `after a transient refresh failure requests stop refreshing up front for a while`() = runTest {
        tokenStore = FakeTokenStore(userId = "u1", refreshToken = "rt")
        val sm = manager(this)
        var elapsed = 1_000L
        sm.elapsedClock = { elapsed }
        enqueue(503, """{"error":{"code":"UNAVAILABLE","message":"try later"}}""")

        assertThat(sm.refresh()).isNull()

        assertThat(sm.shouldRefreshBeforeRequest()).isFalse()
        elapsed += 16_000L
        assertThat(sm.shouldRefreshBeforeRequest()).isTrue()
    }

    private fun session(
        accessToken: String,
        refreshToken: String,
        userId: String = "u1",
        expiresAt: String = "2026-08-16T12:00:00Z",
    ) = """{"tokens":{"access_token":"$accessToken","refresh_token":"$refreshToken",""" +
        """"expires_at":"$expiresAt"},"user":{"id":"$userId"},"session_id":"s1"}"""

    /** Answers once the test says so, so a test can act while the request is on the wire. */
    private class HeldResponse(private val code: Int, private val body: String) : mockwebserver3.Dispatcher() {
        private val received = CountDownLatch(1)
        private val secondReceived = CountDownLatch(2)
        private val released = CountDownLatch(1)

        override fun dispatch(request: mockwebserver3.RecordedRequest): MockResponse {
            received.countDown()
            secondReceived.countDown()
            released.await(WAIT_SECONDS, TimeUnit.SECONDS)
            return MockResponse.Builder().code(code).body(body).build()
        }

        fun awaitReceived() = check(received.await(WAIT_SECONDS, TimeUnit.SECONDS)) { "refresh never arrived" }

        /** Waits (bounded) for a second request; a caller that joined the first never sends one. */
        fun awaitSecondRequest() {
            secondReceived.await(SECOND_REQUEST_WAIT_SECONDS, TimeUnit.SECONDS)
        }

        fun release() = released.countDown()
    }

    private companion object {
        const val WAIT_SECONDS = 10L
        const val SECOND_REQUEST_WAIT_SECONDS = 3L
    }
}
