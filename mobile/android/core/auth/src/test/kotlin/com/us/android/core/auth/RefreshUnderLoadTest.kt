package com.us.android.core.auth

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiConfig
import com.us.android.core.network.TokenAuthenticator
import com.us.android.core.network.cookie.CsrfCookieStore
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.network.interceptor.AuthInterceptor
import com.us.android.core.network.interceptor.ClientHeadersInterceptor
import com.us.android.core.network.interceptor.CsrfInterceptor
import com.us.android.core.network.interceptor.ProactiveRefreshInterceptor
import com.us.android.core.network.interceptor.TracingInterceptor
import com.us.android.core.network.retry.RetryConfig
import com.us.android.core.network.retry.RetryInterceptor
import com.us.android.core.telemetry.NoOpTelemetry
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okhttp3.Call
import okhttp3.Callback
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import org.junit.After
import org.junit.Before
import org.junit.Test
import java.io.IOException
import java.util.Collections
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import javax.inject.Provider

/**
 * H1 + H2 (audit 2026-10-29), through the REAL client stack: the clients and
 * Retrofit come from [NetworkModule]'s own providers, the interceptors and the
 * authenticator are the production classes, and [SessionManager] is wired the
 * way Hilt wires it. Only the server is fake, and it behaves like ours:
 *
 *  - feed routes answer 401 unless the bearer is the current access token;
 *  - `/v1/auth/refresh` answers 401 if the request carries an `access_token`
 *    cookie (the gateway verifies that cookie when there is no bearer and
 *    refuses an expired one before routing), else rotates the pair, and it is
 *    slow, so every 401'd call is parked in the authenticator before it ends.
 *
 * More concurrent calls than OkHttp's five per host: with refresh queued on
 * the authenticated client's own dispatcher, the parked calls hold every slot
 * and the refresh never starts, which these tests see as a timeout.
 */
class RefreshUnderLoadTest {

    private lateinit var server: MockWebServer
    private lateinit var client: OkHttpClient
    private lateinit var sessionClient: OkHttpClient
    private lateinit var sessionManager: SessionManager
    private lateinit var tokenStore: FakeTokenStore
    private val cookieStore = CsrfCookieStore()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val json = NetworkModule.provideJson()

    private val refreshes = AtomicInteger(0)
    private val unauthorized = AtomicInteger(0)
    private val refreshCookieHeaders: MutableList<String> = Collections.synchronizedList(mutableListOf())

    private val currentAccessToken = "at-fresh"

    @Before
    fun setUp() {
        server = MockWebServer()
        server.dispatcher = FakeApi()
        server.start()
        val config = ApiConfig(
            baseUrl = server.url("/").toString(),
            wsBaseUrl = "ws://localhost",
            clientVersion = "test",
            environment = "test",
            isDebug = false,
        )
        tokenStore = FakeTokenStore(userId = "u1", refreshToken = "rt-0")
        val logging = NetworkModule.provideLoggingInterceptor(config)
        val headers = ClientHeadersInterceptor(config)
        sessionClient = NetworkModule.provideSessionOkHttpClient(headers, logging, cookieStore)
        val sessionRetrofit = NetworkModule.provideSessionRetrofit(sessionClient, config, json)

        lateinit var authenticatedApi: AuthApi
        sessionManager = SessionManager(
            tokenStore = tokenStore,
            // The authenticated client's AuthApi, as Hilt provides it. Refresh
            // must NOT go through it.
            authApi = Provider { authenticatedApi },
            cookieStore = cookieStore,
            telemetry = NoOpTelemetry,
            scope = scope,
            sessionApi = Provider { sessionRetrofit.create(SessionTokenApi::class.java) },
        )
        client = NetworkModule.provideOkHttpClient(
            clientHeadersInterceptor = headers,
            authInterceptor = AuthInterceptor(sessionManager, config),
            csrfInterceptor = CsrfInterceptor(cookieStore),
            retryInterceptor = RetryInterceptor(RetryConfig()),
            tracingInterceptor = TracingInterceptor(NoOpTelemetry),
            loggingInterceptor = logging,
            authenticator = TokenAuthenticator(sessionManager),
            cookieJar = cookieStore,
            proactiveRefreshInterceptor = ProactiveRefreshInterceptor(sessionManager, sessionManager, config),
        )
        authenticatedApi = NetworkModule.provideRetrofit(client, config, json).create(AuthApi::class.java)
    }

    @After
    fun tearDown() {
        // A failed run leaves calls parked in the authenticator; interrupt them
        // so the test JVM can exit.
        client.dispatcher.cancelAll()
        client.dispatcher.executorService.shutdownNow()
        sessionClient.dispatcher.executorService.shutdownNow()
        scope.cancel()
        server.close()
    }

