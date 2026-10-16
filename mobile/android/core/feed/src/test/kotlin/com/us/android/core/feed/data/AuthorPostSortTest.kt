package com.us.android.core.feed.data

import androidx.paging.testing.asSnapshot
import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import kotlinx.coroutines.test.runTest
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
 * The profile grid's sort (T-21): `?sort=` on the by-author read, and a sort
 * change starting a fresh pager — no cursor carried across, because the
 * server mints each cursor for one sort and refuses it under another.
 */
class AuthorPostSortTest {

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var api: VideoFeedApi
    private lateinit var repository: VideoFeedRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val retrofit = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
        api = retrofit.create(VideoFeedApi::class.java)
        val mapper = ErrorMapper(json)
        val identity = FeedItemHydrator { it }
        repository = VideoFeedRepository(
            api = api,
            feeds = FeedRepository(retrofit.create(FeedApi::class.java), mapper, identity),
            errorMapper = mapper,
            hydrator = identity,
        )
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(body: String) {
        server.enqueue(
            MockResponse.Builder().code(200).setHeader("Content-Type", "application/json").body(body).build(),
        )
    }

    private fun page(ids: List<String>, next: String?): String {
        val rows = ids.joinToString(",") { """{"id":"$it","author_id":"u1","content_type":"post"}""" }
        val meta = next?.let { ""","meta":{"next_cursor":"$it"}""" }.orEmpty()
        return """{"data":[$rows]$meta}"""
    }

    private fun RecordedRequest.query(name: String): String? = url.queryParameter(name)

    @Test
    fun `the wire values are the three the server accepts, latest first`() {
        assertThat(AuthorPostSort.entries.map { it.wire }).containsExactly("latest", "popular", "oldest").inOrder()
        assertThat(AuthorPostSort.entries.map { it.label }).containsExactly("Latest", "Popular", "Oldest").inOrder()
    }

    @Test
    fun `the by-author read carries the sort beside the type and cursor`() = runTest {
        enqueue("""{"data":[]}""")

        api.postsByAuthor(authorId = "u1", type = "flick", limit = 15, cursor = "c2", sort = "popular")

        assertThat(server.takeRequest().target)
            .isEqualTo("/v1/posts/by-author/u1?type=flick&limit=15&cursor=c2&sort=popular")
    }

    @Test
    fun `the grid sends its sort on every page and pages with the cursor that sort minted`() = runTest {
        enqueue(page(listOf("a", "b"), next = "pop-1"))
        enqueue(page(listOf("c"), next = null))

        val items = repository.authorPosts("u1", "post", AuthorPostSort.POPULAR).asSnapshot {
            appendScrollWhile { true }
        }

        assertThat(items.map { it.id }).containsExactly("a", "b", "c").inOrder()
        val first = server.takeRequest()
        val second = server.takeRequest()
        assertThat(first.query("sort")).isEqualTo("popular")
        assertThat(first.query("cursor")).isNull()
        assertThat(second.query("sort")).isEqualTo("popular")
        assertThat(second.query("cursor")).isEqualTo("pop-1")
    }

    @Test
    fun `a sort change starts again from the top, with no cursor from the old sort`() = runTest {
        enqueue(page(listOf("a", "b"), next = "pop-1"))
        enqueue(page(listOf("c"), next = null))
        repository.authorPosts("u1", "post", AuthorPostSort.POPULAR).asSnapshot { appendScrollWhile { true } }
        server.takeRequest()
        server.takeRequest()

        enqueue(page(listOf("z"), next = null))
        val items = repository.authorPosts("u1", "post", AuthorPostSort.OLDEST).asSnapshot()

        val request = server.takeRequest()
        assertThat(request.query("sort")).isEqualTo("oldest")
        assertThat(request.query("cursor")).isNull()
        assertThat(items.map { it.id }).containsExactly("z")
    }

    @Test
    fun `the default is latest, sent explicitly`() = runTest {
        enqueue(page(listOf("a"), next = null))

        repository.authorPosts("u1", "post").asSnapshot()

        assertThat(server.takeRequest().query("sort")).isEqualTo("latest")
    }

    @Test
    fun `saved posts read every kind of bookmark`() = runTest {
        enqueue(
            """{"data":[{"id":"p","content_type":"post"},{"id":"r","content_type":"flick"},""" +
                """{"id":"v","content_type":"long_video"}]}""",
        )

        val items = repository.savedPosts().asSnapshot()

        assertThat(server.takeRequest().url.encodedPath).isEqualTo("/v1/posts/bookmarks")
        assertThat(items.map { it.id }).containsExactly("p", "r", "v").inOrder()
    }
}
