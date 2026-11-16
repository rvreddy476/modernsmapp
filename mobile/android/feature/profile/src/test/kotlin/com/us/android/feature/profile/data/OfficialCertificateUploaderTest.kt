package com.us.android.feature.profile.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.media.upload.MediaUploadApi
import com.us.android.core.media.upload.MediaUploader
import com.us.android.core.media.upload.PresignedUploader
import com.us.android.core.media.upload.UploadSource
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.Dispatchers
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

/** Contract §6: the certificate goes init (`file_type: document`) → PUT → confirm through `:core:media`. */
class OfficialCertificateUploaderTest {

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
    }
    private lateinit var server: MockWebServer
    private lateinit var client: OkHttpClient
    private lateinit var uploader: MediaOfficialCertificateUploader

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        client = OkHttpClient()
        val api = Retrofit.Builder().baseUrl(server.url("/")).client(client)
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType())).build()
            .create(MediaUploadApi::class.java)
        uploader = MediaOfficialCertificateUploader(
            MediaUploader(api, PresignedUploader(client), ErrorMapper(json)),
            Dispatchers.Unconfined,
        )
    }

    @After
    fun tearDown() {
        client.dispatcher.executorService.shutdown()
        client.connectionPool.evictAll()
        server.close()
    }

    private fun answer(code: Int, body: String = "") = server.enqueue(
        MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build(),
    )

    private val pdf = ByteArray(PDF_SIZE) { 7 }
    private val certificate = PickedCertificate(
        name = "gst.pdf",
        mimeType = "application/pdf",
        sizeBytes = PDF_SIZE.toLong(),
        source = UploadSource { ByteArrayInputStream(pdf) },
    )

    @Test
    fun `init asks for the private document kind, then PUT and confirm, and the id comes back`() = runBlocking {
        val put = server.url("/bucket/doc-1").toString()
        answer(201, """{"data":{"media_id":"doc-1","upload_url":"$put","object_key":"k"}}""")
        answer(200)
        answer(200, """{"data":{"id":"doc-1","file_type":"document"}}""")

        val id = uploader.upload(certificate)

        assertThat(id).isEqualTo("doc-1")
        val init = server.takeRequest()
        assertThat(init.url.encodedPath).isEqualTo("/v1/media/init")
        val sent = Json.parseToJsonElement(init.body!!.utf8()).jsonObject
        assertThat(sent["file_type"]!!.jsonPrimitive.content).isEqualTo("document")
        assertThat(sent["mime_type"]!!.jsonPrimitive.content).isEqualTo("application/pdf")
        assertThat(sent["file_size_bytes"]!!.jsonPrimitive.content).isEqualTo(PDF_SIZE.toString())
        val bytes = server.takeRequest()
        assertThat(bytes.method).isEqualTo("PUT")
        assertThat(bytes.body!!.size).isEqualTo(PDF_SIZE.toLong())
        val confirm = server.takeRequest()
        assertThat(confirm.url.encodedPath).isEqualTo("/v1/media/confirm")
        assertThat(confirm.body!!.utf8()).isEqualTo("""{"media_id":"doc-1"}""")
    }

    @Test
    fun `a refused init gives no id and moves no bytes`() = runBlocking {
        answer(415, """{"error":{"code":"UNSUPPORTED_MEDIA_TYPE","message":"no"}}""")

        assertThat(uploader.upload(certificate)).isNull()
        assertThat(server.requestCount).isEqualTo(1)
    }

    @Test
    fun `a failed PUT gives no id and is never confirmed`() = runBlocking {
        val put = server.url("/bucket/doc-1").toString()
        answer(201, """{"data":{"media_id":"doc-1","upload_url":"$put","object_key":"k"}}""")
        answer(500)

        assertThat(uploader.upload(certificate)).isNull()
        assertThat(server.requestCount).isEqualTo(2)
    }

    @Test
    fun `a failed confirm gives no id`() = runBlocking {
        val put = server.url("/bucket/doc-1").toString()
        answer(201, """{"data":{"media_id":"doc-1","upload_url":"$put","object_key":"k"}}""")
        answer(200)
        answer(500, """{"error":{"code":"INTERNAL","message":"no"}}""")

        assertThat(uploader.upload(certificate)).isNull()
    }

    private companion object {
        const val PDF_SIZE = 1_024
    }
}
