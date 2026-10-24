package com.us.android.core.feed.data.hashtag

import androidx.paging.testing.asSnapshot
import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.FeedApi
import com.us.android.core.feed.data.FeedItemHydrator
import com.us.android.core.feed.data.FeedRepository
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
 * Followed hashtags against post-service (server commit 546eec063):
 *
 *  - the follow routes: hashtag_follows_handler.go:16-18, each answering
 *    `{tag, display_name, following}` (service/hashtag_follows.go:86-94);
 *  - the list: :19-20 and :89-105, `[{tag, display_name, followed_at}]` with
 *    `meta.next_cursor` (service/hashtag_follows.go:154-158);
 *  - the posts: :21-22 and :107-126, the hashtag page's rows, a page that
 *    may be empty and still carry a cursor (service :177-180);
 *  - the refusals: :30-45.
 */
class HashtagFollowsContractTest {

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var session: HashtagFollowSession
    private lateinit var repository: HashtagFollowRepository
    private lateinit var feeds: FeedRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val retrofit = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
        val mapper = ErrorMapper(json)
        session = HashtagFollowSession()
        repository = HashtagFollowRepository(retrofit.create(HashtagFollowApi::class.java), session, mapper)
        feeds = FeedRepository(retrofit.create(FeedApi::class.java), mapper, FeedItemHydrator { it })
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(body: String, code: Int = 200) {
        server.enqueue(
            MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build(),
        )
    }

    @Test
    fun `follow posts to the tag's follow route and remembers the answer`() = runTest {
        enqueue("""{"data":{"tag":"android","display_name":"#android","following":true}}""")

        val follow = (repository.setFollowing("#Android", follow = true) as AppResult.Success).data

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/hashtags/android/follow")
        assertThat(follow).isEqualTo(HashtagFollow("android", "#android", following = true))
        assertThat(session.following.value).containsExactly("android", true)
    }

    @Test
    fun `unfollow deletes and the state read gets, on the same route`() = runTest {
        enqueue("""{"data":{"tag":"android","display_name":"#android","following":false}}""")
        repository.setFollowing("android", follow = false)
        val unfollow = server.takeRequest()
        assertThat(unfollow.method).isEqualTo("DELETE")
        assertThat(unfollow.target).isEqualTo("/v1/hashtags/android/follow")
        assertThat(session.following.value["android"]).isFalse()

        enqueue("""{"data":{"tag":"kotlin","display_name":"#kotlin","following":true}}""")
        val state = (repository.state("kotlin") as AppResult.Success).data
        val read = server.takeRequest()
        assertThat(read.method).isEqualTo("GET")
        assertThat(read.target).isEqualTo("/v1/hashtags/kotlin/follow")
        assertThat(state.following).isTrue()
    }

    @Test
    fun `the followed list decodes its rows and carries the cursor`() = runTest {
        enqueue(
            """{"data":[{"tag":"android","display_name":"#android","followed_at":"2026-10-24T08:00:00Z"},""" +
                """{"tag":"kotlin","display_name":"#kotlin","followed_at":"2026-10-23T08:00:00Z"},""" +
                """{"tag":"Android","display_name":"","followed_at":"2026-10-22T08:00:00Z"}],""" +
                """"meta":{"next_cursor":"c2"}}""",
        )

        val page = (repository.followed(limit = 50, cursor = null) as AppResult.Success).data

        assertThat(server.takeRequest().target).isEqualTo("/v1/hashtags/followed?limit=50")
        assertThat(page.tags).containsExactly(FollowedHashtag("android", "#android"), FollowedHashtag("kotlin", "#kotlin")).inOrder()
        assertThat(page.nextCursor).isEqualTo("c2")
        assertThat(session.following.value).containsExactly("android", true, "kotlin", true)
    }

    @Test
    fun `the last page of the list has no cursor`() = runTest {
        enqueue("""{"data":[]}""")
        val page = (repository.followed(limit = 50, cursor = "c2") as AppResult.Success).data
        assertThat(server.takeRequest().target).isEqualTo("/v1/hashtags/followed?limit=50&cursor=c2")
        assertThat(page).isEqualTo(FollowedHashtagsPage(emptyList(), null))
    }

    @Test
    fun `the posts feed walks past an empty page that still has a cursor`() = runTest {
        enqueue("""{"data":[],"meta":{"next_cursor":"c2"}}""")
        enqueue("""{"data":[{"id":"p1","content_type":"post"}]}""")

        val items = feeds.followedHashtagPosts().asSnapshot()

        assertThat(server.takeRequest().url.encodedPath).isEqualTo("/v1/hashtags/followed/posts")
        assertThat(server.takeRequest().url.queryParameter("cursor")).isEqualTo("c2")
        assertThat(items.map { it.id }).containsExactly("p1")
    }

    @Test
    fun `each follow refusal reads as its own sentence`() = runTest {
        suspend fun refusal(code: Int, errorCode: String): String {
            enqueue("""{"error":{"code":"$errorCode","message":"server words"}}""", code = code)
            val result = repository.setFollowing("android", follow = true)
            server.takeRequest()
            return hashtagFollowRefusal((result as AppResult.Failure).error, HashtagFollowAction.FOLLOW)
        }

        assertThat(refusal(400, "INVALID_HASHTAG"))
            .isEqualTo("That isn't a hashtag that can be followed. A hashtag is 1 to 50 letters, numbers or underscores.")
        assertThat(refusal(404, "HASHTAG_UNAVAILABLE")).isEqualTo("This hashtag is unavailable, so it can't be followed.")
        assertThat(refusal(409, "HASHTAG_FOLLOW_LIMIT")).isEqualTo("You can follow up to 500 hashtags. Unfollow one to follow another.")
        assertThat(refusal(503, "STORE_UNAVAILABLE")).isEqualTo("Hashtags are unavailable right now. Try again in a moment.")
        assertThat(refusal(500, "INTERNAL_ERROR")).isEqualTo(HashtagFollowAction.FOLLOW.fallback)
        // A refusal is not remembered as a follow.
        assertThat(session.following.value).isEmpty()
    }

    @Test
    fun `a tag is addressed in the server's stored form`() {
        assertThat(hashtagKey(" #Android ")).isEqualTo("android")
        assertThat(hashtagLabel("android")).isEqualTo("#android")
        assertThat(hashtagLabel("android", "#Android")).isEqualTo("#Android")
        assertThat(hashtagFollowRefusal(AppError.NoNetwork(), HashtagFollowAction.LIST))
            .isEqualTo("You seem to be offline. Check your connection and try again.")
    }
}
