package com.us.android.core.feed.data

import androidx.paging.PagingConfig
import androidx.paging.PagingData
import androidx.paging.PagingDataEvent
import androidx.paging.PagingDataPresenter
import androidx.paging.PagingSource
import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.data.dto.FeedAuthorDto
import com.us.android.core.feed.data.dto.FeedItemDto
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiMeta
import com.us.android.core.network.ErrorMapper
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Rule
import org.junit.Test

/**
 * How far a feed pages, and how much of it is held (audit 2026-10-30, A2,
 * and the empty-page rule).
 *
 * - The full-screen stages are bounded: past [FEED_STAGE_MAX_SIZE] rows the
 *   oldest pages behind the reader are dropped, so an endless session's heap
 *   stops growing. Finite lists stay unbounded and keep their top.
 * - An empty page that still carries a cursor pages on; an echoed cursor or
 *   a run of empty pages ends the list instead of crashing or spinning.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class FeedPagingBoundsTest {

    @get:Rule
    val mainDispatcher = MainDispatcherRule()

    private val errorMapper = ErrorMapper(Json { ignoreUnknownKeys = true })

    private fun row(id: String) = FeedItem(
        id = id,
        authorId = "a",
        author = FeedAuthor(id = "a", displayName = "A"),
        text = "",
        visibility = "public",
        feedContentType = "post",
        postType = "text",
        createdAt = "",
        isPinned = false,
        media = emptyList(),
        counts = FeedCounts(likes = 0, comments = 0, reposts = 0, views = 0),
        viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
        isRepostable = true,
    )

    /** A timeline that never ends: page n (cursor "n") holds rows p(n*15)..p(n*15+14). */
    private fun infinitePage(request: FeedPageRequest): FeedPage {
        val n = request.cursor?.toInt() ?: 0
        val from = n * FEED_PAGE_SIZE
        val rows = (from until from + request.limit).map { row("p$it") }
        return FeedPage(items = rows, nextCursor = (n + request.limit / FEED_PAGE_SIZE).toString())
    }

    // ── maxSize ──────────────────────────────────────────────────────

    @Test
    fun `the stage config bounds the rows and keeps placeholders off`() {
        val config = feedPagingConfig(FEED_STAGE_MAX_SIZE)

        assertThat(config.maxSize).isEqualTo(FEED_STAGE_MAX_SIZE)
        assertThat(config.enablePlaceholders).isFalse()
        // Paging's own floor: a bound below it would drop the rows on screen.
        assertThat(config.maxSize).isAtLeast(config.pageSize + 2 * config.prefetchDistance)
        assertThat(config.maxSize).isAtLeast(config.initialLoadSize)
    }

    @Test
    fun `a finite list is unbounded by default`() {
        assertThat(feedPagingConfig().maxSize).isEqualTo(PagingConfig.MAX_SIZE_UNBOUNDED)
    }

    @Test
    fun `scrolling far down a stage feed drops the oldest pages`() = runTest {
        val held = scrollPast(400, feedPager(errorMapper, maxSize = FEED_STAGE_MAX_SIZE, load = ::infinitePage))

        assertThat(held.size).isAtMost(FEED_STAGE_MAX_SIZE)
        assertThat(held).doesNotContain("p0")
        assertThat(held).contains("p400")
    }

    @Test
    fun `scrolling far down an unbounded list keeps every row`() = runTest {
        val held = scrollPast(400, feedPager(errorMapper, load = ::infinitePage))

        assertThat(held.first()).isEqualTo("p0")
        assertThat(held.size).isGreaterThan(FEED_STAGE_MAX_SIZE)
    }

    /**
     * Reads the last row held until it is past row [target], the way a reader
     * swiping down does, and returns the ids then held. A presenter of its own
     * rather than paging-testing's `asSnapshot`, whose scroll keeps absolute
     * indices and so cannot follow a list that drops rows from its start.
     */
    private suspend fun TestScope.scrollPast(target: Int, pages: Flow<PagingData<FeedItem>>): List<String> {
        val presenter = object : PagingDataPresenter<FeedItem>() {
            override suspend fun presentPagingDataEvent(event: PagingDataEvent<FeedItem>) = Unit
        }
        backgroundScope.launch { pages.collectLatest(presenter::collectFrom) }
        settle()
        repeat(MAX_SWIPES) {
            val last = presenter[presenter.size - 1] ?: return@repeat
            if (last.id.removePrefix("p").toInt() > target) return presenter.snapshot().items.map { it.id }
            settle()
        }
        error("never got past row $target")
    }

    private suspend fun TestScope.settle() {
        delay(1)
        advanceUntilIdle()
    }

    // ── Empty pages ──────────────────────────────────────────────────

    private fun source(vararg pages: FeedPage): Pair<FeedPagingSource, MutableList<String?>> {
        val asked = mutableListOf<String?>()
        val queue = ArrayDeque(pages.toList())
        return FeedPagingSource(
            loader = { _, cursor ->
                asked += cursor
                queue.removeFirst()
            },
            errorMapper = errorMapper,
        ) to asked
    }

    private suspend fun FeedPagingSource.page(key: String) = load(
        PagingSource.LoadParams.Append(key = key, loadSize = FEED_PAGE_SIZE, placeholdersEnabled = false),
    ) as PagingSource.LoadResult.Page

    @Test
    fun `an empty page with a cursor pages on`() = runTest {
        val (source, _) = source(FeedPage(items = emptyList(), nextCursor = "c2"))

        assertThat(source.page("c1").nextKey).isEqualTo("c2")
    }

    @Test
    fun `an empty envelope with a cursor keeps its cursor`() {
        val page = ApiEnvelope(data = emptyList<FeedItemDto>(), meta = ApiMeta(nextCursor = "c2")).toFeedPage()

        assertThat(page.items).isEmpty()
        assertThat(page.nextCursor).isEqualTo("c2")
    }

    @Test
    fun `an envelope without a cursor is still the end`() {
        val row = FeedItemDto(id = "p", authorId = "a", author = FeedAuthorDto(id = "a"))

        assertThat(ApiEnvelope(data = listOf(row), meta = null).toFeedPage().nextCursor).isNull()
        assertThat(ApiEnvelope(data = listOf(row), meta = ApiMeta(nextCursor = " ")).toFeedPage().nextCursor).isNull()
    }

    /** Paging throws on a reused key, which would take the feed down. */
    @Test
    fun `a cursor echoed back ends the list`() = runTest {
        val (source, _) = source(FeedPage(items = listOf(row("a")), nextCursor = "c1"))

        assertThat(source.page("c1").nextKey).isNull()
    }

    @Test
    fun `a run of empty pages ends the list, and any row in between starts the count again`() = runTest {
        val empty = { n: Int -> FeedPage(items = emptyList(), nextCursor = "e$n") }
        val (source, _) = source(
            empty(1), empty(2), empty(3), empty(4),
            FeedPage(items = listOf(row("a")), nextCursor = "r"),
            empty(5), empty(6), empty(7), empty(8), empty(9),
        )

        val keys = listOf("k0", "e1", "e2", "e3", "e4", "r", "e5", "e6", "e7", "e8").map { source.page(it).nextKey }

        assertThat(keys).containsExactly("e1", "e2", "e3", "e4", "r", "e5", "e6", "e7", "e8", null).inOrder()
    }

    /** De-duplication can empty a page the server filled; that is not an empty page, or the endless feed would end. */
    @Test
    fun `a page emptied only by de-duplication does not count as empty`() = runTest {
        val repeat = { n: Int -> FeedPage(items = listOf(row("same")), nextCursor = "d$n") }
        val (source, _) = source(repeat(1), repeat(2), repeat(3), repeat(4), repeat(5), repeat(6))

        val pages = listOf("k0", "d1", "d2", "d3", "d4", "d5").map { source.page(it) }

        assertThat(pages.last().nextKey).isEqualTo("d6")
        assertThat(pages.flatMap { it.data }.map { it.id }).containsExactly("same")
    }

    // ── The endless feed ─────────────────────────────────────────────

    @Test
    fun `an echoed primary cursor goes on to the fallback`() = runTest {
        val page = endlessPage(
            cursor = "c1",
            primary = { FeedPage(items = listOf(row("a")), nextCursor = "c1") },
            fallback = { error("not yet") },
        )

        assertThat(page.nextCursor).isEqualTo(FALLBACK_CURSOR_PREFIX)
    }

    @Test
    fun `an empty primary page with a fresh cursor stays on the primary`() = runTest {
        val page = endlessPage(
            cursor = "c1",
            primary = { FeedPage(items = emptyList(), nextCursor = "c2") },
            fallback = { error("the primary has not ended") },
        )

        assertThat(page.nextCursor).isEqualTo("c2")
    }

    private companion object {
        /** Far more page loads than four hundred rows need. */
        const val MAX_SWIPES = 200
    }
}