    @Test
    fun `eight concurrent 401s in a live process all recover with exactly one refresh`() {
        // A live session whose access token has just expired server-side.
        sessionManager.applyAuthResponse(json.decodeFromString(session("at-expired", "rt-0")))
        // The sign-in also set the session cookies, as auth-service always does.
        call(post("/v1/auth/login")).use { assertThat(it.code).isEqualTo(200) }

        val codes = concurrently(CALLS) { i -> get("/v1/feed/home?page=$i") }

        assertThat(codes).hasSize(CALLS)
        assertThat(codes.toSet()).containsExactly(200)
        assertThat(refreshes.get()).isEqualTo(1)
        // The refresh carried no access_token cookie (H1): the jar kept csrf_token only.
        assertThat(refreshCookieHeaders.single()).doesNotContain("access_token")
        assertThat(refreshCookieHeaders.single()).doesNotContain("refresh_token")
        assertThat(sessionManager.state.value.isAuthenticated).isTrue()
        assertThat(tokenStore.readRefreshToken()).isEqualTo("rt-1")
    }

    @Test
    fun `a cold start mints one token before its first requests and none of them 401`() {
        // Process death: refresh token on disk, no access token in memory.
        val codes = concurrently(CALLS) { i -> get("/v1/feed/home?page=$i") }

        assertThat(codes.toSet()).containsExactly(200)
        assertThat(refreshes.get()).isEqualTo(1)
        assertThat(unauthorized.get()).isEqualTo(0)

        // The refresh's csrf_token landed in the shared jar, so writes work.
        call(post("/v1/posts")).use { assertThat(it.code).isEqualTo(200) }
        assertThat(postCsrf).isEqualTo("csrf-1")
    }

    private fun session(accessToken: String, refreshToken: String) =
        """{"tokens":{"access_token":"$accessToken","refresh_token":"$refreshToken",""" +
            """"expires_at":"2099-01-01T00:00:00Z"},"user":{"id":"u1"},"session_id":"s1"}"""

    private fun get(path: String): Request = Request.Builder().url(server.url(path)).build()

    private fun post(path: String): Request =
        Request.Builder().url(server.url(path)).post("{}".toRequestBody()).build()

    private fun call(request: Request): Response = client.newCall(request).execute()

    /** Enqueues [count] calls at once (async, so the dispatcher's per-host limit applies) and waits. */
    private fun concurrently(count: Int, request: (Int) -> Request): List<Int> {
        val done = CountDownLatch(count)
        val codes = Collections.synchronizedList(mutableListOf<Int>())
        repeat(count) { i ->
            client.newCall(request(i)).enqueue(
                object : Callback {
                    override fun onFailure(call: Call, e: IOException) = done.countDown()

                    override fun onResponse(call: Call, response: Response) {
                        response.use { codes += it.code }
                        done.countDown()
                    }
                },
            )
        }
        val finished = done.await(DEADLINE_SECONDS, TimeUnit.SECONDS)
        assertThat(finished).isTrue()
        return codes.toList()
    }

    @Volatile
    private var postCsrf: String? = null

    private inner class FakeApi : Dispatcher() {
        override fun dispatch(request: RecordedRequest): MockResponse {
            val path = request.url.encodedPath
            return when {
                path == "/v1/auth/login" -> MockResponse.Builder().code(200)
                    .addHeader("Set-Cookie", "access_token=at-expired; Path=/; HttpOnly")
                    .addHeader("Set-Cookie", "refresh_token=rt-0; Path=/; HttpOnly")
                    .addHeader("Set-Cookie", "csrf_token=csrf-0; Path=/")
                    .body("""{"data":{}}""").build()

                path == "/v1/auth/refresh" -> refresh(request)

                path == "/v1/posts" -> {
                    postCsrf = request.headers["X-CSRF-Token"]
                    feed(request)
                }

                else -> feed(request)
            }
        }

        private fun refresh(request: RecordedRequest): MockResponse {
            refreshes.incrementAndGet()
            val cookies = request.headers["Cookie"].orEmpty()
            refreshCookieHeaders += cookies
            // The gateway: no bearer, so it verifies the access_token cookie,
            // and an expired one is a 401 before auth-service is reached.
            if (cookies.contains("access_token=")) return unauthorized()
            if (request.body?.utf8() != """{"refresh_token":"rt-0"}""") return unauthorized()
            Thread.sleep(REFRESH_DELAY_MILLIS)
            return MockResponse.Builder().code(200)
                .addHeader("Set-Cookie", "access_token=$currentAccessToken; Path=/; HttpOnly")
                .addHeader("Set-Cookie", "refresh_token=rt-1; Path=/; HttpOnly")
                .addHeader("Set-Cookie", "csrf_token=csrf-1; Path=/")
                .body("""{"data":${session(currentAccessToken, "rt-1")}}""")
                .build()
        }

        private fun feed(request: RecordedRequest): MockResponse {
            if (request.headers["Authorization"] != "Bearer $currentAccessToken") {
                unauthorized.incrementAndGet()
                return unauthorized()
            }
            return MockResponse.Builder().code(200).body("""{"data":{}}""").build()
        }

        private fun unauthorized() = MockResponse.Builder().code(401)
            .body("""{"error":{"code":"AUTH_FAILED","message":"Authentication failed"}}""").build()
    }

    private companion object {
        const val CALLS = 8
        const val REFRESH_DELAY_MILLIS = 400L
        const val DEADLINE_SECONDS = 30L
    }
}
