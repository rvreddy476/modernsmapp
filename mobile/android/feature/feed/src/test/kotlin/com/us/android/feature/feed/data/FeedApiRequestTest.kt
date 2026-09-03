package com.us.android.feature.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.FeedQuery
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

/**
 * The bytes on the wire for the three home queries and the two hashtag
 * endpoints.
 *
 * The serializer tests prove the responses decode; these prove the REQUESTS
 * are the ones the server reads. feed-service reads `following_only` and
 * `circle_only` as the literal string `true` (handler.go:115-116), and the
 * plain feed must not gain either parameter — so the For You request is
 * checked for their absence as strictly as Following is checked for
 * presence.
 */
class FeedApiRequestTest {

    private val json = Json { ignoreUnknownKeys = true }
    private lateinit var server: MockWebServer
    private lateinit var api: FeedApi

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(FeedApi::class.java)
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(body: String) {
        server.enqueue(MockResponse.Builder().code(200).body(body).build())
    }

    private fun feedRequest(query: FeedQuery): String {
        enqueue("""{"data":[]}""")
        runBlocking {
            api.getFeed(
                surface = query.surface.path,
                limit = 15,
                cursor = null,
                followingOnly = query.followingOnly.takeIf { it },
                circleOnly = query.circleOnly.takeIf { it },
            )
        }
        return server.takeRequest().target
    }

    @Test
    fun `for you asks for the plain home feed`() {
        val target = feedRequest(FeedQuery.ForYou)

        assertThat(target).startsWith("/v1/feed/home?")
        assertThat(target).contains("limit=15")
        assertThat(target).doesNotContain("following_only")
        assertThat(target).doesNotContain("circle_only")
    }

    @Test
    fun `following sends following_only=true and nothing else`() {
        val target = feedRequest(FeedQuery.Following)

        assertThat(target).startsWith("/v1/feed/home?")
        assertThat(target).contains("following_only=true")
        assertThat(target).doesNotContain("circle_only")
    }

    @Test
    fun `friends sends circle_only=true and nothing else`() {
        val target = feedRequest(FeedQuery.Friends)

        assertThat(target).startsWith("/v1/feed/home?")
        assertThat(target).contains("circle_only=true")
        assertThat(target).doesNotContain("following_only")
    }

    @Test
    fun `trending hashtags is the post-service path with a limit`() {
        enqueue("""{"data":{"hashtags":[]}}""")

        val tags = runBlocking { api.getTrendingHashtags(limit = 20) }

        assertThat(server.takeRequest().target).isEqualTo("/v1/hashtags/trending?limit=20")
        assertThat(tags.data!!.hashtags).isEmpty()
    }

    @Test
    fun `posts by hashtag puts the bare tag in the path and defaults to recent`() {
        enqueue("""{"data":[]}""")

        runBlocking { api.getPostsByHashtag(tag = "android", limit = 15) }

        assertThat(server.takeRequest().target).isEqualTo("/v1/hashtags/android/posts?limit=15&sort=recent")
    }

    @Test
    fun `posts by hashtag replays the cursor verbatim`() {
        enqueue("""{"data":[]}""")

        runBlocking { api.getPostsByHashtag(tag = "android", limit = 15, cursor = "djE6ZTcw") }

        assertThat(server.takeRequest().target).contains("cursor=djE6ZTcw")
    }
}
