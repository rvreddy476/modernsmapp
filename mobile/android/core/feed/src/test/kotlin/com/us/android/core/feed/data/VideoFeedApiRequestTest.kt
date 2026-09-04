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

/** Tube's requests, byte for byte: the chip narrowings and the three You-page lists. */
class VideoFeedApiRequestTest {

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var api: VideoFeedApi

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(VideoFeedApi::class.java)
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(body: String) {
        server.enqueue(MockResponse.Builder().code(200).body(body).build())
    }

    @Test
    fun `the plain videos surface sends neither narrowing`() {
        enqueue("""{"data":[]}""")

        runBlocking { api.getFeed(surface = "videos", limit = 15) }

        assertThat(server.takeRequest().target).isEqualTo("/v1/feed/videos?limit=15")
    }

    @Test
    fun `following asks the watch surface with following_only=true`() {
        enqueue("""{"data":[]}""")

        runBlocking { api.getFeed(surface = "watch", limit = 15, followingOnly = true) }

        val target = server.takeRequest().target
        assertThat(target).startsWith("/v1/feed/watch?")
        assertThat(target).contains("following_only=true")
        assertThat(target).doesNotContain("category")
    }

    @Test
    fun `a category chip sends category=slug and nothing else`() {
        enqueue("""{"data":[]}""")

        runBlocking { api.getFeed(surface = "videos", limit = 15, category = "comedy") }

        val target = server.takeRequest().target
        assertThat(target).isEqualTo("/v1/feed/videos?limit=15&category=comedy")
    }

    @Test
    fun `continue watching decodes progress rows keyed by post id`() {
        enqueue(
            """{"data":[{"user_id":"u1","post_id":"p1","position_ms":42000,"duration_ms":120000,""" +
                """"percent_watched":35,"completed":false,"updated_at":"2026-09-05T10:00:00Z"}]}""",
        )

        val rows = runBlocking { api.continueWatching(limit = 10) }

        assertThat(server.takeRequest().target).isEqualTo("/v1/videos/continue-watching?limit=10")
        val row = rows.data!!.single()
        assertThat(row.postId).isEqualTo("p1")
        assertThat(row.positionMs).isEqualTo(42_000L)
        assertThat(row.durationMs).isEqualTo(120_000L)
        assertThat(row.completed).isFalse()
    }

    @Test
    fun `own videos filter by-author to long_video and replay the cursor`() {
        enqueue("""{"data":[]}""")

        runBlocking { api.postsByAuthor(authorId = "u1", type = "long_video", limit = 15, cursor = "c2") }

        assertThat(server.takeRequest().target).isEqualTo("/v1/posts/by-author/u1?type=long_video&limit=15&cursor=c2")
    }

    @Test
    fun `bookmarks is the post-service list`() {
        enqueue("""{"data":[]}""")

        runBlocking { api.bookmarks(limit = 15) }

        assertThat(server.takeRequest().target).isEqualTo("/v1/posts/bookmarks?limit=15")
    }

    @Test
    fun `categories decode id and label`() {
        enqueue("""{"data":[{"id":"comedy","label":"Comedy"},{"id":"music","label":""}]}""")

        val categories = runBlocking { api.categories() }

        assertThat(server.takeRequest().target).isEqualTo("/v1/posts/categories")
        assertThat(categories.data!!.map { it.id }).containsExactly("comedy", "music").inOrder()
    }
}
