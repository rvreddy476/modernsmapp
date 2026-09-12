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

    // ── Subscriptions (2026-09-12) ───────────────────────────────────

    @Test
    fun `a channel read decodes the subscriber count and the viewer's edge`() {
        enqueue(
            """{"data":{"user_id":"u1","name":"Ada","handle":"ada","video_count":3,""" +
                """"subscriber_count":1200,"is_subscribed":true,"notify_on":"none"}}""",
        )

        val channel = runBlocking { api.get("ada") }.data!!

        assertThat(channel.subscriberCount).isEqualTo(1200)
        assertThat(channel.isSubscribed).isTrue()
        assertThat(channel.notifyOn).isEqualTo("none")
    }

    @Test
    fun `a public channel read carries no edge`() {
        enqueue("""{"data":{"user_id":"u1","name":"Ada","handle":"ada","subscriber_count":7}}""")

        val channel = runBlocking { api.get("ada") }.data!!

        assertThat(channel.isSubscribed).isNull()
        assertThat(channel.notifyOn).isNull()
    }

    @Test
    fun `subscribe posts the ref path with the bell in the body`() {
        enqueue("""{"data":{"status":"subscribed","notify_on":"all","follow":"followed","subscriber_count":8}}""")

        val answer = runBlocking { api.subscribe("u1", SubscribeRequest(notifyOn = "all")) }.data!!

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/channels/u1/subscribe")
        assertThat(request.body!!.utf8()).isEqualTo("""{"notify_on":"all"}""")
        assertThat(answer.status).isEqualTo("subscribed")
        assertThat(answer.subscriberCount).isEqualTo(8)
    }

    /** The app's JSON drops nulls: no bell in the request means the server's default, not `null`. */
    @Test
    fun `subscribe with no bell sends an empty body`() {
        enqueue("""{"data":{"status":"subscribed","notify_on":"all","subscriber_count":1}}""")

        runBlocking { api.subscribe("u1", SubscribeRequest()) }

        assertThat(server.takeRequest().body!!.utf8()).isEqualTo("{}")
    }

    @Test
    fun `unsubscribe deletes the same path`() {
        enqueue("""{"data":{"status":"unsubscribed","subscriber_count":7}}""")

        val answer = runBlocking { api.unsubscribe("u1") }.data!!

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("DELETE")
        assertThat(request.target).isEqualTo("/v1/channels/u1/subscribe")
        assertThat(answer.status).isEqualTo("unsubscribed")
    }

    @Test
    fun `the bell patches the subscription with notify_on`() {
        enqueue("""{"data":{"subscribed":true,"notify_on":"none"}}""")

        val answer = runBlocking { api.updateSubscription("u1", NotifyOnRequest(notifyOn = "none")) }.data!!

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("PATCH")
        assertThat(request.target).isEqualTo("/v1/channels/u1/subscription")
        assertThat(request.body!!.utf8()).isEqualTo("""{"notify_on":"none"}""")
        assertThat(answer.subscribed).isTrue()
        assertThat(answer.notifyOn).isEqualTo("none")
    }

    @Test
    fun `the subscription read is its own path and decodes an absent edge`() {
        enqueue("""{"data":{"subscribed":false}}""")

        val answer = runBlocking { api.subscription("u1") }.data!!

        assertThat(server.takeRequest().target).isEqualTo("/v1/channels/u1/subscription")
        assertThat(answer.subscribed).isFalse()
        assertThat(answer.notifyOn).isNull()
    }

    @Test
    fun `the subscriptions list is paged and decodes each channel summary`() {
        enqueue(
            """{"data":[{"channel":{"user_id":"u2","name":"Bob","handle":"bob",""" +
                """"avatar_url":"https://obj/b.jpg","subscriber_count":42},"notify_on":"all",""" +
                """"subscribed_at":"2026-09-12T00:00:00Z"}],"meta":{"next_cursor":"c2"}}""",
        )

        val envelope = runBlocking { api.subscriptions(limit = 50) }

        assertThat(server.takeRequest().target).isEqualTo("/v1/channels/subscriptions?limit=50")
        val row = envelope.data!!.single()
        assertThat(row.channel.userId).isEqualTo("u2")
        assertThat(row.channel.handle).isEqualTo("bob")
        assertThat(row.channel.subscriberCount).isEqualTo(42)
        assertThat(row.notifyOn).isEqualTo("all")
        assertThat(row.subscribedAt).isEqualTo("2026-09-12T00:00:00Z")
        assertThat(envelope.meta?.nextCursor).isEqualTo("c2")
    }
}
