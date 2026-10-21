package com.us.android.core.media.upload

import com.google.common.truth.Truth.assertThat
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
}
