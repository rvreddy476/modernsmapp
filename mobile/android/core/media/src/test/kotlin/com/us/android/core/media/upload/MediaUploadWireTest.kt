package com.us.android.core.media.upload

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.di.BareClient
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import java.io.ByteArrayInputStream

/**
 * The media upload contract, on the wire — C-LB-6.2 and C-LB-6.4.
 *
 * ## WHY THIS FILE EXISTS
 *
 * Its absence was a launch blocker. `BareClientTest` proves the bare OkHttp
 * PROVIDER carries no interceptors, which is a different claim entirely: it
 * never instantiates `PresignedUploader`, never exercises the qualifier that
 * path actually resolves, and never inspects a real PUT. An accidental switch
 * to the authenticated client would have left it green while the app sent the
 * user's bearer token to a third-party object store.
 *
 * The `Json` here mirrors `NetworkModule.provideJson()` exactly, with
 * `encodeDefaults` OFF. A test that enabled it would pass while the app failed.
 */
class MediaUploadWireTest {

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }

    private lateinit var server: MockWebServer
    private lateinit var client: OkHttpClient
    private lateinit var api: MediaUploadApi

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
            .create(MediaUploadApi::class.java)
    }

    @After
    fun tearDown() {
        client.dispatcher.executorService.shutdown()
        client.connectionPool.evictAll()
        server.close()
    }

    private fun enqueueJson(body: String) {
        server.enqueue(
            MockResponse.Builder()
                .code(200)
                .body(body)
                .setHeader("Content-Type", "application/json")
                .build(),
        )
    }

    // ── init ────────────────────────────────────────────────────────────

    /**
     * The exact init body.
     *
     * `file_type` is bound `required,oneof=image video` server-side, and
     * `upload_purpose` decides whether an abandoned asset can ever be reclaimed.
     * Both equal their Kotlin defaults, so both vanish without `@EncodeDefault`
     * — the defect that shipped twice in Slice B.
     */
    @Test
    fun `init sends every required field including the ones equal to their defaults`() = runBlocking {
        enqueueJson("""{"data":{"media_id":"m1","upload_url":"https://obj/put","object_key":"k"}}""")

        api.init(
            MediaInitRequest(
                mimeType = "image/jpeg",
                fileSizeBytes = 12_345,
                uploadPurpose = UPLOAD_PURPOSE_COMPOSER,
            ),
        )

        val sent = json.parseToJsonElement(server.takeRequest().body!!.utf8()).jsonObject
        assertThat(sent.keys).containsExactly(
            "file_type",
            "media_subtype",
            "mime_type",
            "file_size_bytes",
            "alt_text",
            "decorative",
            "upload_purpose",
        )
        assertThat(sent["file_type"]!!.jsonPrimitive.content).isEqualTo("image")
        assertThat(sent["media_subtype"]!!.jsonPrimitive.content).isEqualTo("general")
        assertThat(sent["mime_type"]!!.jsonPrimitive.content).isEqualTo("image/jpeg")
        assertThat(sent["file_size_bytes"]!!.jsonPrimitive.content).isEqualTo("12345")
        assertThat(sent["decorative"]!!.jsonPrimitive.content).isEqualTo("false")
        assertThat(sent["upload_purpose"]!!.jsonPrimitive.content).isEqualTo("composer")
    }

    /**
     * The lease must reach the wire.
     *
     * Without it the server stores NULL, and a NULL lease means the asset is
     * never a confirmed-reclamation candidate — so every abandoned composer
     * photo is retained forever. A storage leak, not a crash, which is exactly
     * the kind of defect nobody notices for months.
     */
    @Test
    fun `the composer lease is on the wire`() {
        val body = json.encodeToString(
            MediaInitRequest(
                mimeType = "image/png",
                fileSizeBytes = 1,
                uploadPurpose = UPLOAD_PURPOSE_COMPOSER,
            ),
        )
        assertThat(body).contains("\"upload_purpose\":\"composer\"")
    }

    /**
     * A voice upload reserves with `file_type: audio` — the value the
     * media-service SERVICE layer defines (`validation.go:80`,
     * `media.go:226`) — and the audio MIME type unchanged.
     */
    @Test
    fun `an audio reservation puts file_type audio and the audio mime on the wire`() = runBlocking {
        enqueueJson("""{"data":{"media_id":"m9","upload_url":"https://obj/put","object_key":"k"}}""")

        MediaUploader(api, PresignedUploader(client), com.us.android.core.network.ErrorMapper(json))
            .reserve(mimeType = "audio/mp4", sizeBytes = 48_000, fileType = FILE_TYPE_AUDIO)

        val sent = json.parseToJsonElement(server.takeRequest().body!!.utf8()).jsonObject
        assertThat(sent["file_type"]!!.jsonPrimitive.content).isEqualTo("audio")
        assertThat(sent["mime_type"]!!.jsonPrimitive.content).isEqualTo("audio/mp4")
        assertThat(sent["upload_purpose"]!!.jsonPrimitive.content).isEqualTo("composer")
    }

    /** The client allow-list is the server's, so a rejected type never costs an upload. */
    @Test
    fun `the audio allow-list mirrors media-service and ignores mime parameters`() {
        assertThat(isSupportedAudioUpload("audio/mp4")).isTrue()
        assertThat(isSupportedAudioUpload("audio/MP4; codecs=mp4a.40.2")).isTrue()
        assertThat(isSupportedAudioUpload("audio/mpeg")).isTrue()
        assertThat(isSupportedAudioUpload("audio/amr")).isTrue()
        assertThat(isSupportedAudioUpload("audio/x-unknown")).isFalse()
        assertThat(isSupportedAudioUpload("image/jpeg")).isFalse()
        assertThat(isSupportedAudioUpload("video/mp4")).isFalse()
    }

    // ── confirm and alt-text ────────────────────────────────────────────

    @Test
    fun `confirm sends only the media id under its wire name`() = runBlocking {
        enqueueJson("""{"data":{"id":"m1","processing_status":"ready"}}""")

        api.confirm(MediaConfirmRequest("m1"))

        assertThat(server.takeRequest().body!!.utf8()).isEqualTo("""{"media_id":"m1"}""")
    }

    /**
     * The final accessibility decision, and its explicit `false`.
     *
     * `decorative` omitted is not "false" — it is "unspecified", and a described
     * image would then be indistinguishable on the wire from one the creator
     * marked as carrying no information.
     */
    @Test
    fun `the alt-text update sends the description and an explicit decorative flag`() = runBlocking {
        enqueueJson("""{"data":{"media_id":"m1"}}""")

        api.updateAltText(
            "m1",
            MediaAltTextRequest(altText = "a cat asleep on a keyboard", decorative = false),
        )

        val recorded = server.takeRequest()
        assertThat(recorded.method).isEqualTo("PATCH")
        assertThat(recorded.target).isEqualTo("/v1/media/m1/alt-text")
        assertThat(recorded.body!!.utf8())
            .isEqualTo("""{"alt_text":"a cat asleep on a keyboard","decorative":false}""")
    }

    @Test
    fun `a decorative image sends an empty description and decorative true`() = runBlocking {
        enqueueJson("""{"data":{"media_id":"m1"}}""")

        api.updateAltText("m1", MediaAltTextRequest(altText = "", decorative = true))

        assertThat(server.takeRequest().body!!.utf8())
            .isEqualTo("""{"alt_text":"","decorative":true}""")
    }

    // ── The presigned PUT: header isolation (C-LB-6.4) ──────────────────

    /**
     * The real [PresignedUploader], driving a real PUT.
     *
     * This is the test whose absence was a launch blocker. A presigned URL
     * authenticates through its own query signature; a request that ALSO
     * carries `Authorization` is ambiguous and S3-compatible stores reject it —
     * and, far worse, it hands a credential that authenticates as the user to a
     * host that is not ours.
     *
     * NC-C6C swaps the injected client for the authenticated one, and this is
     * what fails.
     */
    @Test
    fun `the presigned put carries no credential of any kind`() = runBlocking {
        server.enqueue(MockResponse.Builder().code(200).build())
        val bytes = ByteArray(2048) { 7 }

        val result = PresignedUploader(client).put(
            url = server.url("/bucket/key?X-Amz-Signature=abc").toString(),
            mimeType = "image/jpeg",
            sizeBytes = bytes.size.toLong(),
            source = { ByteArrayInputStream(bytes) },
            onProgress = { _, _ -> },
        )

        assertThat(result).isEqualTo(PresignedPutResult.Success)

        val recorded = server.takeRequest()
        assertThat(recorded.method).isEqualTo("PUT")
        assertThat(recorded.headers["Content-Type"]).startsWith("image/jpeg")
        assertThat(recorded.headers["Content-Length"]).isEqualTo(bytes.size.toString())
        assertThat(recorded.body!!.size).isEqualTo(bytes.size.toLong())

        for (header in listOf(
            "Authorization",
            "Cookie",
            "X-CSRF-Token",
            "X-User-Id",
            "X-Client-Platform",
            "X-Request-Id",
        )) {
            assertThat(recorded.headers[header]).isNull()
        }
    }

    /**
     * The uploader is WIRED to the bare client, not merely capable of using one.
     *
     * This is the half the PUT test above cannot see. That test constructs the
     * uploader with a client it chose itself, so it stays green no matter what
     * Hilt actually injects — exactly the blind spot `BareClientTest` has at the
     * other end, where it proves the PROVIDER is clean but never checks that
     * anything consumes it.
     *
     * Without the qualifier Hilt injects the unqualified, AUTHENTICATED client,
     * and every presigned PUT would then carry the user's bearer token to a
     * third-party object-store host: a credential leak first and a broken upload
     * second, because S3-compatible stores reject a request that presents two
     * credentials. Nothing else in the build fails when the annotation is
     * deleted, which is why it is asserted here.
     *
     * This is the assertion NC-C6C mutates.
     */
    @Test
    fun `the uploader is injected with the bare client, by qualifier`() {
        val constructor = PresignedUploader::class.java.declaredConstructors.single()

        val qualifiers = constructor.parameterAnnotations.single()
            .map { it.annotationClass }

        assertThat(qualifiers).contains(BareClient::class)
    }

    /** Progress is monotonic and finishes at the total. */
    @Test
    fun `the presigned put reports monotonic progress ending at the total`() = runBlocking {
        server.enqueue(MockResponse.Builder().code(200).build())
        val bytes = ByteArray(40_000) { 3 }
        val seen = mutableListOf<Long>()

        PresignedUploader(client).put(
            url = server.url("/bucket/key").toString(),
            mimeType = "image/jpeg",
            sizeBytes = bytes.size.toLong(),
            source = { ByteArrayInputStream(bytes) },
            onProgress = { uploaded, _ -> seen += uploaded },
        )

        assertThat(seen.first()).isEqualTo(0L)
        assertThat(seen.last()).isEqualTo(bytes.size.toLong())
        assertThat(seen).isInOrder()
    }

    /**
     * A 403 is an expired signature, not a permission decision about the user.
     *
     * Distinguished because the responses differ: an expired URL can never
     * succeed on retry, so the client must run a NEW `init` rather than repeat
     * the PUT.
     */
    @Test
    fun `an object-store 403 is reported as an expired url`() = runBlocking {
        server.enqueue(MockResponse.Builder().code(403).build())

        val result = PresignedUploader(client).put(
            url = server.url("/bucket/key").toString(),
            mimeType = "image/jpeg",
            sizeBytes = 4,
            source = { ByteArrayInputStream(ByteArray(4)) },
            onProgress = { _, _ -> },
        )

        assertThat(result).isEqualTo(PresignedPutResult.UrlExpired)
    }
}
