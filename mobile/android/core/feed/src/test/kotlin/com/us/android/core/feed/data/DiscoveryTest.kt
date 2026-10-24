package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import com.us.android.core.network.ErrorMapper
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

/**
 * Discovery (2026-10-24): Reels Explore, Tube Trending and Topics, against
 * the server's own shapes. Fixtures cite the Go that writes them.
 */
class DiscoveryTest {

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var repository: DiscoveryRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(DiscoveryApi::class.java)
        repository = DiscoveryRepository(api, ErrorMapper(json)) { it }
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(body: String, code: Int = 200) {
        server.enqueue(MockResponse.Builder().code(code).body(body).build())
    }

    private fun item(id: String, createdAt: String = "", publishAt: String? = null) = FeedItem(
        id = id,
        authorId = "a",
        author = FeedAuthor(id = "a", displayName = "Ada"),
        text = "",
        visibility = "public",
        feedContentType = "flick",
        postType = "video",
        createdAt = createdAt,
        isPinned = false,
        media = emptyList(),
        counts = FeedCounts(0, 0, 0, 0),
        viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
        isRepostable = true,
        publishAt = publishAt,
    )

    // ── The slug rule ──────────────────────────────────────────────────

    /** feed-service service/category.go:34 and :41, verbatim. */
    @Test
    fun `a chip value is trimmed and lowercased, and only a slug the server takes is valid`() {
        assertThat(normalizeExploreCategory(null)).isEqualTo(ExploreSlug("", valid = true))
        assertThat(normalizeExploreCategory("  ")).isEqualTo(ExploreSlug("", valid = true))
        assertThat(normalizeExploreCategory(" Music ")).isEqualTo(ExploreSlug("music", valid = true))
        assertThat(normalizeExploreCategory("film-animation")).isEqualTo(ExploreSlug("film-animation", valid = true))
        assertThat(normalizeExploreCategory("a_b").valid).isTrue()
        assertThat(normalizeExploreCategory("-music").valid).isFalse()
        assertThat(normalizeExploreCategory("mu sic").valid).isFalse()
        assertThat(normalizeExploreCategory("a".repeat(32)).valid).isTrue()
        assertThat(normalizeExploreCategory("a".repeat(33)).valid).isFalse()
    }

    // ── The walk ───────────────────────────────────────────────────────

    private fun scripted(vararg pages: Pair<List<String>, String?>): Pair<MutableList<String?>, suspend (String?) -> AppResult<Pair<List<FeedItem>, String?>>> {
        val asked = mutableListOf<String?>()
        val queue = ArrayDeque(pages.toList())
        return asked to { cursor ->
            asked += cursor
            val (ids, next) = queue.removeFirst()
            AppResult.Success(ids.map { item(it) } to next)
        }
    }

    @Test
    fun `the walk stops at the first page with reels`() = runBlocking {
        val (asked, fetch) = scripted(emptyList<String>() to "c1", listOf("r1") to "c2", listOf("r2") to null)

        val page = (walkExplore(cursor = null, fetch = fetch) as AppResult.Success).data

        assertThat(page.items.map { it.id }).containsExactly("r1")
        assertThat(page.nextCursor).isEqualTo("c2")
        assertThat(page.requests).isEqualTo(2)
        assertThat(asked).containsExactly(null, "c1").inOrder()
    }

    @Test
    fun `an empty walk gives up after the cap and keeps the cursor to look further`() = runBlocking {
        val (asked, fetch) = scripted(
            emptyList<String>() to "c1",
            emptyList<String>() to "c2",
            emptyList<String>() to "c3",
            emptyList<String>() to "c4",
            listOf("never") to null,
        )

        val page = (walkExplore(cursor = null, fetch = fetch) as AppResult.Success).data

        assertThat(page.items).isEmpty()
        assertThat(page.nextCursor).isEqualTo("c4")
        assertThat(page.requests).isEqualTo(EXPLORE_MAX_HOPS)
        assertThat(asked).hasSize(EXPLORE_MAX_HOPS)
    }

    @Test
    fun `a repeated cursor ends the walk with no cursor, and a blank one is none`() = runBlocking {
        val (_, stuckFetch) = scripted(emptyList<String>() to "same")
        val stuck = (walkExplore(cursor = "same", fetch = stuckFetch) as AppResult.Success).data
        assertThat(stuck.nextCursor).isNull()
        assertThat(stuck.requests).isEqualTo(1)

        val (_, blankFetch) = scripted(emptyList<String>() to "")
        val blank = (walkExplore(cursor = null, fetch = blankFetch) as AppResult.Success).data
        assertThat(blank.nextCursor).isNull()
    }

