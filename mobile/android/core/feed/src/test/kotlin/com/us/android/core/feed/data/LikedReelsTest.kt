package com.us.android.core.feed.data

import androidx.paging.testing.asSnapshot
import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.data.dto.FeedItemDto
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
 * The owner's Liked tab (2026-10-17): `GET /v1/reels/liked` pages ids by
 * cursor, and each page becomes posts through one `POST /v1/posts/batch`,
 * kept in liked order.
 */
class LikedReelsTest {

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
        server.enqueue(
            MockResponse.Builder().code(200).setHeader("Content-Type", "application/json").body(body).build(),
        )
    }

    private fun ids(vararg ids: String, next: String? = null): String {
        val meta = next?.let { ""","meta":{"next_cursor":"$it"}""" }.orEmpty()
        return """{"data":[${ids.joinToString(",") { "\"$it\"" }}]$meta}"""
    }

    /** The batch answer: a map keyed by id, in no particular order. */
    private fun batch(vararg rows: Pair<String, String>): String =
        """{"data":{${rows.joinToString(",") { (id, type) -> """"$id":{"id":"$id","content_type":"$type"}""" }}}}"""

    private fun RecordedRequest.query(name: String): String? = url.queryParameter(name)

    @Test
    fun `paging follows next_cursor and each page is one batch read in liked order`() = runTest {
        enqueue(ids("r2", "r1", next = "c1"))
        enqueue(batch("r1" to "flick", "r2" to "flick"))
        enqueue(ids("r3"))
        enqueue(batch("r3" to "reel"))

        val items = repository.likedReels().asSnapshot { appendScrollWhile { true } }

        assertThat(items.map { it.id }).containsExactly("r2", "r1", "r3").inOrder()
        val firstIds = server.takeRequest()
        assertThat(firstIds.url.encodedPath).isEqualTo("/v1/reels/liked")
        assertThat(firstIds.query("cursor")).isNull()
        val firstBatch = server.takeRequest()
        assertThat(firstBatch.method).isEqualTo("POST")
        assertThat(firstBatch.url.encodedPath).isEqualTo("/v1/posts/batch")
        assertThat(firstBatch.body?.utf8()).isEqualTo("""{"ids":["r2","r1"]}""")
        assertThat(server.takeRequest().query("cursor")).isEqualTo("c1")
        assertThat(server.takeRequest().url.encodedPath).isEqualTo("/v1/posts/batch")
    }

    /** The server filters what the viewer may no longer open: a page can be empty and still go on. */
    @Test
    fun `an empty page with a cursor is walked past, not taken for the end`() = runTest {
        enqueue(ids(next = "c1"))
        enqueue(ids("r9"))
        enqueue(batch("r9" to "flick"))

        val items = repository.likedReels().asSnapshot()

        assertThat(items.map { it.id }).containsExactly("r9")
        server.takeRequest()
        assertThat(server.takeRequest().query("cursor")).isEqualTo("c1")
    }

    @Test
    fun `nothing liked is an empty list with no batch read`() = runTest {
        enqueue(ids())

        val items = repository.likedReels().asSnapshot()

        assertThat(items).isEmpty()
        assertThat(server.requestCount).isEqualTo(1)
    }

    @Test
    fun `only reels the batch returned are kept, in the liked order, each once`() {
        val rows = mapOf(
            "a" to FeedItemDto(id = "a", contentType = "flick"),
            "b" to FeedItemDto(id = "b", contentType = "post"),
            "c" to FeedItemDto(id = "c", contentType = "reel", deletedAt = "2026-10-01T00:00:00Z"),
            "d" to null,
            "e" to FeedItemDto(id = "e", contentType = "short"),
        )

        val items = likedInOrder(listOf("e", "a", "b", "c", "d", "missing", "a"), rows)

        assertThat(items.map { it.id }).containsExactly("e", "a").inOrder()
    }
}
