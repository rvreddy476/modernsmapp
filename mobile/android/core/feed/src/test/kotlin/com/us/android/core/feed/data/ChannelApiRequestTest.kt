package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.di.NetworkModule
import kotlinx.coroutines.runBlocking
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

/** The channel endpoints on the wire: the paths, the query, the create body. */
class ChannelApiRequestTest {

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var api: ChannelApi

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(ChannelApi::class.java)
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(body: String, code: Int = 200) {
        server.enqueue(MockResponse.Builder().code(code).body(body).build())
    }

    @Test
    fun `me reads v1 channels me and decodes the channel`() {
        enqueue(
            """{"data":{"user_id":"u1","name":"Ada","handle":"ada","about":"Notes","video_count":3,""" +
                """"avatar_url":"https://obj/a.jpg","created_at":"2026-09-05T00:00:00Z"}}""",
        )

        val channel = runBlocking { api.me() }.data!!

        assertThat(server.takeRequest().target).isEqualTo("/v1/channels/me")
        assertThat(channel.handle).isEqualTo("ada")
        assertThat(channel.videoCount).isEqualTo(3)
        assertThat(channel.avatarUrl).isEqualTo("https://obj/a.jpg")
    }

    @Test
    fun `create posts name and handle, and omits a blank about`() {
        enqueue("""{"data":{"user_id":"u1","name":"Ada","handle":"ada"}}""", code = 201)

        runBlocking { api.create(CreateChannelRequest(name = "Ada", handle = "ada", about = null)) }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/channels")
        val body = request.body!!.utf8()
        assertThat(body).contains("\"name\":\"Ada\"")
        assertThat(body).contains("\"handle\":\"ada\"")
        assertThat(body).doesNotContain("about")
    }

    @Test
    fun `handle availability is a query and decodes the suggestion`() {
        enqueue("""{"data":{"available":false,"suggestion":"ada2"}}""")

        val answer = runBlocking { api.handleAvailable("ada") }.data!!

        assertThat(server.takeRequest().target).isEqualTo("/v1/channels/handle-available?handle=ada")
        assertThat(answer.available).isFalse()
        assertThat(answer.suggestion).isEqualTo("ada2")
    }

    @Test
    fun `a channel by handle or id is one path`() {
        enqueue("""{"data":{"user_id":"u1","name":"Ada","handle":"ada"}}""")

        runBlocking { api.get("ada") }

        assertThat(server.takeRequest().target).isEqualTo("/v1/channels/ada")
    }
}
