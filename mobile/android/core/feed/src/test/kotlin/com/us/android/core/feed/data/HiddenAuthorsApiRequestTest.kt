package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.profile.data.FollowListApi
import kotlinx.coroutines.runBlocking
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okhttp3.MediaType.Companion.toMediaType
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

/**
 * Settings › Hidden channels (2026-10-22) against feed-service's handlers
 * (`GetMutedAuthors`: an envelope whose `data` is `[{author_id, created_at}]`,
 * newest first; `PostFeedback` with `author_id` and `interested` is the
 * undo) and profile-service's bare `POST /v1/profiles/batch` map, which the
 * web's page uses for the names.
 */
class HiddenAuthorsApiRequestTest {

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var repository: HiddenAuthorsRepository

    /** Path → (status, body). A path with no answer is a 500. */
    private val answers = mutableMapOf<String, Pair<Int, String>>()
    private val seen = mutableListOf<RecordedRequest>()

    @Before
    fun setUp() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                synchronized(seen) { seen += request }
                val (code, body) = answers[request.url.encodedPath] ?: (500 to """{"error":{"code":"INTERNAL_ERROR"}}""")
                return MockResponse.Builder().code(code).body(body).build()
            }
        }
        server.start()
        val retrofit = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
        repository = HiddenAuthorsRepository(
            api = retrofit.create(HiddenAuthorsApi::class.java),
            feed = retrofit.create(FeedApi::class.java),
            profiles = retrofit.create(FollowListApi::class.java),
            errorMapper = ErrorMapper(json),
        )
    }

    @After
    fun tearDown() = server.close()

    private fun request(path: String) = seen.single { it.url.encodedPath == path }

    @Test
    fun `the list is the feedback authors, named through the profiles batch`() {
        answers["/v1/feed/feedback/authors"] = 200 to
            """{"data":[{"author_id":"a1","created_at":"2026-10-20T09:00:00Z"},{"author_id":"a2","created_at":"2026-10-19T09:00:00Z"}]}"""
        answers["/v1/profiles/batch"] = 200 to
            """{"a1":{"user_id":"a1","username":"nonna","display_name":"Nonna","avatar_media_id":"m1"}}"""

        val result = runBlocking { repository.hidden() }

        assertThat(request("/v1/feed/feedback/authors").method).isEqualTo("GET")
        val batch = request("/v1/profiles/batch")
        assertThat(batch.method).isEqualTo("POST")
        assertThat(batch.body?.utf8()).isEqualTo("""{"user_ids":["a1","a2"]}""")
        val rows = (result as AppResult.Success).data
        assertThat(rows.map { it.authorId }).containsExactly("a1", "a2").inOrder()
        assertThat(rows[0].name).isEqualTo("Nonna")
        assertThat(rows[0].username).isEqualTo("nonna")
        assertThat(rows[0].avatarMediaId).isEqualTo("m1")
        assertThat(rows[0].hiddenAt).isEqualTo("2026-10-20T09:00:00Z")
        // The batch left a2 out (blocked, deactivated): it still lists, nameless, and can still be shown again.
        assertThat(rows[1].name).isEmpty()
    }

    @Test
    fun `nothing hidden is an empty list, with no profiles read`() {
        answers["/v1/feed/feedback/authors"] = 200 to """{"data":[]}"""

        val result = runBlocking { repository.hidden() }

        assertThat((result as AppResult.Success).data).isEmpty()
        assertThat(seen.none { it.url.encodedPath == "/v1/profiles/batch" }).isTrue()
    }

    /** The names are a nicety: a failed batch must not fail the page. */
    @Test
    fun `a failed profiles batch still lists the rows`() {
        answers["/v1/feed/feedback/authors"] = 200 to """{"data":[{"author_id":"a1","created_at":"2026-10-20T09:00:00Z"}]}"""

        val result = runBlocking { repository.hidden() }

        assertThat((result as AppResult.Success).data.single().authorId).isEqualTo("a1")
    }

    @Test
    fun `a failed list is a failure`() {
        answers["/v1/feed/feedback/authors"] = 502 to """{"error":{"code":"FEEDBACK_FAILED","message":"try again"}}"""

        val result = runBlocking { repository.hidden() }

        assertThat(result).isInstanceOf(AppResult.Failure::class.java)
    }

    @Test
    fun `show again is the author-scoped interested signal`() {
        answers["/v1/feed/feedback"] = 200 to """{"data":{"author_id":"a1","signal":"interested"}}"""

        val result = runBlocking { repository.showAgain("a1") }

        val post = request("/v1/feed/feedback")
        assertThat(post.method).isEqualTo("POST")
        assertThat(post.body?.utf8()).isEqualTo("""{"signal":"interested","author_id":"a1"}""")
        assertThat(result).isInstanceOf(AppResult.Success::class.java)
    }

    @Test
    fun `a refused show again is a failure`() {
        answers["/v1/feed/feedback"] = 502 to """{"error":{"code":"FEEDBACK_FAILED","message":"try again"}}"""

        val result = runBlocking { repository.showAgain("a1") }

        assertThat(result).isInstanceOf(AppResult.Failure::class.java)
    }
}
