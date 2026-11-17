package com.us.android.core.media.upload

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.RequestBody.Companion.toRequestBody
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import java.io.ByteArrayInputStream

/**
 * The resumable routes on the wire, against
 * `media-service/internal/http/resumable_handler.go`: the paths, the
 * `part_number` query, raw `application/octet-stream` bodies with an exact
 * `Content-Length` (the server sizes each part from it), and the DTOs.
 *
 * The `Json` mirrors `NetworkModule.provideJson()`, `encodeDefaults` OFF.
 */
class ResumableUploadWireTest {

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }

    private lateinit var server: MockWebServer
    private lateinit var client: OkHttpClient
    private lateinit var api: ResumableUploadApi

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        client = OkHttpClient()
        api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(client)
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(ResumableUploadApi::class.java)
    }

    @After
    fun tearDown() {
        client.dispatcher.executorService.shutdown()
        client.connectionPool.evictAll()
        server.close()
    }

    private fun enqueueJson(body: String, code: Int = 200) {
        server.enqueue(
            MockResponse.Builder().code(code).body(body).setHeader("Content-Type", "application/json").build(),
        )
    }

    @Test
    fun `init posts file type, mime type and total bytes, file type included though it is the default`() =
        runBlocking {
            enqueueJson(
                """{"data":{"upload_id":"u1","media_id":"m1","chunk_size":5242880,"total_parts":615,""" +
                    """"expires_at":"2026-10-22T10:00:00.123456789Z"}}""",
            )

            val answer = api.init(ResumableInitRequest(mimeType = "video/mp4", totalBytes = 3_221_225_472L))

            val recorded = server.takeRequest()
            assertThat(recorded.method).isEqualTo("POST")
            assertThat(recorded.target).isEqualTo("/v1/media/upload/resumable/init")
            val sent = json.parseToJsonElement(recorded.body!!.utf8()).jsonObject
            assertThat(sent.keys).containsExactly("file_type", "mime_type", "total_bytes")
            assertThat(sent["file_type"]!!.jsonPrimitive.content).isEqualTo("video")
            assertThat(sent["mime_type"]!!.jsonPrimitive.content).isEqualTo("video/mp4")
            assertThat(sent["total_bytes"]!!.jsonPrimitive.content).isEqualTo("3221225472")
            assertThat(answer.data).isEqualTo(
                ResumableInitDto("u1", "m1", 5_242_880L, 615, "2026-10-22T10:00:00.123456789Z"),
            )
        }

    @Test
    fun `a part is raw octets on the chunk route with its number in the query and an exact length`() = runBlocking {
        enqueueJson("""{"data":{"upload_id":"u1","part_number":3,"uploaded_bytes":30,"all_parts_in":false}}""")
        val bytes = ByteArray(10) { it.toByte() }

        val answer = api.uploadPart("u1", 3, bytes.toRequestBody("application/octet-stream".toMediaType()))

        val recorded = server.takeRequest()
        assertThat(recorded.method).isEqualTo("POST")
        assertThat(recorded.target).isEqualTo("/v1/media/upload/resumable/u1/chunk?part_number=3")
        assertThat(recorded.headers["Content-Type"]).isEqualTo("application/octet-stream")
        assertThat(recorded.headers["Content-Length"]).isEqualTo("10")
        assertThat(recorded.body!!.toByteArray()).isEqualTo(bytes)
        assertThat(answer.data?.partNumber).isEqualTo(3)
        assertThat(answer.data?.uploadedBytes).isEqualTo(30L)
    }

    @Test
    fun `complete is a bodiless POST that answers the media record`() = runBlocking {
        enqueueJson("""{"data":{"id":"m1","file_type":"video","processing_status":"processing"}}""")

        val answer = api.complete("u1")

        val recorded = server.takeRequest()
        assertThat(recorded.method).isEqualTo("POST")
        assertThat(recorded.target).isEqualTo("/v1/media/upload/resumable/u1/complete")
        assertThat(recorded.body?.size ?: 0).isEqualTo(0)
        assertThat(answer.data?.id).isEqualTo("m1")
    }

    @Test
    fun `status is a GET and a null part list decodes as empty`() = runBlocking {
        enqueueJson(
            """{"data":{"upload_id":"u1","media_id":"m1","total_bytes":25,"uploaded_bytes":0,"chunk_size":10,""" +
                """"total_parts":3,"status":"initiated","uploaded_parts":null}}""",
        )
        enqueueJson("""{"data":{"upload_id":"u1","status":"uploading","uploaded_parts":[1,2]}}""")

        val fresh = api.status("u1")
        val partial = api.status("u1")

        val recorded = server.takeRequest()
        assertThat(recorded.method).isEqualTo("GET")
        assertThat(recorded.target).isEqualTo("/v1/media/upload/resumable/u1")
        assertThat(fresh.data?.uploadedParts).isEmpty()
        assertThat(fresh.data?.totalParts).isEqualTo(3)
        assertThat(partial.data?.uploadedParts).containsExactly(1, 2).inOrder()
    }

    /**
     * The uploader over the real transport and Retrofit: every part lands with
     * the right number, length and bytes, and complete follows once.
     */
    @Test
    fun `an upload end to end sends each slice with its own content length`() = runBlocking {
        enqueueJson("""{"data":{"upload_id":"u1","media_id":"m1","chunk_size":10,"total_parts":3}}""")
        repeat(3) { enqueueJson("""{"data":{"upload_id":"u1"}}""") }
        enqueueJson("""{"data":{"id":"m1","processing_status":"processing"}}""")
        val bytes = ByteArray(25) { (it * 7).toByte() }
        val uploader = ResumableUploader(RetrofitResumableTransport(api, ErrorMapper(json)))

        val outcome = uploader.upload(
            mimeType = "video/mp4",
            totalBytes = 25L,
            source = UploadSource { ByteArrayInputStream(bytes) },
            resume = null,
            onSession = {},
            onProgress = { _, _ -> },
        )

        assertThat(outcome).isEqualTo(ResumableOutcome.Completed("m1"))
        assertThat(server.takeRequest().target).isEqualTo("/v1/media/upload/resumable/init")
        val slices = listOf(0 until 10, 10 until 20, 20 until 25)
        slices.forEachIndexed { index, range ->
            val part = server.takeRequest()
            assertThat(part.target).isEqualTo("/v1/media/upload/resumable/u1/chunk?part_number=${index + 1}")
            assertThat(part.headers["Content-Length"]).isEqualTo(range.count().toString())
            assertThat(part.body!!.toByteArray()).isEqualTo(bytes.copyOfRange(range.first, range.last + 1))
        }
        assertThat(server.takeRequest().target).isEqualTo("/v1/media/upload/resumable/u1/complete")
        assertThat(server.requestCount).isEqualTo(5)
    }

    @Test
    fun `a 413 from the simple init maps to the resumable switch`() = runBlocking {
        enqueueJson("""{"error":{"code":"USE_RESUMABLE_UPLOAD","message":"too big for one PUT"}}""", code = 413)
        val simple = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(client)
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(MediaUploadApi::class.java)
        val uploader = MediaUploader(simple, PresignedUploader(client), ErrorMapper(json))

        val result = uploader.reserve("video/mp4", 6L * 1024L * 1024L * 1024L, fileType = FILE_TYPE_VIDEO)

        assertThat(result).isInstanceOf(AppResult.Failure::class.java)
        assertThat((result as AppResult.Failure).error.asksForResumableUpload()).isTrue()
    }

    // ── 413 VIDEO_TOO_LARGE (2026-11-07) ────────────────────────────────

    private val tooLargeBody =
        """{"error":{"code":"VIDEO_TOO_LARGE","message":"video too large: 20000000000 bytes (max 17179869184)",""" +
            """"details":{"max_bytes":17179869184}},"meta":{"request_id":"req-9"}}"""

    @Test
    fun `the simple init's too-large keeps its code, request id and cap, and is no resumable switch`() = runBlocking {
        enqueueJson(tooLargeBody, code = 413)
        val simple = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(client)
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(MediaUploadApi::class.java)
        val uploader = MediaUploader(simple, PresignedUploader(client), ErrorMapper(json))

        val result = uploader.reserve("video/mp4", 20_000_000_000L, fileType = FILE_TYPE_VIDEO)

        val error = (result as AppResult.Failure).error as AppError.Unknown
        // The body was peeked, not consumed: the mapper still read all of it.
        assertThat(error.code).isEqualTo(CODE_VIDEO_TOO_LARGE)
        assertThat(error.requestId).isEqualTo("req-9")
        assertThat(error.videoTooLarge()).isEqualTo(VideoTooLarge(maxBytes = 17_179_869_184L))
        assertThat(error.asksForResumableUpload()).isFalse()
    }

    @Test
    fun `the resumable init's too-large stops the upload with the cap, once, and not retryable`() = runBlocking {
        enqueueJson(tooLargeBody, code = 413)
        val uploader = ResumableUploader(RetrofitResumableTransport(api, ErrorMapper(json)))

        val outcome = uploader.upload(
            mimeType = "video/mp4",
            totalBytes = 20_000_000_000L,
            source = UploadSource { ByteArrayInputStream(ByteArray(0)) },
            resume = null,
            onSession = {},
            onProgress = { _, _ -> },
        )

        assertThat(outcome).isEqualTo(
            ResumableOutcome.Failed(
                message = "This video is too big to upload (max 16 GB).",
                retryable = false,
                discardSession = true,
            ),
        )
        assertThat(server.requestCount).isEqualTo(1)
    }

    @Test
    fun `another 413 keeps no details it did not have and other failures are untouched`() = runBlocking {
        enqueueJson("""{"error":{"code":"USE_RESUMABLE_UPLOAD","message":"too big for one PUT"}}""", code = 413)
        enqueueJson("""{"error":{"code":"BAD_REQUEST","message":"no","details":{"field":"mime_type"}}}""", code = 400)
        val transport = RetrofitResumableTransport(api, ErrorMapper(json))
        val request = ResumableInitRequest(mimeType = "video/mp4", totalBytes = 1L)

        val first = (transport.init(request) as AppResult.Failure).error as AppError.Unknown
        val second = (transport.init(request) as AppResult.Failure).error as AppError.Unknown

        assertThat(first.details).isEmpty()
        assertThat(first.asksForResumableUpload()).isTrue()
        // A 400's details are not this helper's to read.
        assertThat(second.details).isEmpty()
        assertThat(second.code).isEqualTo("BAD_REQUEST")
    }

    // ── 2026-11-17 contract: duration_ms, sliding expires_at, refusal codes ──

    private fun simpleApi(): MediaUploadApi = Retrofit.Builder()
        .baseUrl(server.url("/"))
        .client(client)
        .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
        .build()
        .create(MediaUploadApi::class.java)

    private fun enqueueRefusal(code: Int, body: String, retryAfter: String? = null) {
        val builder = MockResponse.Builder().code(code).body(body).setHeader("Content-Type", "application/json")
        retryAfter?.let { builder.setHeader("Retry-After", it) }
        server.enqueue(builder.build())
    }

    @Test
    fun `both inits declare duration_ms when the length is known (N-3)`() = runBlocking {
        enqueueJson("""{"data":{"upload_id":"u1","media_id":"m1","chunk_size":10,"total_parts":1}}""")
        enqueueJson("""{"data":{"media_id":"m2","upload_url":"http://x"}}""")

        api.init(ResumableInitRequest(mimeType = "video/mp4", totalBytes = 10L, durationMs = 754_321L))
        MediaUploader(simpleApi(), PresignedUploader(client), ErrorMapper(json))
            .reserve("video/mp4", 10L, fileType = FILE_TYPE_VIDEO, durationMs = 61_000L)

        val resumable = json.parseToJsonElement(server.takeRequest().body!!.utf8()).jsonObject
        val simple = json.parseToJsonElement(server.takeRequest().body!!.utf8()).jsonObject
        assertThat(resumable["duration_ms"]!!.jsonPrimitive.content).isEqualTo("754321")
        assertThat(simple["duration_ms"]!!.jsonPrimitive.content).isEqualTo("61000")
    }

    @Test
    fun `a part's answer carries the session's new expiry`() = runBlocking {
        enqueueJson("""{"data":{"upload_id":"u1","part_number":1,"expires_at":"2026-11-20T10:00:00Z"}}""")

        val answer = api.uploadPart("u1", 1, ByteArray(1).toRequestBody("application/octet-stream".toMediaType()))

        assertThat(answer.data?.expiresAt).isEqualTo("2026-11-20T10:00:00Z")
    }

    @Test
    fun `a quota 429 is shown with its wait, once, never retried as a blip`() = runBlocking {
        enqueueRefusal(
            429,
            """{"error":{"code":"UPLOAD_QUOTA_EXCEEDED","message":"You can upload up to 32 GB of video a day. """ +
                """Try again later, or contact support to raise your limit.",""" +
                """"details":{"limit":"daily_video_bytes",""" +
                """"max":34359738368,"used":34000000000,"requested":900000000,"window_seconds":86400}}}""",
            retryAfter = "3600",
        )
        val uploader = ResumableUploader(RetrofitResumableTransport(api, ErrorMapper(json)))

        val outcome = uploader.upload(
            mimeType = "video/mp4",
            totalBytes = 900_000_000L,
            source = UploadSource { ByteArrayInputStream(ByteArray(0)) },
            resume = null,
            onSession = {},
            onProgress = { _, _ -> },
        ) as ResumableOutcome.Failed

        assertThat(outcome.message).isEqualTo(
            "You can upload up to 32 GB of video a day. Try again later, or contact support to raise your limit. " +
                "You can try again in about an hour.",
        )
        assertThat(outcome.retryable).isTrue()
        assertThat(outcome.transient).isFalse()
        assertThat(server.requestCount).isEqualTo(1)
    }

    @Test
    fun `an over-long video is refused at init with the server's sentence and no Retry`() = runBlocking {
        enqueueRefusal(
            413,
            """{"error":{"code":"VIDEO_TOO_LONG","message":"This video is longer than 4 hours, the longest we can """ +
                """process right now. Trim it or split it into parts, then upload again.",""" +
                """"details":{"max_duration_seconds":14400,"limit":"processing_duration"}}}""",
        )
        val uploader = ResumableUploader(RetrofitResumableTransport(api, ErrorMapper(json)))

        val outcome = uploader.upload(
            mimeType = "video/mp4",
            totalBytes = 900_000_000L,
            source = UploadSource { ByteArrayInputStream(ByteArray(0)) },
            resume = null,
            onSession = {},
            onProgress = { _, _ -> },
            durationMs = 5L * 3_600_000L,
        ) as ResumableOutcome.Failed

        assertThat(outcome.message).startsWith("This video is longer than 4 hours")
        assertThat(outcome.retryable).isFalse()
        assertThat(server.requestCount).isEqualTo(1)
        val sent = json.parseToJsonElement(server.takeRequest().body!!.utf8()).jsonObject
        assertThat(sent["duration_ms"]!!.jsonPrimitive.content).isEqualTo("18000000")
    }

    @Test
    fun `a long-video wait and an hour's rate limit keep their codes, a short 429 and the limiter stay retryable`() =
        runBlocking {
            enqueueRefusal(
                429,
                """{"error":{"code":"LONG_VIDEO_IN_PROGRESS","message":"busy","details":{"max":2,"used":2}}}""",
                "600",
            )
            enqueueRefusal(429, """{"error":{"code":"RATE_LIMITED","message":"upload rate limit exceeded"}}""", "3600")
            enqueueRefusal(429, """{"error":{"code":"RATE_LIMITED","message":"slow down"}}""", "5")
            enqueueRefusal(503, """{"error":{"code":"UPLOAD_LIMITER_UNAVAILABLE","message":"paused"}}""", "30")
            val transport = RetrofitResumableTransport(api, ErrorMapper(json))
            val request = ResumableInitRequest(mimeType = "video/mp4", totalBytes = 1L)

            val long = (transport.init(request) as AppResult.Failure).error
            val hourly = (transport.init(request) as AppResult.Failure).error
            val blip = (transport.init(request) as AppResult.Failure).error
            val limiter = (transport.init(request) as AppResult.Failure).error

            assertThat(long.uploadRefusal())
                .isEqualTo(UploadRefusal(CODE_LONG_VIDEO_IN_PROGRESS, "busy", mapOf("max" to "2", "used" to "2"), 600L))
            assertThat(long.uploadRefusal()!!.message).isEqualTo("busy You can try again in about 10 minutes.")
            assertThat(hourly.uploadRefusal()!!.message)
                .isEqualTo("You've started too many uploads for now. You can try again in about an hour.")
            assertThat(blip).isInstanceOf(AppError.RateLimited::class.java)
            assertThat(limiter).isEqualTo(AppError.Server(statusCode = 503, code = CODE_UPLOAD_LIMITER_UNAVAILABLE))
            assertThat(limiter.uploadRefusal()!!.message)
                .isEqualTo("Uploads are paused for a moment. Try again in a minute.")
        }

    @Test
    fun `a part refused 410 starts one fresh session and the creator is told`() = runBlocking {
        enqueueJson("""{"data":{"upload_id":"u1","media_id":"m1","chunk_size":10,"total_parts":1}}""")
        enqueueRefusal(410, """{"error":{"code":"UPLOAD_EXPIRED","message":"The upload took too long and expired."}}""")
        enqueueJson("""{"data":{"upload_id":"u2","media_id":"m2","chunk_size":10,"total_parts":1}}""")
        enqueueJson("""{"data":{"upload_id":"u2","part_number":1}}""")
        enqueueJson("""{"data":{"id":"m2"}}""")
        val uploader = ResumableUploader(RetrofitResumableTransport(api, ErrorMapper(json)))
        var told = 0

        val outcome = uploader.upload(
            mimeType = "video/mp4",
            totalBytes = 10L,
            source = UploadSource { ByteArrayInputStream(ByteArray(10)) },
            resume = null,
            onSession = {},
            onProgress = { _, _ -> },
            onRestarted = { told++ },
        )

        assertThat(outcome).isEqualTo(ResumableOutcome.Completed("m2"))
        assertThat(told).isEqualTo(1)
    }
}
