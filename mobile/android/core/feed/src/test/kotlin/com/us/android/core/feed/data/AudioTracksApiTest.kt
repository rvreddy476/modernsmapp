package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import kotlinx.coroutines.runBlocking
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

/**
 * media-service's audio-tracks routes on the wire (2026-10-21): the bodies are
 * the handler's own shapes (`audio_tracks_handler.go` writes
 * `api.JSON(…, gin.H{"tracks": tracks})`, a `service.AudioTrackView` per
 * track, and `api.ErrorWithContext` for refusals), and every request is
 * pinned byte for byte — path, method, multipart field names, JSON body.
 */
class AudioTracksApiTest {

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var api: AudioTracksApi
    private lateinit var repository: AudioTracksRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(AudioTracksApi::class.java)
        repository = AudioTracksRepository(api, ErrorMapper(json))
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(code: Int, body: String = "") {
        server.enqueue(MockResponse.Builder().code(code).body(body).build())
    }

    /** What `GET /v1/media/:id/audio-tracks` answers for a video with three tracks in three states. */
    private val listFixture = """
        {"data":{"tracks":[
          {"id":"original","language":"und","label":"Original","source":"original","status":"ready"},
          {"id":"5b1c0d6e-1111-4c1e-9a55-0d8f3a2b7c01","language":"hi","label":"Hindi","source":"uploaded",
           "status":"ready","playback_url":"/v1/media/9f6d/serve/dub_hi_720p","rungs":["720p","480p"],
           "created_at":"2026-10-21T08:00:00Z"},
          {"id":"5b1c0d6e-2222-4c1e-9a55-0d8f3a2b7c02","language":"te","label":"","source":"generated",
           "status":"processing","created_at":"2026-10-21T08:05:00Z"},
          {"id":"5b1c0d6e-3333-4c1e-9a55-0d8f3a2b7c03","language":"ta","label":"Tamil","source":"uploaded",
           "status":"failed","error":"duration mismatch","created_at":"2026-10-21T08:06:00Z"}
        ]},"meta":{"request_id":"r1"}}
    """.trimIndent()

    @Test
    fun `the list is read from the media's audio-tracks path and parsed in the server's order`() {
        enqueue(200, listFixture)

        val result = runBlocking { repository.refresh("9f6d") }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("GET")
        assertThat(request.target).isEqualTo("/v1/media/9f6d/audio-tracks")
        val tracks = (result as AppResult.Success).data
        assertThat(tracks.map { it.id }).containsExactly(
            "original",
            "5b1c0d6e-1111-4c1e-9a55-0d8f3a2b7c01",
            "5b1c0d6e-2222-4c1e-9a55-0d8f3a2b7c02",
            "5b1c0d6e-3333-4c1e-9a55-0d8f3a2b7c03",
        ).inOrder()
        assertThat(tracks[0].isOriginal).isTrue()
        assertThat(tracks[0].playbackUrl).isNull()
        assertThat(tracks[1].status).isEqualTo(AudioTrackStatus.READY)
        assertThat(tracks[1].playbackUrl).isEqualTo("/v1/media/9f6d/serve/dub_hi_720p")
        assertThat(tracks[1].playable).isTrue()
        assertThat(tracks[2].source).isEqualTo(AudioTrackSource.GENERATED)
        assertThat(tracks[2].status.working).isTrue()
        assertThat(tracks[3].status).isEqualTo(AudioTrackStatus.FAILED)
        assertThat(tracks[3].error).isEqualTo("duration mismatch")
        // And the cache holds them for the More menu.
        assertThat(repository.tracks.value["9f6d"]).isEqualTo(tracks)
    }

    @Test
    fun `the picker offers the original and the ready dub alone`() {
        enqueue(200, listFixture)

        val tracks = (runBlocking { repository.refresh("9f6d") } as AppResult.Success).data

        assertThat(audioTrackOptions(tracks).map { it.label }).containsExactly("Original", "Hindi").inOrder()
    }

