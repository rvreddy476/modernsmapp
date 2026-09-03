package com.us.android.feature.feed.data

import androidx.paging.PagingSource
import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.FeedSurface
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiMeta
import com.us.android.core.network.ErrorMapper
import com.us.android.feature.feed.data.dto.FeedAuthorDto
import com.us.android.feature.feed.data.dto.FeedItemDto
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Test

/**
 * The feed must never emit the same post id twice — Slice D follow-up.
 *
 * ## THIS IS A CRASH, NOT A COSMETIC ISSUE
 *
 * `FeedScreen` keys its `LazyColumn` by post id. A repeated key throws
 *
 * ```
 * IllegalArgumentException: Key "691d0f37-…" was already used.
 * ```
 *
 * which takes down the FEED — the first screen after login — so the app dies
 * seconds after signing in and looks like a broken install. It was reproduced
 * on a device and then on an emulator before this fix.
 *
 * ## WHY THE SERVER CAN LEGITIMATELY REPEAT AN ID
 *
 * The timeline is time-ordered and assembled by fan-out. A post can reach a
 * viewer through more than one path, and a post inserted between two page
 * fetches shifts every later row into the next page — so the same id genuinely
 * arrives twice. The client cannot control that and must not be fragile to it.
 */
class FeedPagingDeduplicationTest {

    private val json = Json { ignoreUnknownKeys = true }

    private fun item(id: String) = FeedItemDto(
        id = id,
        authorId = "author-1",
        text = "post $id",
        author = FeedAuthorDto(id = "author-1", displayName = "Author"),
    )

    /** Serves pre-canned pages so the duplication is the test's to choose. */
    private class FakeFeedApi(private val pages: List<List<FeedItemDto>>) : FeedApi {
        var calls = 0

        override suspend fun getFeed(
            surface: String,
            limit: Int,
            cursor: String?,
            followingOnly: Boolean?,
            circleOnly: Boolean?,
        ): ApiEnvelope<List<FeedItemDto>> {
            val index = calls++
            val isLast = index == pages.lastIndex
            return ApiEnvelope(
                data = pages[index],
                meta = if (isLast) null else ApiMeta(nextCursor = "cursor-${index + 1}"),
            )
        }

        override suspend fun getDelta(
            feedType: String,
            anchor: String,
            limit: Int,
        ) = error("the paging source never asks for a delta")

        override suspend fun votePoll(
            postId: String,
            body: PollVoteRequest,
        ) = error("the paging source never votes")

        override suspend fun getTrendingHashtags(limit: Int) = error("the paging source never lists tags")

        override suspend fun getPostsByHashtag(
            tag: String,
            limit: Int,
            cursor: String?,
            sort: String,
        ) = error("these pages are the home surface")
    }

    // The home loader exactly as the repository builds it: the source itself
    // no longer knows which surface it pages.
    private fun source(pages: List<List<FeedItemDto>>): FeedPagingSource {
        val api = FakeFeedApi(pages)
        return FeedPagingSource(
            loader = { limit, cursor -> api.getFeed(FeedSurface.Home.path, limit, cursor).toFeedPage() },
            errorMapper = ErrorMapper(json),
        )
    }

    private suspend fun load(source: FeedPagingSource, cursor: String?) =
        source.load(
            PagingSource.LoadParams.Refresh(key = cursor, loadSize = 20, placeholdersEnabled = false),
        ) as PagingSource.LoadResult.Page

    @Test
    fun `a duplicate inside one page is emitted once`() = runTest {
        val source = source(listOf(listOf(item("a"), item("b"), item("a"))))

        val page = load(source, null)

        assertThat(page.data.map { it.id }).containsExactly("a", "b").inOrder()
    }

    /**
     * The case page-local filtering cannot catch.
     *
     * A post present on page 1 arrives again on page 2 because rows shifted.
     * De-duplication therefore has to be scoped to the paging SOURCE, which
     * lives for the whole generation, not to a single `load` call.
     */
    @Test
    fun `a duplicate across two pages is emitted once`() = runTest {
        val source = source(
            listOf(
                listOf(item("a"), item("b")),
                listOf(item("b"), item("c")),
            ),
        )

        val first = load(source, null)
        val second = load(source, "cursor-1")

        assertThat(first.data.map { it.id }).containsExactly("a", "b").inOrder()
        assertThat(second.data.map { it.id }).containsExactly("c")
    }

    /** First occurrence wins, so the server's ordering is preserved. */
    @Test
    fun `the first occurrence is the one kept`() = runTest {
        val source = source(listOf(listOf(item("a"), item("b"), item("c"), item("b"))))

        val page = load(source, null)

        assertThat(page.data.map { it.id }).containsExactly("a", "b", "c").inOrder()
    }

    /**
     * A fresh generation starts clean.
     *
     * Paging constructs a new source on refresh. If the seen-set outlived that,
     * a pull-to-refresh would return an empty feed — every id already "seen".
     */
    @Test
    fun `a new paging generation does not suppress previously seen ids`() = runTest {
        val pages = listOf(listOf(item("a"), item("b")))

        val firstGeneration = load(source(pages), null)
        val secondGeneration = load(source(pages), null)

        assertThat(firstGeneration.data.map { it.id }).containsExactly("a", "b").inOrder()
        assertThat(secondGeneration.data.map { it.id }).containsExactly("a", "b").inOrder()
    }

    /** Nothing to de-duplicate is the ordinary case and must be untouched. */
    @Test
    fun `a page without duplicates passes through unchanged`() = runTest {
        val source = source(listOf(listOf(item("a"), item("b"), item("c"))))

        val page = load(source, null)

        assertThat(page.data.map { it.id }).containsExactly("a", "b", "c").inOrder()
    }
}
