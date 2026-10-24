package com.us.android.core.profile.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.ConnectionStatus
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
 * Connections (2026-10-24) against graph-service's own handlers, every answer
 * shaped as the Go code writes it:
 *
 *  - SendConnectionRequest (internal/http/handler.go:764-800): body
 *    `{user_id, source, message}`, answer `{"data":{"status":"request_sent"}}`.
 *  - Accept / Decline (handler.go:802-850): body `{user_id: <sender>}`.
 *  - Cancel (handler.go:854-865) and RemoveConnection (handler.go:867-877, a
 *    DELETE with a body) read `{user_id}` through parseAuthAndBody (handler.go:319).
 *  - GetPendingConnectionRequests / GetSentConnectionRequests (handler.go:894-925):
 *    `store.ConnectionRequest` rows (store/graph.go:113-122).
 *  - GetConnections (handler.go:879-892): a bare array of ids.
 *  - Mute / Unmute (handler.go:1007-1060): 204, no body.
 */
class ConnectionsApiRequestTest {

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }
    private lateinit var server: MockWebServer
    private lateinit var repository: ConnectionsRepository
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
        repository = ConnectionsRepository(
            api = retrofit.create(ConnectionsApi::class.java),
            profiles = retrofit.create(FollowListApi::class.java),
            errorMapper = ErrorMapper(json),
        )
    }

    @After
    fun tearDown() = server.close()

    private fun request(line: String) = requests.single { "${it.method} ${it.url.encodedPath}" == line }

    private fun RecordedRequest.text() = body?.utf8().orEmpty()

    @Test
    fun `send posts the target and the profile source, and nothing else`() = runBlocking {
        answers["POST /v1/graph/connection-request"] = 200 to """{"data":{"status":"request_sent"}}"""

        val result = repository.send("u-2")

        assertThat(result).isInstanceOf(AppResult.Success::class.java)
        assertThat(request("POST /v1/graph/connection-request").text())
            .isEqualTo("""{"user_id":"u-2","source":"profile"}""")
    }

    @Test
    fun `accept and decline name the SENDER, cancel names the receiver`() = runBlocking {
        answers["POST /v1/graph/connection-request/accept"] = 200 to """{"data":{"status":"accepted"}}"""
        answers["POST /v1/graph/connection-request/decline"] = 200 to """{"data":{"status":"declined"}}"""
        answers["POST /v1/graph/connection-request/cancel"] = 200 to """{"data":{"status":"cancelled"}}"""

        repository.accept("sender-1")
        repository.decline("sender-2")
        repository.cancel("receiver-3")

        assertThat(request("POST /v1/graph/connection-request/accept").text()).isEqualTo("""{"user_id":"sender-1"}""")
        assertThat(request("POST /v1/graph/connection-request/decline").text()).isEqualTo("""{"user_id":"sender-2"}""")
        assertThat(request("POST /v1/graph/connection-request/cancel").text()).isEqualTo("""{"user_id":"receiver-3"}""")
    }

    @Test
    fun `remove is a DELETE that carries the id in its body`() = runBlocking {
        answers["DELETE /v1/graph/connection"] = 200 to """{"data":{"status":"removed"}}"""

        val result = repository.remove("u-9")

        assertThat(result).isInstanceOf(AppResult.Success::class.java)
        assertThat(request("DELETE /v1/graph/connection").text()).isEqualTo("""{"user_id":"u-9"}""")
    }

    @Test
    fun `a refused write is a failure with the server's code`() = runBlocking {
        answers["POST /v1/graph/connection-request"] = 429 to
            """{"error":{"code":"RATE_LIMITED","message":"rate limited"}}"""

        val result = repository.send("u-2")

        assertThat(result).isInstanceOf(AppResult.Failure::class.java)
    }

    @Test
    fun `received requests are the senders, named from the profiles batch`() = runBlocking {
        answers["GET /v1/graph/connection-requests"] = 200 to """{"data":[
            {"sender_id":"s-1","receiver_id":"me","status":"pending","source":"profile","message":"hi","created_at":"2026-10-24T10:00:00Z","updated_at":"2026-10-24T10:00:00Z","expires_at":"2026-11-23T10:00:00Z"},
            {"sender_id":"s-2","receiver_id":"me","status":"pending","source":"","created_at":"2026-10-23T10:00:00Z","updated_at":"2026-10-23T10:00:00Z","expires_at":"2026-11-22T10:00:00Z"}
        ]}"""
        answers["POST /v1/profiles/batch"] = 200 to
            """{"s-1":{"user_id":"s-1","username":"ana","display_name":"Ana","avatar_media_id":"m-1"}}"""

        val rows = (repository.requests(ConnectionRequestDirection.RECEIVED) as AppResult.Success).data

        assertThat(rows.map { it.userId }).containsExactly("s-1", "s-2").inOrder()
        assertThat(rows[0].displayName).isEqualTo("Ana")
        assertThat(rows[0].avatarMediaId).isEqualTo("m-1")
        assertThat(rows[0].message).isEqualTo("hi")
        // Left out of the batch: still a row, still answerable, just nameless.
        assertThat(rows[1].displayName).isEmpty()
        assertThat(request("POST /v1/profiles/batch").text()).isEqualTo("""{"user_ids":["s-1","s-2"]}""")
    }

    @Test
    fun `sent requests are the receivers`() = runBlocking {
        answers["GET /v1/graph/connection-requests/sent"] = 200 to """{"data":[
            {"sender_id":"me","receiver_id":"r-1","status":"pending","source":"profile","created_at":"2026-10-24T10:00:00Z"}
        ]}"""
        answers["POST /v1/profiles/batch"] = 200 to """{"r-1":{"user_id":"r-1","username":"rae"}}"""

        val rows = (repository.requests(ConnectionRequestDirection.SENT) as AppResult.Success).data

        assertThat(rows.single().userId).isEqualTo("r-1")
        // No display name: the handle stands in.
        assertThat(rows.single().displayName).isEqualTo("rae")
    }

    @Test
    fun `a failed profiles batch keeps the request rows`() = runBlocking {
        answers["GET /v1/graph/connection-requests"] = 200 to
            """{"data":[{"sender_id":"s-1","receiver_id":"me","status":"pending","created_at":"x"}]}"""

        val rows = (repository.requests(ConnectionRequestDirection.RECEIVED) as AppResult.Success).data

        assertThat(rows.single().userId).isEqualTo("s-1")
    }

    @Test
    fun `the pending senders are read without asking for names, and null data is none`() = runBlocking {
        answers["GET /v1/graph/connection-requests"] = 200 to
            """{"data":[{"sender_id":"s-1","receiver_id":"me"},{"sender_id":"s-2","receiver_id":"me"}]}"""

        val ids = (repository.pendingSenderIds() as AppResult.Success).data

        assertThat(ids).containsExactly("s-1", "s-2")
        assertThat(requests.none { it.url.encodedPath == "/v1/profiles/batch" }).isTrue()

        answers["GET /v1/graph/connection-requests"] = 200 to """{"data":null}"""
        assertThat((repository.pendingSenderIds() as AppResult.Success).data).isEmpty()
    }

    @Test
    fun `no requests is an empty list, and asks for no names`() = runBlocking {
        answers["GET /v1/graph/connection-requests"] = 200 to """{"data":[]}"""

        val rows = (repository.requests(ConnectionRequestDirection.RECEIVED) as AppResult.Success).data

        assertThat(rows).isEmpty()
        assertThat(requests.none { it.url.encodedPath == "/v1/profiles/batch" }).isTrue()
    }

    @Test
    fun `connections are a bare id array and a person the batch left out leaves the list`() = runBlocking {
        answers["GET /v1/graph/connections/owner"] = 200 to """{"data":["c-1","c-2"]}"""
        answers["POST /v1/profiles/batch"] = 200 to """{"c-1":{"user_id":"c-1","username":"cy","display_name":"Cy"}}"""

        val rows = (repository.connections("owner") as AppResult.Success).data

        assertThat(rows.map { it.userId }).containsExactly("c-1")
        assertThat(request("GET /v1/graph/connections/owner").url.queryParameter("limit")).isEqualTo("50")
        assertThat(request("GET /v1/graph/connections/owner").url.queryParameter("offset")).isEqualTo("0")
    }

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
    fun `the relationship reads the connection and the mute`() {
        // GET /v1/graph/relationship answers store.Relationship (store/graph.go:34-70).
        val dto = json.decodeFromString(
            RelationshipDto.serializer(),
            """{"follows":false,"connection_status":"pending_received","is_connection":false,"is_muted":true}""",
        )
        val relationship = dto.toDomain()

        assertThat(relationship.connectionStatus).isEqualTo(ConnectionStatus.PENDING_RECEIVED)
        assertThat(relationship.isMuted).isTrue()
    }

    @Test
    fun `connection_status maps every wire value, and is_connection wins`() {
        assertThat(ConnectionStatus.fromWire("none")).isEqualTo(ConnectionStatus.NONE)
        assertThat(ConnectionStatus.fromWire("pending_sent")).isEqualTo(ConnectionStatus.PENDING_SENT)
        assertThat(ConnectionStatus.fromWire("pending_received")).isEqualTo(ConnectionStatus.PENDING_RECEIVED)
        assertThat(ConnectionStatus.fromWire("accepted")).isEqualTo(ConnectionStatus.CONNECTED)
        assertThat(ConnectionStatus.fromWire("", isConnection = true)).isEqualTo(ConnectionStatus.CONNECTED)
        assertThat(ConnectionStatus.fromWire(null)).isEqualTo(ConnectionStatus.NONE)
    }

    @Test
    fun `allowing connection requests restores a narrower audience, never widens it`() {
        assertThat(connectionRequestAudience(allow = false, original = "everyone")).isEqualTo("no_one")
        assertThat(connectionRequestAudience(allow = true, original = "no_one")).isEqualTo("everyone")
        assertThat(connectionRequestAudience(allow = true, original = "friends_of_friends")).isEqualTo("friends_of_friends")
        assertThat(connectionRequestAudience(allow = true, original = "contacts_only")).isEqualTo("contacts_only")
    }
}
