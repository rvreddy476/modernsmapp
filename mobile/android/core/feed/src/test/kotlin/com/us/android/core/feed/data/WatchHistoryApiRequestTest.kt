package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedChannel
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import kotlinx.coroutines.runBlocking
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
 * Tube's History (2026-10-22) byte for byte against post-service's handlers
 * (`video_series_handler.go`: `GetWatchHistory`, `ClearWatchHistory`,
 * `DeleteWatchProgress`). The page body is the server's own shape: the
 * `ContinueWatchingItem` — the embedded `WatchProgress` fields (`updated_at`
 * is `last_watched_at` on the wire) beside the hydrated `post` — with
 * `meta.next_cursor` absent on the last page.
 */
class WatchHistoryApiRequestTest {

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var repository: WatchHistoryRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(WatchHistoryApi::class.java)
        repository = WatchHistoryRepository(api, ErrorMapper(json)) { it }
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(body: String, code: Int = 200) {
        server.enqueue(MockResponse.Builder().code(code).body(body).build())
    }

    private fun RecordedRequest.line(): String = "$method $target"

    /** One row as `GetWatchHistory` writes it. */
    private fun row(postId: String, position: Long, duration: Long, completed: Boolean, post: String? = null) =
        """{"user_id":"me","post_id":"$postId","position_ms":$position,"duration_ms":$duration,""" +
            """"percent_watched":${if (duration > 0) position * 100 / duration else 0},"completed":$completed,""" +
            """"updated_at":"2026-10-21T10:00:00Z","post":${post ?: """{"id":"$postId","content_type":"long_video","title":"Video $postId"}"""}}"""

    @Test
    fun `the first page asks for thirty with no cursor, and carries the next cursor`() {
        enqueue("""{"data":[${row("a", 60_000, 600_000, false)},${row("b", 0, 300_000, true)}],"meta":{"next_cursor":"c2"}}""")

        val result = runBlocking { repository.page(cursor = null) }

        assertThat(server.takeRequest().line()).isEqualTo("GET /v1/videos/history?limit=30")
        val page = (result as AppResult.Success).data
        assertThat(page.items.map { it.postId }).containsExactly("a", "b").inOrder()
        assertThat(page.nextCursor).isEqualTo("c2")
        val first = page.items.first()
        assertThat(first.positionMs).isEqualTo(60_000)
        assertThat(first.durationMs).isEqualTo(600_000)
        assertThat(first.completed).isFalse()
        assertThat(first.watchedAt).isEqualTo("2026-10-21T10:00:00Z")
        assertThat(first.item.title).isEqualTo("Video a")
        assertThat(page.items[1].fraction).isEqualTo(1f)
    }

    @Test
    fun `a later page sends the cursor it was given, and the last page has none`() {
        enqueue("""{"data":[${row("c", 1_000, 2_000, false)}]}""")

        val result = runBlocking { repository.page(cursor = "c2") }

        assertThat(server.takeRequest().line()).isEqualTo("GET /v1/videos/history?limit=30&cursor=c2")
        assertThat((result as AppResult.Success).data.nextCursor).isNull()
    }

    /** An empty history is `"data": []` (the handler never sends null), and also survives a Go null. */
    @Test
    fun `an empty history is an empty page, not a failure`() {
        enqueue("""{"data":null}""")

        val result = runBlocking { repository.page(cursor = null) }

        assertThat((result as AppResult.Success).data.items).isEmpty()
    }

    @Test
    fun `a row whose post is gone or missing is dropped, and a repeated post shows once`() {
        enqueue(
            """{"data":[${row("a", 1, 10, false)},""" +
                row("gone", 1, 10, false, post = """{"id":"gone","deleted_at":"2026-10-20T00:00:00Z"}""") + "," +
                row("bare", 1, 10, false, post = "null") + "," +
                row("a", 5, 10, false) + "]}",
        )

        val result = runBlocking { repository.page(cursor = null) }

        assertThat((result as AppResult.Success).data.items.map { it.postId }).containsExactly("a")
    }

    @Test
    fun `a refused page is a failure`() {
        enqueue("""{"error":{"code":"INVALID_CURSOR","message":"cursor is not one this service issued"}}""", code = 400)

        val result = runBlocking { repository.page(cursor = "forged") }

        assertThat(result).isInstanceOf(AppResult.Failure::class.java)
    }

    @Test
    fun `removing one is a DELETE on the video's progress, and 204 is success`() {
        server.enqueue(MockResponse.Builder().code(204).build())

        val result = runBlocking { repository.remove("p1") }

        val request = server.takeRequest()
        assertThat(request.line()).isEqualTo("DELETE /v1/videos/p1/progress")
        assertThat(request.body?.utf8().orEmpty()).isEmpty()
        assertThat(result).isInstanceOf(AppResult.Success::class.java)
    }

    @Test
    fun `clearing is one DELETE on the history, and 204 is success`() {
        server.enqueue(MockResponse.Builder().code(204).build())

        val result = runBlocking { repository.clear() }

        assertThat(server.takeRequest().line()).isEqualTo("DELETE /v1/videos/history")
        assertThat(result).isInstanceOf(AppResult.Success::class.java)
    }

    @Test
    fun `a refused clear is a failure the page can roll back on`() {
        enqueue("""{"error":{"code":"INTERNAL_ERROR","message":"boom"}}""", code = 500)

        val result = runBlocking { repository.clear() }

        assertThat(result).isInstanceOf(AppResult.Failure::class.java)
    }

    // ── Search ───────────────────────────────────────────────────────────

    private fun entry(id: String, title: String = "", channel: String? = null, author: String = "") = WatchHistoryEntry(
        item = FeedItem(
            id = id,
            authorId = "u",
            author = FeedAuthor(id = "u", displayName = author, username = null),
            text = "",
            title = title,
            visibility = "public",
            feedContentType = "long_video",
            postType = "video",
            createdAt = "",
            isPinned = false,
            media = emptyList(),
            counts = FeedCounts(likes = 0, comments = 0, reposts = 0, views = 0),
            viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
            isRepostable = false,
            channel = channel?.let { FeedChannel(userId = "u", name = it, handle = it.lowercase()) },
        ),
        positionMs = 0,
        durationMs = 0,
        completed = false,
        watchedAt = "",
    )

    @Test
    fun `search keeps every row for a blank query and matches title, channel and author case-insensitively`() {
        val rows = listOf(
            entry("1", title = "Cooking Pasta", channel = "Nonna"),
            entry("2", title = "Bike repair", author = "Garage Guy"),
            entry("3"),
        )

        assertThat(filterHistory(rows, "  ")).hasSize(3)
        assertThat(filterHistory(rows, "pasta").map { it.postId }).containsExactly("1")
        assertThat(filterHistory(rows, "NONNA").map { it.postId }).containsExactly("1")
        assertThat(filterHistory(rows, "garage").map { it.postId }).containsExactly("2")
        assertThat(filterHistory(rows, "zzz")).isEmpty()
    }
}
