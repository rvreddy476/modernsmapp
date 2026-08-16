package com.us.android.core.network

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.retry.RetryConfig
import com.us.android.core.network.retry.RetryInterceptor
import com.us.android.core.network.retry.Retryable
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.SocketEffect
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import retrofit2.http.GET
import retrofit2.http.POST

private interface RetryProbeApi {
    @GET("read")
    suspend fun read(): ApiEnvelope<ProbeDto>

    /** Not annotated: a plain write must never be replayed. */
    @POST("write")
    suspend fun write(): ApiEnvelope<ProbeDto>

    /** Opt-in: safe to replay because the server honours an idempotency key. */
    @Retryable
    @POST("idempotent-write")
    suspend fun idempotentWrite(): ApiEnvelope<ProbeDto>
}

/**
 * Acceptance tests for audit B3.
 *
 * The defect: `retryOnConnectionFailure(true)` applied to every request, so a
 * `POST /v1/auth/register` whose connection dropped mid-flight was re-sent —
 * a duplicate account attempt caught only by a unique constraint.
 */
class RetryInterceptorTest {

    private lateinit var server: MockWebServer
    private lateinit var api: RetryProbeApi
    private lateinit var errorMapper: ErrorMapper

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
    }

    // No real sleeping: the backoff schedule is asserted, not waited out.
    private val slept = mutableListOf<Long>()

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        errorMapper = ErrorMapper(json)

        val interceptor = RetryInterceptor(
            config = RetryConfig(
                maxAttempts = 3,
                baseDelayMillis = 10,
                maxDelayMillis = 40,
                totalBudgetMillis = 5_000,
            ),
            sleeper = { millis -> slept += millis },
        )

        api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(
                OkHttpClient.Builder()
                    .addInterceptor(interceptor)
                    .retryOnConnectionFailure(false)
                    .build(),
            )
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(RetryProbeApi::class.java)
    }

    @After
    fun tearDown() = server.close()

    /**
     * Drops the connection as the request starts — the transport failure that
     * OkHttp's global `retryOnConnectionFailure` used to replay blindly.
     */
    private fun enqueueDisconnect() {
        server.enqueue(
            MockResponse.Builder()
                .onRequestStart(SocketEffect.CloseSocket())
                .build(),
        )
    }

    private fun enqueueOk() {
        server.enqueue(
            MockResponse.Builder()
                .code(200)
                .body("""{"data":{"id":"1","name":"ok"}}""")
                .build(),
        )
    }

    // ── The core B3 property ────────────────────────────────────────────

    @Test
    fun `a plain POST is issued exactly once on connection failure`() = runTest {
        enqueueDisconnect()
        enqueueOk() // would be consumed by an unwanted retry

        runCatching { api.write() }

        assertThat(server.requestCount).isEqualTo(1)
    }

    @Test
    fun `a GET retries on connection failure`() = runTest {
        enqueueDisconnect()
        enqueueDisconnect()
        enqueueOk()

        val result = apiCall(errorMapper) { api.read() }

        assertThat(result).isInstanceOf(
            com.us.android.core.common.result.AppResult.Success::class.java,
        )
        assertThat(server.requestCount).isEqualTo(3)
    }

    @Test
    fun `a POST annotated Retryable does retry`() = runTest {
        enqueueDisconnect()
        enqueueOk()

        val result = apiCall(errorMapper) { api.idempotentWrite() }

        assertThat(result).isInstanceOf(
            com.us.android.core.common.result.AppResult.Success::class.java,
        )
        assertThat(server.requestCount).isEqualTo(2)
    }

    // ── Bounds ──────────────────────────────────────────────────────────

    @Test
    fun `retries are capped at maxAttempts`() = runTest {
        repeat(10) { enqueueDisconnect() }

        runCatching { api.read() }

        assertThat(server.requestCount).isEqualTo(3)
    }

    @Test
    fun `backoff grows and is jittered within the cap`() = runTest {
        repeat(10) { enqueueDisconnect() }

        runCatching { api.read() }

        assertThat(slept).hasSize(2)
        // Full jitter draws from [1, capped), so the assertion is on the
        // BOUND, not on an exact schedule — an exact schedule would mean the
        // jitter was not doing its job.
        slept.forEach { assertThat(it).isAtMost(40L) }
        slept.forEach { assertThat(it).isAtLeast(1L) }
    }

    @Test
    fun `a 503 is retried but a 500 is not`() = runTest {
        server.enqueue(MockResponse.Builder().code(503).body("{}").build())
        enqueueOk()
        apiCall(errorMapper) { api.read() }
        assertThat(server.requestCount).isEqualTo(2)

        // 500 usually means the request WAS received and failed, so repeating
        // it risks duplicating whatever partial effect it had.
        server.enqueue(MockResponse.Builder().code(500).body("{}").build())
        apiCall(errorMapper) { api.read() }
        assertThat(server.requestCount).isEqualTo(3)
    }

    @Test
    fun `a successful first attempt does not sleep`() = runTest {
        enqueueOk()

        apiCall(errorMapper) { api.read() }

        assertThat(slept).isEmpty()
        assertThat(server.requestCount).isEqualTo(1)
    }
}