    @Test
    fun `an upload is multipart with the handler's file, language and label fields`() {
        enqueue(
            201,
            """{"data":{"id":"t1","language":"hi","label":"Hindi dub","source":"uploaded","status":"pending"}}""",
        )
        enqueue(200, """{"data":{"tracks":[]}}""")
        val file = AudioUploadFile("dub.m4a", "audio/mp4", "AUDIO".toRequestBody("audio/mp4".toMediaType()))

        val result = runBlocking { repository.upload("9f6d", file, language = "hi", label = " Hindi dub ") }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/media/9f6d/audio-tracks")
        assertThat(request.headers["Content-Type"]).startsWith("multipart/form-data")
        val body = request.body!!.utf8()
        assertThat(body).contains("""Content-Disposition: form-data; name="file"; filename="dub.m4a"""")
        assertThat(body).contains("Content-Type: audio/mp4")
        assertThat(body).contains("AUDIO")
        assertThat(body).contains("""Content-Disposition: form-data; name="language"""")
        assertThat(body).contains("""Content-Disposition: form-data; name="label"""")
        assertThat(body).contains("Hindi dub")
        assertThat((result as AppResult.Success).data.status).isEqualTo(AudioTrackStatus.PENDING)
        // The list is read again so the sheet shows the new row.
        assertThat(server.takeRequest().target).isEqualTo("/v1/media/9f6d/audio-tracks")
    }

    @Test
    fun `a blank label is left out of the upload`() {
        val parts = audioUploadParts(
            AudioUploadFile("a.mp3", "audio/mpeg", "x".toRequestBody()),
            language = "te",
            label = "   ",
        )

        assertThat(parts.label).isNull()
    }

    @Test
    fun `generate sends the language alone as JSON`() {
        enqueue(
            202,
            """{"data":{"id":"t2","language":"te","label":"Telugu","source":"generated","status":"pending"}}""",
        )
        enqueue(200, """{"data":{"tracks":[]}}""")

        val result = runBlocking { repository.generate("9f6d", "te") }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/media/9f6d/audio-tracks/generate")
        assertThat(request.body!!.utf8()).isEqualTo("""{"language":"te"}""")
        assertThat((result as AppResult.Success).data.source).isEqualTo(AudioTrackSource.GENERATED)
    }

    @Test
    fun `503 DUBBING_UNAVAILABLE reads as dubbing being off, with the clear sentence`() {
        enqueue(
            503,
            """{"error":{"code":"DUBBING_UNAVAILABLE","message":"AI dubbing is not available"},""" +
                """"meta":{"request_id":"r2"}}""",
        )

        val result = runBlocking { repository.generate("9f6d", "te") }

        val error = (result as AppResult.Failure).error
        assertThat(error.isDubbingUnavailable()).isTrue()
        assertThat(audioTrackRefusal(error)).isEqualTo(DUBBING_UNAVAILABLE_MESSAGE)
    }

    @Test
    fun `422 AUDIO_DURATION_MISMATCH says the lengths differ`() {
        enqueue(
            422,
            """{"error":{"code":"AUDIO_DURATION_MISMATCH","message":"audio is 12.0s, video is 30.0s"}}""",
        )
        val file = AudioUploadFile("dub.m4a", "audio/mp4", "x".toRequestBody())

        val result = runBlocking { repository.upload("9f6d", file, "hi", "") }

        val error = (result as AppResult.Failure).error
        assertThat(error.isDubbingUnavailable()).isFalse()
        assertThat(audioTrackRefusal(error)).isEqualTo("That audio is not the same length as the video.")
    }

    @Test
    fun `delete is a DELETE on the track's path, and the cache drops it on 204`() {
        enqueue(200, listFixture)
        runBlocking { repository.refresh("9f6d") }
        server.takeRequest()
        enqueue(204)

        val result = runBlocking { repository.delete("9f6d", "5b1c0d6e-1111-4c1e-9a55-0d8f3a2b7c01") }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("DELETE")
        assertThat(request.target).isEqualTo("/v1/media/9f6d/audio-tracks/5b1c0d6e-1111-4c1e-9a55-0d8f3a2b7c01")
        assertThat(result).isInstanceOf(AppResult.Success::class.java)
        assertThat(repository.tracks.value["9f6d"]!!.map { it.id })
            .doesNotContain("5b1c0d6e-1111-4c1e-9a55-0d8f3a2b7c01")
    }

    @Test
    fun `a video the viewer may not have is remembered as the original alone and not asked again`() {
        enqueue(404, """{"error":{"code":"NOT_FOUND","message":"Not found"}}""")

        runBlocking {
            repository.ensure("gone")
            repository.ensure("gone")
        }

        assertThat(server.requestCount).isEqualTo(1)
        assertThat(repository.tracks.value["gone"]).isEmpty()
    }

    @Test
    fun `a network failure is not remembered, so the next settle asks again`() {
        enqueue(500, """{"error":{"code":"INTERNAL_ERROR","message":"boom"}}""")
        enqueue(200, """{"data":{"tracks":[]}}""")

        runBlocking {
            repository.ensure("m1")
            repository.ensure("m1")
        }

        assertThat(server.requestCount).isEqualTo(2)
    }

    @Test
    fun `the refusal sentences follow the server's codes`() {
        assertThat(audioTrackRefusal(AppError.Unknown(code = "AUDIO_TRACK_LIMIT", statusCode = 422)))
            .isEqualTo("A video can carry up to 10 audio tracks.")
        assertThat(audioTrackRefusal(AppError.Unknown(code = "MEDIA_NOT_READY", statusCode = 409)))
            .isEqualTo("The video is still processing. Try again in a moment.")
        assertThat(audioTrackRefusal(AppError.Unknown(code = "PAYLOAD_TOO_LARGE", statusCode = 413)))
            .isEqualTo("That file is too large (50 MB max).")
        assertThat(audioTrackRefusal(AppError.NoNetwork())).contains("offline")
    }
}
