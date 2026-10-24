package com.us.android.core.feed.data

import androidx.paging.testing.asSnapshot
import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import kotlinx.coroutines.test.runTest
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

/**
 * The channel page's tabs (2026-10-24): the counts on the channel read
 * (post-service service/channels.go:172-174) and the Live tab's rows —
 * long videos with `source = "live"` (store/postgres/posts.go:139).
 */
class ChannelTabsWireTest {

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var repository: VideoFeedRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val retrofit = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
        val mapper = ErrorMapper(json)
        val identity = FeedItemHydrator { it }
        repository = VideoFeedRepository(
            api = retrofit.create(VideoFeedApi::class.java),
            feeds = FeedRepository(retrofit.create(FeedApi::class.java), mapper, identity),
            errorMapper = mapper,
            hydrator = identity,
        )
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(body: String) {
        server.enqueue(MockResponse.Builder().code(200).setHeader("Content-Type", "application/json").body(body).build())
    }

    @Test
    fun `the channel read carries the tab tally, and an older one leaves it unknown`() {
        val withCounts = json.decodeFromString<ApiEnvelope<ChannelDto>>(
            """{"data":{"user_id":"u1","name":"K","handle":"k","video_count":4,"subscriber_count":9,
               "short_count":2,"live_count":0,"collection_count":3}}""",
        ).data!!
        assertThat(listOf(withCounts.shortCount, withCounts.liveCount, withCounts.collectionCount)).containsExactly(2, 0, 3).inOrder()

        val older = json.decodeFromString<ApiEnvelope<ChannelDto>>("""{"data":{"user_id":"u1","name":"K","handle":"k"}}""").data!!
        assertThat(listOf(older.shortCount, older.liveCount, older.collectionCount)).containsExactly(null, null, null)
    }

    @Test
    fun `the Live tab keeps only recordings and walks past a page of uploads`() = runTest {
        enqueue(
            """{"data":[{"id":"u1","content_type":"long_video","source":"upload"},
                        {"id":"u2","content_type":"long_video"}],"meta":{"next_cursor":"c1"}}""",
        )
        enqueue(
            """{"data":[{"id":"l1","content_type":"long_video","source":"live"},
                        {"id":"r1","content_type":"flick","source":"live"},
                        {"id":"l2","content_type":"long_video","source":" LIVE "}]}""",
        )

        val items = repository.liveVideos("owner").asSnapshot { appendScrollWhile { true } }

        assertThat(items.map { it.id }).containsExactly("l1", "l2").inOrder()
        val first = server.takeRequest()
        assertThat(first.url.encodedPath).isEqualTo("/v1/posts/by-author/owner")
        assertThat(first.url.queryParameter("type")).isEqualTo("long_video")
        assertThat(server.takeRequest().url.queryParameter("cursor")).isEqualTo("c1")
    }
}
