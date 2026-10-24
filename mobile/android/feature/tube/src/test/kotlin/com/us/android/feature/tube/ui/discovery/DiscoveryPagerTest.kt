package com.us.android.feature.tube.ui.discovery

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.DiscoveryPage
import com.us.android.core.feed.data.TrendingPeriod
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.runTest
import org.junit.Test

/** Trending and topic pages: their paging, and the words an empty page says. */
@OptIn(ExperimentalCoroutinesApi::class)
class DiscoveryPagerTest {

    private fun item(id: String) = FeedItem(
        id = id,
        authorId = "a",
        author = FeedAuthor(id = "a", displayName = "Ada"),
        text = "",
        visibility = "public",
        feedContentType = "long_video",
        postType = "video",
        createdAt = "",
        isPinned = false,
        media = emptyList(),
        counts = FeedCounts(0, 0, 0, 0),
        viewer = FeedViewerState(false, false, false),
        isRepostable = false,
    )

    private class Script(vararg pages: Pair<String?, AppResult<DiscoveryPage>>) {
        val map = pages.toMap()
        val asked = mutableListOf<String?>()
        suspend fun load(cursor: String?): AppResult<DiscoveryPage> {
            asked += cursor
            return map[cursor] ?: AppResult.Failure(AppError.NoNetwork())
        }
    }

    private fun page(vararg ids: String, next: String? = null): AppResult<DiscoveryPage> =
        AppResult.Success(DiscoveryPage(ids.map { item(it) }, next))

    @Test
    fun `the first page, then more with the cursor, each row once`() = runTest(UnconfinedTestDispatcher()) {
        val script = Script(null to page("a", "b", "a", next = "c1"), "c1" to page("b", "c"))
        val pager = DiscoveryPager(backgroundScope, "trending", script::load)

        pager.start()
        assertThat(pager.state.value.phase).isEqualTo(DiscoveryPhase.Ready)
        assertThat(pager.state.value.items.map { it.id }).containsExactly("a", "b").inOrder()
        assertThat(pager.state.value.wantsMore).isTrue()

        pager.loadMore()
        assertThat(pager.state.value.items.map { it.id }).containsExactly("a", "b", "c").inOrder()
        assertThat(pager.state.value.canLoadMore).isFalse()
        pager.loadMore()
        assertThat(script.asked).containsExactly(null, "c1").inOrder()
    }

    @Test
    fun `a failed first page says why, a failed later page keeps the rows`() = runTest(UnconfinedTestDispatcher()) {
        val failing = DiscoveryPager(backgroundScope, "trending") { AppResult.Failure(AppError.NoNetwork()) }
        failing.start()
        assertThat(failing.state.value.phase)
            .isEqualTo(DiscoveryPhase.Failed("You're offline. Check your connection and try again."))

        val script = Script(null to page("a", next = "c1"))
        val pager = DiscoveryPager(backgroundScope, "this topic", script::load)
        pager.start()
        pager.loadMore()
        assertThat(pager.state.value.items.map { it.id }).containsExactly("a")
        assertThat(pager.state.value.moreFailed).isTrue()
        assertThat(pager.state.value.wantsMore).isFalse()
        assertThat(discoveryFailure(AppError.Unknown(null, 500), "this topic").message).isEqualTo("We couldn't load this topic.")
    }

    @Test
    fun `refresh replaces the rows from the top and keeps them on a failure`() = runTest(UnconfinedTestDispatcher()) {
        var fresh = false
        val pager = DiscoveryPager(backgroundScope, "trending") { cursor ->
            when {
                cursor != null -> AppResult.Failure(AppError.NoNetwork())
                fresh -> page("n1")
                else -> page("o1", next = "c")
            }
        }
        pager.start()
        fresh = true
        pager.refresh()
        assertThat(pager.state.value.items.map { it.id }).containsExactly("n1")
        assertThat(pager.state.value.refreshing).isFalse()
        assertThat(pager.state.value.canLoadMore).isFalse()
    }

    @Test
    fun `the page's words follow the web`() {
        assertThat(trendingSubtitle(TrendingPeriod.WEEK)).isEqualTo("What people are watching this week")
        assertThat(trendingEmptyTitle(TrendingPeriod.TODAY, narrowedOut = true)).isEqualTo("Nothing new today")
        assertThat(trendingEmptyTitle(TrendingPeriod.MONTH, narrowedOut = true)).isEqualTo("Nothing new this month")
        assertThat(trendingEmptyTitle(TrendingPeriod.WEEK, narrowedOut = false)).isEqualTo("Nothing is trending yet")
    }
}
