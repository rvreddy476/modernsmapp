package com.us.android.core.feed.offline

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.network.ApiConfig
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import kotlinx.coroutines.runBlocking
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okhttp3.MediaType.Companion.toMediaType
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

/**
 * Offline copies' four calls, byte for byte: the method, the path, the
 * query and the body, as post-service's routes take them
 * (`internal/http/offline_copies.go`).
 *
 * What this protects above all is `device_id` on EVERY call. A copy is
 * granted per device: a grant without it is refused, a check without it
 * answers for no device, and a remove without it removes nothing.
 *
 * The repository is driven through the real Retrofit stack, so what is
 * pinned is what actually goes on the wire.
 */
class OfflineApiRequestTest {

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var repository: OfflineRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(OfflineApi::class.java)
        val urls = MediaUrlResolver(
            ApiConfig(
                baseUrl = "https://api.test",
                wsBaseUrl = "wss://api.test",
                clientVersion = "test",
                environment = "test",
                isDebug = true,
            ),
        )
        repository = OfflineRepository({ api }, ErrorMapper(json), urls)
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(body: String, code: Int = 200) {
        server.enqueue(MockResponse.Builder().code(code).body(body).build())
    }

    private fun RecordedRequest.line(): String = "$method $target"

    private fun RecordedRequest.bodyText(): String = body?.utf8().orEmpty()

    // ── Grant ───────────────────────────────────────────────────────────

    @Test
    fun `a grant is a POST to the post's offline route with this device's id`() {
        enqueue(
            """{"data":{"post_id":"p1","expires_at":"2026-10-27T12:00:00Z","recheck_after_seconds":172800,
               "media":{"media_id":"m1","variant":"720p","path":"/v1/media/m1/serve/720p","mime":"video/mp4",
               "size_bytes":123}}}""",
            code = 201,
        )

        val result = runBlocking { repository.grant("p1", "device-1", nowMs = 0L) }

        val request = server.takeRequest()
        assertThat(request.line()).isEqualTo("POST /v1/posts/p1/offline")
        assertThat(request.bodyText()).isEqualTo("""{"device_id":"device-1"}""")
        val grant = (result as AppResult.Success).data
        // The gateway path is resolved against the API base, never used as it came.
        assertThat(grant.video.url).isEqualTo("https://api.test/v1/media/m1/serve/720p")
        assertThat(grant.video.sizeBytes).isEqualTo(123L)
    }

    @Test
    fun `a grant with nothing to store is a failure, not an empty copy`() {
        enqueue("""{"data":{"post_id":"p1","media":{"media_id":"m1","variant":"720p"}}}""")

        val result = runBlocking { repository.grant("p1", "device-1", nowMs = 0L) }

        assertThat((result as AppResult.Failure).error).isInstanceOf(AppError.Malformed::class.java)
    }

    @Test
    fun `the server's refusals are told apart by code`() {
        fun refusal(code: Int, errorCode: String): String {
            enqueue("""{"error":{"code":"$errorCode","message":"no"}}""", code = code)
            val result = runBlocking { repository.grant("p1", "device-1", nowMs = 0L) }
            server.takeRequest()
            return offlineRefusalMessage((result as AppResult.Failure).error)
        }

        assertThat(refusal(403, "OFFLINE_NOT_ALLOWED")).isEqualTo("The creator hasn't allowed saving this offline.")
        assertThat(refusal(409, "NOT_READY")).isEqualTo("This video isn't ready to save yet. Try again soon.")
        assertThat(refusal(409, "OFFLINE_LIMIT"))
            .isEqualTo("You've reached the limit of offline copies. Remove one and try again.")
        assertThat(refusal(404, "NOT_FOUND")).isEqualTo("This video is no longer available.")
        assertThat(refusal(500, "INTERNAL")).isEqualTo(COULD_NOT_SAVE_OFFLINE)
    }

    // ── Check ───────────────────────────────────────────────────────────

    @Test
    fun `a check is a POST with this device's id and the post ids`() {
        enqueue(
            """{"data":[{"post_id":"a","valid":true,"expires_at":"2026-10-27T12:00:00Z"},
               {"post_id":"b","valid":false,"reason":"private"}]}""",
        )

        val result = runBlocking { repository.check("device-1", listOf("a", "b")) }

        val request = server.takeRequest()
        assertThat(request.line()).isEqualTo("POST /v1/posts/offline/check")
        assertThat(request.bodyText()).isEqualTo("""{"device_id":"device-1","post_ids":["a","b"]}""")
        val answers = (result as AppResult.Success).data
        assertThat(answers["a"]).isInstanceOf(OfflineCheckAnswer.Valid::class.java)
        assertThat(answers["b"]).isEqualTo(OfflineCheckAnswer.Invalid("private"))
    }

    @Test
    fun `more than a hundred copies are checked a hundred at a time`() {
        enqueue("""{"data":[]}""")
        enqueue("""{"data":[{"post_id":"p100","valid":false,"reason":"deleted"}]}""")
        val ids = (0..100).map { "p$it" }

        val result = runBlocking { repository.check("device-1", ids) }

        val first = server.takeRequest().bodyText()
        val second = server.takeRequest().bodyText()
        assertThat(first).contains(""""p99"""")
        assertThat(first).doesNotContain(""""p100"""")
        assertThat(second).isEqualTo("""{"device_id":"device-1","post_ids":["p100"]}""")
        assertThat((result as AppResult.Success).data).containsExactly("p100", OfflineCheckAnswer.Invalid("deleted"))
    }

    @Test
    fun `a check that could not be made is a failure, never an empty answer`() {
        enqueue("""{"error":{"code":"INTERNAL","message":"down"}}""", code = 503)

        val result = runBlocking { repository.check("device-1", listOf("a")) }

        assertThat(result).isInstanceOf(AppResult.Failure::class.java)
    }

    // ── List ────────────────────────────────────────────────────────────

    @Test
    fun `the device's copies are read with the device id in the query`() {
        enqueue(
            """{"data":[{"post_id":"a","media":{"media_id":"m","variant":"720p"}},{"post_id":"b"},
               {"post_id":""}]}""",
        )

        val result = runBlocking { repository.held("device 1") }

        assertThat(server.takeRequest().line()).isEqualTo("GET /v1/posts/offline?device_id=device%201")
        assertThat((result as AppResult.Success).data).containsExactly("a", "b").inOrder()
    }

    @Test
    fun `no copies on the server is an empty list, not a failure`() {
        enqueue("""{"data":null}""")

        assertThat((runBlocking { repository.held("device-1") } as AppResult.Success).data).isEmpty()
    }

    // ── Remove ──────────────────────────────────────────────────────────

    @Test
    fun `a remove is a DELETE on the post's offline route with the device id in the query and no body`() {
        enqueue("""{"data":{"post_id":"p1","removed":true}}""")

        val result = runBlocking { repository.remove("p1", "device-1") }

        val request = server.takeRequest()
        assertThat(request.line()).isEqualTo("DELETE /v1/posts/p1/offline?device_id=device-1")
        assertThat(request.bodyText()).isEmpty()
        assertThat(result).isInstanceOf(AppResult.Success::class.java)
    }

    @Test
    fun `a remove that could not be sent is a failure`() {
        enqueue("""{"error":{"code":"INTERNAL","message":"down"}}""", code = 500)

        assertThat(runBlocking { repository.remove("p1", "device-1") }).isInstanceOf(AppResult.Failure::class.java)
    }
}