    @Test
    fun `tiles are every page's reels once, in order`() {
        val pages = listOf(
            ExplorePage(listOf(item("a"), item("b")), "c1", 1),
            ExplorePage(listOf(item("b"), item("c")), null, 1),
        )
        assertThat(exploreTiles(pages).map { it.id }).containsExactly("a", "b", "c").inOrder()
    }

    // ── The wire ───────────────────────────────────────────────────────

    /**
     * `GET /v1/feed/reels?category=` (feed-service internal/http/handler.go:308)
     * answers hydrated rows with `meta.next_cursor` (rankedPageMeta). An
     * empty page WITH a cursor is walked past, not taken as the end.
     */
    @Test
    fun `explore asks reels with the category, and walks an empty page that has a cursor`() = runBlocking {
        enqueue("""{"data":[],"meta":{"next_cursor":"w1"}}""")
        enqueue("""{"data":[{"id":"r1","post_type":"video","content_type":"flick"}],"meta":{"next_cursor":"w2"}}""")

        val page = (repository.explorePage(" Music ", cursor = null) as AppResult.Success).data

        assertThat(server.takeRequest().target).isEqualTo("/v1/feed/reels?limit=18&category=music")
        assertThat(server.takeRequest().target).isEqualTo("/v1/feed/reels?limit=18&cursor=w1&category=music")
        assertThat(page.items.map { it.id }).containsExactly("r1")
        assertThat(page.nextCursor).isEqualTo("w2")
    }

    @Test
    fun `All sends no category at all`() = runBlocking {
        enqueue("""{"data":null}""")

        val page = (repository.explorePage("", cursor = null) as AppResult.Success).data

        assertThat(server.takeRequest().target).isEqualTo("/v1/feed/reels?limit=18")
        assertThat(page.items).isEmpty()
        assertThat(page.nextCursor).isNull()
    }

    @Test
    fun `a malformed category is never sent, and the server's 400 reads the same`() = runBlocking {
        val local = repository.explorePage("not a slug", cursor = null)
        assertThat((local as AppResult.Failure).error.isInvalidCategory()).isTrue()
        assertThat(server.requestCount).isEqualTo(0)

        // feed-service handler.go:310: 400 INVALID_CATEGORY before the service is touched.
        enqueue("""{"error":{"code":"INVALID_CATEGORY","message":"category must be a lowercase slug"}}""", code = 400)
        val remote = repository.explorePage("music", cursor = null)
        assertThat((remote as AppResult.Failure).error.isInvalidCategory()).isTrue()
        assertThat(AppError.NoNetwork().isInvalidCategory()).isFalse()
    }

    /** post-service service/categories.go:41 (`{id, slug, label, kind}`) and handler.go:744 (`?kind=short`). */
    @Test
    fun `short topics come from kind=short, normalised, long-only entries dropped`() = runBlocking {
        enqueue(
            """{"data":[
              {"id":"comedy","slug":"comedy","label":"Comedy","kind":"all"},
              {"id":"music","label":"","kind":"short"},
              {"id":"COMEDY","slug":"comedy","label":"dupe","kind":"all"},
              {"id":"","slug":"","label":"nothing"},
              {"id":"people-blogs","slug":"people-blogs","label":"People & blogs","kind":"long"},
              {"id":"old-row","label":"Old row"}
            ]}""",
        )

        val topics = (repository.topics(TopicKind.SHORT) as AppResult.Success).data

        assertThat(server.takeRequest().target).isEqualTo("/v1/posts/categories?kind=short")
        assertThat(topics).containsExactly(
            Topic("comedy", "Comedy", TopicKind.ALL),
            Topic("music", "Music", TopicKind.SHORT),
            Topic("old-row", "Old row", TopicKind.ALL),
        ).inOrder()
    }

    @Test
    fun `long topics keep all and long, and a blank label is the slug in title case`() {
        val topics = normalizeTopics(
            listOf(
                FeedCategoryDto(id = "film-animation", label = "", kind = "long"),
                FeedCategoryDto(id = "dance", label = "Dance", kind = "short"),
                FeedCategoryDto(id = "news", label = "News", kind = "all"),
            ),
        )
        assertThat(longTopics(topics).map { it.slug to it.label })
            .containsExactly("film-animation" to "Film Animation", "news" to "News").inOrder()
        assertThat(shortTopics(topics).map { it.slug }).containsExactly("dance", "news").inOrder()
    }

