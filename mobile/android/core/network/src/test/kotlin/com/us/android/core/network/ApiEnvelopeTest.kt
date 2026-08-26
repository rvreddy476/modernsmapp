package com.us.android.core.network

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.Serializable
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

@Serializable
data class ProbeDto(val id: String, val name: String)

private interface ProbeApi {
    @GET("probe")
    suspend fun probe(): ApiEnvelope<ProbeDto>

    @GET("list")
    suspend fun list(): ApiEnvelope<List<ProbeDto>>
}

/**
 * Contract tests for the platform response envelope.
 *
 * The fixtures below are the real shapes emitted by
 * identity-platform/shared/api/response.go, not invented ones.
 */
class ApiEnvelopeTest {

    private lateinit var server: MockWebServer
    private lateinit var api: ProbeApi
    private lateinit var errorMapper: ErrorMapper

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
    }

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        errorMapper = ErrorMapper(json)
        api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(ProbeApi::class.java)
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(code: Int, body: String) {
        server.enqueue(MockResponse.Builder().code(code).body(body).build())
    }

    @Test
    fun `unwraps data on success`() = runTest {
        enqueue(200, """{"data":{"id":"1","name":"Raghu"},"meta":{"request_id":"req-7"}}""")

        val result = apiCall(errorMapper) { api.probe() }

        assertThat(result).isInstanceOf(AppResult.Success::class.java)
        assertThat((result as AppResult.Success).data).isEqualTo(ProbeDto("1", "Raghu"))
    }

    @Test
    fun `a 2xx carrying an error object is a failure, not a success`() = runTest {
        enqueue(
            200,
            """{"error":{"code":"WEIRD_BUT_LEGAL","message":"x"},"meta":{"request_id":"req-8"}}""",
        )

        val result = apiCall(errorMapper) { api.probe() }

        assertThat(result).isInstanceOf(AppResult.Failure::class.java)
        val error = (result as AppResult.Failure).error
        assertThat((error as AppError.Unknown).code).isEqualTo("WEIRD_BUT_LEGAL")
        assertThat(error.requestId).isEqualTo("req-8")
    }

    @Test
    fun `a 2xx with neither data nor error is malformed, not a silent null`() = runTest {
        enqueue(200, """{"meta":{"request_id":"req-9"}}""")

        val result = apiCall(errorMapper) { api.probe() }

        val error = (result as AppResult.Failure).error
        assertThat(error).isInstanceOf(AppError.Malformed::class.java)
        assertThat(error.requestId).isEqualTo("req-9")
    }

    @Test
    fun `unparseable body maps to Malformed rather than crashing`() = runTest {
        enqueue(200, "<html>gateway</html>")

        val result = apiCall(errorMapper) { api.probe() }

        assertThat(result).isInstanceOf(AppResult.Failure::class.java)
    }

    @Test
    fun `paged call folds meta next_cursor into the result`() = runTest {
        enqueue(
            200,
            """{"data":[{"id":"1","name":"a"},{"id":"2","name":"b"}],""" +
                """"meta":{"next_cursor":"eyJvIjoyfQ=="}}""",
        )

        val result = pagedApiCall(errorMapper) { api.list() }

        val page = (result as AppResult.Success).data
        assertThat(page.items).hasSize(2)
        assertThat(page.nextCursor).isEqualTo("eyJvIjoyfQ==")
        assertThat(page.hasMore).isTrue()
    }

    @Test
    fun `absent next_cursor means the last page`() = runTest {
        enqueue(200, """{"data":[{"id":"1","name":"a"}],"meta":{"request_id":"r"}}""")

        val page = (pagedApiCall(errorMapper) { api.list() } as AppResult.Success).data

        assertThat(page.nextCursor).isNull()
        assertThat(page.hasMore).isFalse()
    }

    @Test
    fun `list call treats data null as an empty list`() = runTest {
        // Captured live from GET /v1/auth/trusted-devices on an account with
        // no trusted devices: a Go nil slice marshals as `"data": null`.
        enqueue(200, """{"data":null}""")

        val result = listApiCall(errorMapper) { api.list() }

        assertThat((result as AppResult.Success).data).isEmpty()
    }

    @Test
    fun `list call treats an absent data key as an empty list`() = runTest {
        enqueue(200, """{"meta":{"request_id":"req-10"}}""")

        val result = listApiCall(errorMapper) { api.list() }

        assertThat((result as AppResult.Success).data).isEmpty()
    }

    @Test
    fun `list call still surfaces an error object as a failure`() = runTest {
        enqueue(200, """{"error":{"code":"NOPE","message":"x"},"meta":{"request_id":"req-11"}}""")

        val result = listApiCall(errorMapper) { api.list() }

        val error = (result as AppResult.Failure).error
        assertThat((error as AppError.Unknown).code).isEqualTo("NOPE")
        assertThat(error.requestId).isEqualTo("req-11")
    }

    @Test
    fun `an empty page is a success, not an error`() = runTest {
        enqueue(200, """{"data":[],"meta":{}}""")

        val page = (pagedApiCall(errorMapper) { api.list() } as AppResult.Success).data

        assertThat(page.items).isEmpty()
        assertThat(page.hasMore).isFalse()
    }
}
