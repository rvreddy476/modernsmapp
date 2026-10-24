package com.us.android.core.profile.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.profile.data.dto.RelationshipDto
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import mockwebserver3.Dispatcher
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
import java.util.concurrent.CopyOnWriteArrayList

/**
 * Mute against graph-service's own handlers (handler.go:1007-1060: 204, no
 * body), the one half of the old connections seam that survives Connect's
 * retirement (founder, 24 Oct 2026).
 */
class MuteApiRequestTest {

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }
    private lateinit var server: MockWebServer
    private lateinit var repository: MuteRepository
    private val requests = CopyOnWriteArrayList<RecordedRequest>()
    private val answers = mutableMapOf<String, Pair<Int, String>>()

    @Before
    fun setUp() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                requests += request
                val (code, body) = answers["${request.method} ${request.url.encodedPath}"] ?: (500 to "")
                return MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()
            }
        }
        server.start()
        val retrofit = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
        repository = MuteRepository(api = retrofit.create(MuteApi::class.java), errorMapper = ErrorMapper(json))
    }

    @After
    fun tearDown() = server.close()

    private fun request(line: String) = requests.single { "${it.method} ${it.url.encodedPath}" == line }

    private fun RecordedRequest.text() = body?.utf8().orEmpty()

    @Test
    fun `mute and unmute answer 204 with no body`() = runBlocking {
        answers["POST /v1/graph/mute"] = 204 to ""
        answers["DELETE /v1/graph/mute"] = 204 to ""

        assertThat(repository.mute("u-1")).isInstanceOf(AppResult.Success::class.java)
        assertThat(repository.unmute("u-1")).isInstanceOf(AppResult.Success::class.java)
        assertThat(request("POST /v1/graph/mute").text()).isEqualTo("""{"user_id":"u-1"}""")
        assertThat(request("DELETE /v1/graph/mute").text()).isEqualTo("""{"user_id":"u-1"}""")
    }

    @Test
    fun `a refused mute is a failure`() = runBlocking {
        answers["POST /v1/graph/mute"] = 429 to """{"error":{"code":"RATE_LIMITED","message":"rate limited"}}"""

        assertThat(repository.mute("u-1")).isInstanceOf(AppResult.Failure::class.java)
    }

    @Test
    fun `the relationship reads the mute, and the retired connection keys are ignored`() {
        // GET /v1/graph/relationship still carries connection_status / is_connection
        // for old data (store/graph.go:34-70); nothing on Android reads them now.
        val dto = json.decodeFromString(
            RelationshipDto.serializer(),
            """{"follows":true,"connection_status":"pending_received","is_connection":true,"is_muted":true}""",
        )
        val relationship = dto.toDomain()

        assertThat(relationship.isMuted).isTrue()
        assertThat(RelationshipDto::class.java.declaredFields.map { it.name })
            .containsNoneOf("isConnection", "connectionStatus")
    }
}