    /**
     * `GET /v1/posts/trending` (post-service internal/http/handler.go:2674):
     * the cursor rides in BOTH `meta` and `data` (writeTrendingPage,
     * handler.go:2724), the rows are bare `PostDetail`.
     */
    @Test
    fun `trending asks long videos and reads the items and the cursor`() = runBlocking {
        enqueue(
            """{"data":{"items":[{"id":"v1","content_type":"long_video"},{"id":"gone","deleted_at":"2026-10-01T00:00:00Z"}],
               "next_cursor":"t1"},"meta":{"next_cursor":"t1"}}""",
        )

        val page = (repository.trendingPage(cursor = null) as AppResult.Success).data

        assertThat(server.takeRequest().target).isEqualTo("/v1/posts/trending?content_type=long_video&limit=24")
        assertThat(page.items.map { it.id }).containsExactly("v1")
        assertThat(page.nextCursor).isEqualTo("t1")
    }

    /** A release that wrote only `data.next_cursor` (handler.go:2721, kept for shipped clients) still pages. */
    @Test
    fun `trending pages on the data cursor when meta is absent`() = runBlocking {
        enqueue("""{"data":{"items":[{"id":"v1","content_type":"long_video"}],"next_cursor":"d1"}}""")

        val page = (repository.trendingPage(cursor = null) as AppResult.Success).data

        assertThat(page.nextCursor).isEqualTo("d1")
    }

    @Test
    fun `trending's last page has no meta and an empty data cursor`() = runBlocking {
        enqueue("""{"data":{"items":[],"next_cursor":""}}""")

        val page = (repository.trendingPage(cursor = "t1") as AppResult.Success).data

        assertThat(server.takeRequest().target).isEqualTo("/v1/posts/trending?content_type=long_video&limit=24&cursor=t1")
        assertThat(page.nextCursor).isNull()
    }

    /** feed-service handler.go:364: `sort=recent|popular` beside `category` on /v1/feed/videos. */
    @Test
    fun `a topic page asks videos with the slug and the sort`() = runBlocking {
        enqueue("""{"data":[{"id":"v1","content_type":"long_video"}],"meta":{"next_cursor":"n"}}""")

        val page = (repository.topicPage("Film-Animation", TopicSort.POPULAR, cursor = null) as AppResult.Success).data

        assertThat(server.takeRequest().target)
            .isEqualTo("/v1/feed/videos?limit=20&category=film-animation&sort=popular")
        assertThat(page.nextCursor).isEqualTo("n")
    }

    // ── Trending's period, on the client ───────────────────────────────

    @Test
    fun `the period narrows by when the post went live, an unparsable date is out`() {
        val now = java.time.Instant.parse("2026-10-24T12:00:00Z").toEpochMilli()
        val rows = listOf(
            item("hour", createdAt = "2026-10-24T11:00:00Z"),
            item("days", createdAt = "2026-10-20T12:00:00Z"),
            item("weeks", createdAt = "2026-10-03T12:00:00Z"),
            item("months", createdAt = "2026-08-01T12:00:00Z"),
            item("scheduled", createdAt = "2026-07-01T00:00:00Z", publishAt = "2026-10-24T10:00:00+05:30"),
            item("junk", createdAt = "yesterday"),
            item("future", createdAt = "2026-10-24T12:05:00Z"),
        )
        assertThat(filterByPeriod(rows, TrendingPeriod.TODAY, now).map { it.id })
            .containsExactly("hour", "scheduled").inOrder()
        assertThat(filterByPeriod(rows, TrendingPeriod.WEEK, now).map { it.id })
            .containsExactly("hour", "days", "scheduled").inOrder()
        assertThat(filterByPeriod(rows, TrendingPeriod.MONTH, now).map { it.id })
            .containsExactly("hour", "days", "weeks", "scheduled").inOrder()
    }

    @Test
    fun `the edges of a period`() {
        val now = 10L * 24 * 60 * 60 * 1000
        val dayAgo = java.time.Instant.ofEpochMilli(now - 24L * 60 * 60 * 1000).toString()
        val justOver = java.time.Instant.ofEpochMilli(now - 24L * 60 * 60 * 1000 - 1).toString()
        val skew = java.time.Instant.ofEpochMilli(now + 60_000).toString()
        val tooFar = java.time.Instant.ofEpochMilli(now + 60_001).toString()
        assertThat(withinPeriod(dayAgo, TrendingPeriod.TODAY, now)).isTrue()
        assertThat(withinPeriod(justOver, TrendingPeriod.TODAY, now)).isFalse()
        assertThat(withinPeriod(skew, TrendingPeriod.TODAY, now)).isTrue()
        assertThat(withinPeriod(tooFar, TrendingPeriod.TODAY, now)).isFalse()
        assertThat(withinPeriod("", TrendingPeriod.MONTH, now)).isFalse()
        assertThat(withinPeriod(null, TrendingPeriod.MONTH, now)).isFalse()
    }
}
