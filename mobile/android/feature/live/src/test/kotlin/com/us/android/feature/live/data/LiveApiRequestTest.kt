package com.us.android.feature.live.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.network.noContentApiCall
import kotlinx.coroutines.runBlocking
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

/**
 * live-service-v2's requests, byte for byte, as the live-fix contract
 * (2026-10-01, section 1) pins them: remove message, ban, unban, the full
 * moderator set, report, and the new stream fields. Coded against the
 * contract while the backend routes are built in parallel.
 */
class LiveApiRequestTest {

    private val json = NetworkModule.provideJson()
    private val errorMapper = ErrorMapper(json)
    private lateinit var server: MockWebServer
    private lateinit var api: LiveApi

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(LiveApi::class.java)
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(code: Int = 200, body: String = """{"data":{}}""") {
        server.enqueue(MockResponse.Builder().code(code).body(body).build())
    }

    private fun takeBody(): String = server.takeRequest().body?.utf8().orEmpty()

    @Test
    fun `remove message is DELETE on the message under the stream`() {
        enqueue()

        val result = runBlocking { noContentApiCall(errorMapper) { api.removeChatMessage("s1", "m9") } }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("DELETE")
        assertThat(request.target).isEqualTo("/v1/livestream/streams/s1/chat/m9")
        assertThat(result).isInstanceOf(AppResult.Success::class.java)
    }

    @Test
    fun `a 204 with no body is a success too`() {
        server.enqueue(MockResponse.Builder().code(204).build())

        val result = runBlocking { noContentApiCall(errorMapper) { api.unbanUser("s1", "u2") } }

        assertThat(result).isInstanceOf(AppResult.Success::class.java)
    }

    @Test
    fun `ban posts user_id and reason`() {
        enqueue()

        runBlocking { api.banUser("s1", BanUserRequest(userId = "u2", reason = "spam in chat")) }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/livestream/streams/s1/bans")
        assertThat(request.body?.utf8()).isEqualTo("""{"user_id":"u2","reason":"spam in chat"}""")
    }

    @Test
    fun `ban without a reason omits the key`() {
        enqueue()

        runBlocking { api.banUser("s1", BanUserRequest(userId = "u2")) }

        assertThat(takeBody()).isEqualTo("""{"user_id":"u2"}""")
    }

    @Test
    fun `unban is DELETE on the banned user`() {
        enqueue()

        runBlocking { api.unbanUser("s1", "u2") }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("DELETE")
        assertThat(request.target).isEqualTo("/v1/livestream/streams/s1/bans/u2")
    }

    @Test
    fun `moderators is PUT with the whole set, and an empty set is sent as an empty list`() {
        enqueue()
        enqueue()

        runBlocking {
            api.setModerators("s1", SetModeratorsRequest(userIds = listOf("u1", "u2")))
            api.setModerators("s1", SetModeratorsRequest(userIds = emptyList()))
        }

        val first = server.takeRequest()
        assertThat(first.method).isEqualTo("PUT")
        assertThat(first.target).isEqualTo("/v1/livestream/streams/s1/moderators")
        assertThat(first.body?.utf8()).isEqualTo("""{"user_ids":["u1","u2"]}""")
        assertThat(takeBody()).isEqualTo("""{"user_ids":[]}""")
    }

    @Test
    fun `reporting the stream sends only the reason`() {
        enqueue()

        runBlocking { api.report("s1", LiveReportRequest(reason = "spam")) }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/livestream/streams/s1/reports")
        assertThat(request.body?.utf8()).isEqualTo("""{"reason":"spam"}""")
    }

    @Test
    fun `reporting a message sends message_id and the note`() {
        enqueue()

        runBlocking { api.report("s1", LiveReportRequest(reason = "harassment", messageId = "m9", note = "rude")) }

        assertThat(takeBody()).isEqualTo("""{"reason":"harassment","message_id":"m9","note":"rude"}""")
    }

    @Test
    fun `create sends the title, and the default visibility is left to the server`() {
        enqueue(body = """{"data":{"id":"s1"}}""")

        runBlocking { api.createStream(CreateStreamRequest(title = "Hello")) }

        val request = server.takeRequest()
        assertThat(request.target).isEqualTo("/v1/livestream/streams")
        // encodeDefaults is off app-wide, so "public" is omitted; live-service-v2
        // normalises a blank visibility to public (normalizeVisibility).
        assertThat(request.body?.utf8()).isEqualTo("""{"title":"Hello"}""")
    }

    @Test
    fun `the stream decodes status, ended_reason, status_changed_at and both viewer numbers`() {
        enqueue(
            body = """{"data":{"id":"s1","status":"ended","ended_reason":"host_lost",""" +
                """"status_changed_at":"2026-10-01T10:00:00Z","viewer_count":3,"viewer_peak":9,"future":1}}""",
        )

        val stream = runBlocking { api.getStream("s1") }.data!!

        assertThat(liveStatusOf(stream.status)).isEqualTo(LiveStatus.Ended)
        assertThat(endedReasonOf(stream.endedReason)).isEqualTo(EndedReason.HostLost)
        assertThat(stream.statusChangedAt).isEqualTo("2026-10-01T10:00:00Z")
        assertThat(stream.viewerCount).isEqualTo(3)
        assertThat(stream.viewerPeak).isEqualTo(9)
        assertThat(server.takeRequest().target).isEqualTo("/v1/livestream/streams/s1")
    }

    @Test
    fun `a null ended_reason and an absent moderator list decode to their defaults`() {
        enqueue(body = """{"data":{"id":"s1","status":"live","ended_reason":null}}""")

        val stream = runBlocking { api.getStream("s1") }.data!!

        assertThat(stream.endedReason).isEmpty()
        assertThat(stream.moderatorUserIds).isNull()
    }

    @Test
    fun `an empty moderator list is present, which is how the host and moderators are told apart`() {
        enqueue(body = """{"data":{"id":"s1","moderator_user_ids":[]}}""")
        enqueue(body = """{"data":{"id":"s1","moderator_user_ids":["u1","u2"]}}""")

        val empty = runBlocking { api.getStream("s1") }.data!!
        val two = runBlocking { api.getStream("s1") }.data!!

        assertThat(empty.moderatorUserIds).isEmpty()
        assertThat(two.moderatorUserIds).containsExactly("u1", "u2").inOrder()
    }

    @Test
    fun `a 403 LIVE_NOT_ENABLED on create reaches the screen as the pilot code`() {
        enqueue(code = 403, body = """{"error":{"code":"LIVE_NOT_ENABLED","message":"not in pilot"}}""")

        val result = runBlocking { apiCall(errorMapper) { api.createStream(CreateStreamRequest(title = "x")) } }

        val error = (result as AppResult.Failure).error
        assertThat(error).isInstanceOf(AppError.Forbidden::class.java)
        assertThat(error.liveCode()).isEqualTo(CODE_LIVE_NOT_ENABLED)
    }

    @Test
    fun `a 403 LIVE_NOT_ENABLED on start reaches the screen as the pilot code`() {
        enqueue(code = 403, body = """{"error":{"code":"LIVE_NOT_ENABLED","message":"not in pilot"}}""")

        val result = runBlocking { apiCall(errorMapper) { api.startStream("s1") } }

        assertThat(server.takeRequest().target).isEqualTo("/v1/livestream/streams/s1/start")
        assertThat((result as AppResult.Failure).error.liveCode()).isEqualTo(CODE_LIVE_NOT_ENABLED)
    }
}
