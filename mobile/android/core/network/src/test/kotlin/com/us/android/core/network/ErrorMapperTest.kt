package com.us.android.core.network

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
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
import retrofit2.http.GET

private interface ErrApi {
    @GET("x")
    suspend fun call(): ApiEnvelope<ProbeDto>
}

/**
 * The error-code contract table.
 *
 * Every assertion here keys off `error.code` or the HTTP status — never off
 * `error.message`. Message text is human-facing and will be reworded; codes
 * are the contract (PHASE_0_1_PLAN §D.1).
 */
class ErrorMapperTest {

    private lateinit var server: MockWebServer
    private lateinit var api: ErrApi
    private lateinit var mapper: ErrorMapper
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
    }

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        mapper = ErrorMapper(json)
        api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(ErrApi::class.java)
    }

    @After
    fun tearDown() = server.close()

    private suspend fun failureFor(code: Int, body: String): AppError {
        server.enqueue(MockResponse.Builder().code(code).body(body).build())
        return (apiCall(mapper) { api.call() } as AppResult.Failure).error
    }

    @Test
    fun `400 INVALID_REQUEST carries message and field errors`() = runTest {
        val error = failureFor(
            400,
            """{"error":{"code":"INVALID_REQUEST","message":"bad email",""" +
                """"details":{"email":"must be a valid address"}},"meta":{"request_id":"r1"}}""",
        )

        assertThat(error).isInstanceOf(AppError.InvalidRequest::class.java)
        val invalid = error as AppError.InvalidRequest
        assertThat(invalid.message).isEqualTo("bad email")
        assertThat(invalid.fieldErrors["email"]).isEqualTo("must be a valid address")
        assertThat(invalid.requestId).isEqualTo("r1")
    }

    @Test
    fun `401 AUTH_FAILED maps to AuthFailed`() = runTest {
        val error = failureFor(
            401,
            """{"error":{"code":"AUTH_FAILED","message":"Authentication failed"}}""",
        )
        assertThat(error).isInstanceOf(AppError.AuthFailed::class.java)
    }

    @Test
    fun `403 EMAIL_NOT_VERIFIED keeps its code AND its verification token`() = runTest {
        // The single most important mapping in the file. This 403 is not
        // terminal: `details.verification_token` is the only way a user who
        // closed the app mid-signup can finish signing up. A client that
        // discards error bodies strands the account.
        val error = failureFor(
            403,
            """{"error":{"code":"EMAIL_NOT_VERIFIED","message":"email not verified",""" +
                """"details":{"verification_token":"vt-abc123",""" +
                """"verify_via":"POST /v1/auth/verify-email"}},"meta":{"request_id":"r2"}}""",
        )

        assertThat(error).isInstanceOf(AppError.Forbidden::class.java)
        val forbidden = error as AppError.Forbidden
        assertThat(forbidden.code).isEqualTo("EMAIL_NOT_VERIFIED")
        assertThat(forbidden.details["verification_token"]).isEqualTo("vt-abc123")
    }

    @Test
    fun `404 maps to NotFound`() = runTest {
        assertThat(failureFor(404, """{"error":{"code":"NOT_FOUND","message":"no"}}"""))
            .isInstanceOf(AppError.NotFound::class.java)
    }

    @Test
    fun `429 carries Retry-After`() = runTest {
        server.enqueue(
            MockResponse.Builder()
                .code(429)
                .addHeader("Retry-After", "42")
                .body("""{"error":{"code":"RATE_LIMITED","message":"slow down"}}""")
                .build(),
        )
        val error = (apiCall(mapper) { api.call() } as AppResult.Failure).error
        assertThat((error as AppError.RateLimited).retryAfterSeconds).isEqualTo(42L)
    }

    @Test
    fun `500 maps to Server with the status code`() = runTest {
        val error = failureFor(500, """{"error":{"code":"INTERNAL","message":"boom"}}""")
        val server500 = error as AppError.Server
        assertThat(server500.statusCode).isEqualTo(500)
        assertThat(server500.code).isEqualTo("INTERNAL")
    }

    @Test
    fun `an unmodelled code is preserved rather than flattened`() = runTest {
        val error = failureFor(418, """{"error":{"code":"TEAPOT","message":"short and stout"}}""")
        val unknown = error as AppError.Unknown
        assertThat(unknown.code).isEqualTo("TEAPOT")
        assertThat(unknown.statusCode).isEqualTo(418)
    }

    @Test
    fun `an error body that is not our envelope still yields a typed error`() = runTest {
        // Cloudflare, nginx and the like return HTML. The client must not
        // crash on it — this is exactly what the Flutter build hit when
        // pointed at an Access-protected host (finding F7 context).
        val error = failureFor(502, "<html><body>Bad Gateway</body></html>")
        assertThat((error as AppError.Server).statusCode).isEqualTo(502)
    }
}
