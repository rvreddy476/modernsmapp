package com.us.android.core.notifications

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.NotificationAddress
import com.us.android.core.network.ErrorMapper
import com.us.android.core.notifications.data.NotificationDeleteApi
import com.us.android.core.notifications.data.NotificationDeletes
import kotlinx.coroutines.runBlocking
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

/**
 * Deleting one notification (2026-10-24) against notification-service
 * DeleteNotification (internal/http/handler.go:55 and :191-218): the
 * `(bucket, ts)` address in the path, `{"data":{"status":"deleted"}}` back.
 */
class NotificationDeleteRequestTest {

    private val json = Json { ignoreUnknownKeys = true }
    private lateinit var server: MockWebServer
    private lateinit var deletes: NotificationDeletes

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder().baseUrl(server.url("/")).client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType())).build()
            .create(NotificationDeleteApi::class.java)
        deletes = NotificationDeletes(api, ErrorMapper(json))
    }

    @After
    fun tearDown() = server.close()

    @Test
    fun `the address goes in the path of a DELETE`() = runBlocking {
        server.enqueue(MockResponse.Builder().code(200).setHeader("Content-Type", "application/json")
            .body("""{"data":{"status":"deleted"}}""").build())

        val result = deletes.delete(NotificationAddress(bucket = 202610, ts = "2026-10-24T10:00:00.123Z"))

        assertThat(result).isInstanceOf(AppResult.Success::class.java)
        val sent = server.takeRequest()
        assertThat(sent.method).isEqualTo("DELETE")
        assertThat(sent.url.pathSegments).containsExactly("v1", "notifications", "202610", "2026-10-24T10:00:00.123Z").inOrder()
    }

    @Test
    fun `a malformed address is refused`() = runBlocking {
        server.enqueue(MockResponse.Builder().code(400).setHeader("Content-Type", "application/json")
            .body("""{"error":{"code":"BAD_REQUEST","message":"Invalid bucket"}}""").build())

        assertThat(deletes.delete(NotificationAddress(0, "x"))).isInstanceOf(AppResult.Failure::class.java)
    }
}
