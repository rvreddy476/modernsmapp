package com.us.android.core.network

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.cookie.CsrfCookieStore
import com.us.android.core.network.interceptor.AuthInterceptor
import com.us.android.core.network.interceptor.ClientHeadersInterceptor
import com.us.android.core.network.interceptor.CsrfInterceptor
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.junit.After
import org.junit.Before
import org.junit.Test

/**
 * The wire contract from PHASE_0_1_PLAN §D.4.
 *
 * These assertions are about what the server actually sees, which is why they
 * go through a real OkHttp client and MockWebServer rather than unit-testing
 * the interceptors in isolation.
 */
class HeaderContractTest {

    private lateinit var server: MockWebServer
    private lateinit var cookieStore: CsrfCookieStore
    private var token: String? = "access-token-1"

    private val config = ApiConfig(
        baseUrl = "http://localhost",
        wsBaseUrl = "ws://localhost",
        clientVersion = "0.1.0",
        environment = "dev",
        isDebug = true,
    )

    private lateinit var client: OkHttpClient

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        cookieStore = CsrfCookieStore()
        client = OkHttpClient.Builder()
            .cookieJar(cookieStore)
            .addInterceptor(ClientHeadersInterceptor(config))
            .addInterceptor(
                AuthInterceptor(
                    object : TokenProvider {
                        override fun currentAccessToken(): String? = token
                    },
                ),
            )
            .addInterceptor(CsrfInterceptor(cookieStore))
            .build()
    }

    @After
    fun tearDown() = server.close()

    private fun get(path: String = "/v1/feed/home") {
        server.enqueue(MockResponse.Builder().code(200).body("{}").build())
        client.newCall(Request.Builder().url(server.url(path)).build()).execute().close()
    }

    private fun post(path: String = "/v1/posts") {
        server.enqueue(MockResponse.Builder().code(200).body("{}").build())
        client.newCall(
            Request.Builder().url(server.url(path)).post("{}".toRequestBody()).build(),
        ).execute().close()
    }

    @Test
    fun `standard client headers are present on every request`() {
        get()
        val recorded = server.takeRequest()

        assertThat(recorded.headers["X-Requested-With"]).isEqualTo("XMLHttpRequest")
        assertThat(recorded.headers["X-Client-Platform"]).isEqualTo("android")
        assertThat(recorded.headers["X-Client-Version"]).isEqualTo("0.1.0")
        assertThat(recorded.headers["X-Request-Id"]).isNotEmpty()
        assertThat(recorded.headers["Accept"]).isEqualTo("application/json")
    }

    @Test
    fun `X-User-Id is never sent`() {
        // Modification M5. The Flutter client sends client-asserted identity
        // here. It is harmless server-side (auth middleware overwrites it
        // from the JWT) but there is no reason to put it on the wire.
        get()
        assertThat(server.takeRequest().headers["X-User-Id"]).isNull()
    }

    @Test
    fun `bearer token is attached when a session exists`() {
        get()
        assertThat(server.takeRequest().headers["Authorization"])
            .isEqualTo("Bearer access-token-1")
    }

    @Test
    fun `no Authorization header when there is no session`() {
        token = null
        get()
        assertThat(server.takeRequest().headers["Authorization"]).isNull()
    }

    @Test
    fun `token-minting endpoints never carry a stale Authorization header`() {
        post("/v1/auth/login")
        assertThat(server.takeRequest().headers["Authorization"]).isNull()

        post("/v1/auth/refresh")
        assertThat(server.takeRequest().headers["Authorization"]).isNull()
    }

    @Test
    fun `CSRF header is absent when the server has issued no cookie`() {
        // Never fabricate a token. The Flutter client generates a random one
        // client-side, which cannot match the server's cookie — that is not
        // protection, it is noise. A clean 403 is easier to diagnose than a
        // silent mismatch (modification M3).
        post()
        assertThat(server.takeRequest().headers["X-CSRF-Token"]).isNull()
    }

    @Test
    fun `CSRF header echoes the server cookie on mutating methods only`() {
        server.enqueue(
            MockResponse.Builder()
                .code(200)
                .addHeader("Set-Cookie", "csrf_token=server-issued-abc; Path=/")
                .body("{}")
                .build(),
        )
        client.newCall(Request.Builder().url(server.url("/v1/auth/login")).build())
            .execute().close()
        server.takeRequest()

        post()
        assertThat(server.takeRequest().headers["X-CSRF-Token"]).isEqualTo("server-issued-abc")

        get()
        assertThat(server.takeRequest().headers["X-CSRF-Token"]).isNull()
    }

    @Test
    fun `session cookies from the server are ignored as session state`() {
        // Bearer is authoritative (blocker B4). The server sets access and
        // refresh cookies on login; only csrf_token is ever read back.
        server.enqueue(
            MockResponse.Builder()
                .code(200)
                .addHeader("Set-Cookie", "access_token=cookie-access; Path=/")
                .addHeader("Set-Cookie", "refresh_token=cookie-refresh; Path=/")
                .addHeader("Set-Cookie", "csrf_token=csrf-1; Path=/")
                .body("{}")
                .build(),
        )
        client.newCall(Request.Builder().url(server.url("/v1/auth/login")).build())
            .execute().close()
        server.takeRequest()

        assertThat(cookieStore.csrfToken()).isEqualTo("csrf-1")

        // The bearer header still comes from the in-memory token, not a cookie.
        get()
        assertThat(server.takeRequest().headers["Authorization"])
            .isEqualTo("Bearer access-token-1")
    }
}
