package com.us.android.core.network

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.interceptor.AuthInterceptor
import kotlinx.coroutines.runBlocking
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.OkHttpClient
import okhttp3.Request
import org.junit.After
import org.junit.Before
import org.junit.Test
import java.util.concurrent.atomic.AtomicInteger

class TokenAuthenticatorTest {

    private lateinit var server: MockWebServer
    private val refreshCount = AtomicInteger(0)

    @Volatile
    private var token: String? = "stale-token"

    private val refresher = object : TokenRefresher {
        override suspend fun refresh(): String? {
            refreshCount.incrementAndGet()
            token = "fresh-token"
            return token
        }
    }

    private val provider = object : TokenProvider {
        override fun currentAccessToken(): String? = token
    }

    private lateinit var client: OkHttpClient

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        client = OkHttpClient.Builder()
            .addInterceptor(AuthInterceptor(provider, testApiConfig(server)))
            .authenticator(TokenAuthenticator(refresher))
            .build()
    }

    @After
    fun tearDown() = server.close()

    @Test
    fun `a 401 triggers refresh and replays the original request`() {
        server.enqueue(MockResponse.Builder().code(401).body("{}").build())
        server.enqueue(MockResponse.Builder().code(200).body("""{"data":{}}""").build())

        val response = client.newCall(
            Request.Builder().url(server.url("/v1/feed/home")).build(),
        ).execute()

        assertThat(response.code).isEqualTo(200)
        response.close()

        assertThat(refreshCount.get()).isEqualTo(1)

        val first = server.takeRequest()
        val replay = server.takeRequest()
        assertThat(first.headers["Authorization"]).isEqualTo("Bearer stale-token")
        // The replay must carry the NEW token, otherwise it just 401s again.
        assertThat(replay.headers["Authorization"]).isEqualTo("Bearer fresh-token")
        assertThat(replay.url.encodedPath).isEqualTo("/v1/feed/home")
    }

    @Test
    fun `a 401 on the refresh endpoint itself does not recurse`() {
        // Without the guard, an expired or revoked refresh token drives an
        // infinite refresh loop against the server.
        server.enqueue(MockResponse.Builder().code(401).body("{}").build())

        val response = client.newCall(
            Request.Builder().url(server.url("/v1/auth/refresh")).build(),
        ).execute()

        assertThat(response.code).isEqualTo(401)
        response.close()
        assertThat(refreshCount.get()).isEqualTo(0)
    }

    @Test
    fun `gives up rather than looping when refresh keeps yielding a rejected token`() {
        val stubbornRefresher = object : TokenRefresher {
            override suspend fun refresh(): String? = "always-the-same"
        }
        val stubbornClient = OkHttpClient.Builder()
            .addInterceptor(
                AuthInterceptor(
                    object : TokenProvider {
                        override fun currentAccessToken(): String = "always-the-same"
                    },
                    testApiConfig(server),
                ),
            )
            .authenticator(TokenAuthenticator(stubbornRefresher))
            .build()

        repeat(4) { server.enqueue(MockResponse.Builder().code(401).body("{}").build()) }

        val response = stubbornClient.newCall(
            Request.Builder().url(server.url("/v1/feed/home")).build(),
        ).execute()

        // The refreshed token equals the one that just failed, so retrying is
        // pointless — the authenticator must stop, not spin.
        assertThat(response.code).isEqualTo(401)
        response.close()
        assertThat(server.requestCount).isEqualTo(1)
    }

    @Test
    fun `a null refresh surfaces the 401 to the caller`() {
        val failingClient = OkHttpClient.Builder()
            .addInterceptor(AuthInterceptor(provider, testApiConfig(server)))
            .authenticator(
                TokenAuthenticator(object : TokenRefresher {
                    override suspend fun refresh(): String? = null
                }),
            )
            .build()

        server.enqueue(MockResponse.Builder().code(401).body("{}").build())

        val response = failingClient.newCall(
            Request.Builder().url(server.url("/v1/feed/home")).build(),
        ).execute()

        assertThat(response.code).isEqualTo(401)
        response.close()
    }

    @Test
    fun `runBlocking inside the authenticator does not deadlock`() {
        // The Authenticator contract is synchronous while refresh is a
        // suspend function. This asserts the bridge actually completes.
        server.enqueue(MockResponse.Builder().code(401).body("{}").build())
        server.enqueue(MockResponse.Builder().code(200).body("{}").build())

        val code = runBlocking {
            client.newCall(Request.Builder().url(server.url("/x")).build())
                .execute().use { it.code }
        }
        assertThat(code).isEqualTo(200)
    }
}

/**
 * An [ApiConfig] naming the mock server as the API origin.
 *
 * AuthInterceptor attaches the bearer only to that origin, so a config that
 * pointed anywhere else would make these tests pass for the wrong reason.
 */
private fun testApiConfig(server: MockWebServer) = ApiConfig(
    baseUrl = server.url("/").toString(),
    wsBaseUrl = "ws://localhost",
    clientVersion = "test",
    environment = "test",
    isDebug = true,
)
