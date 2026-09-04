package com.us.android.feature.tube.ui.watch

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import org.junit.Test

/** The "Up next" rule and the auto-advance it implies. */
class UpNextTest {

    private val items = listOf("a", "b", "c", "d").map(::item)

    @Test
    fun `up next is the rows after the current one in order`() {
        assertThat(upNext(items, "b").map { it.id }).containsExactly("c", "d").inOrder()
        assertThat(upNext(items, "a").map { it.id }).containsExactly("b", "c", "d").inOrder()
    }

    @Test
    fun `the last row has nothing up next and nothing to advance to`() {
        assertThat(upNext(items, "d")).isEmpty()
        assertThat(nextAfter(items, "d")).isNull()
    }

    @Test
    fun `a video outside the list offers the whole list minus itself`() {
        assertThat(upNext(items, "zz").map { it.id }).containsExactly("a", "b", "c", "d").inOrder()
        assertThat(nextAfter(items, "zz")?.id).isEqualTo("a")
    }

    @Test
    fun `the list is capped`() {
        val long = (1..30).map { item("v$it") }
        assertThat(upNext(long, "v1")).hasSize(UP_NEXT_LIMIT)
        assertThat(upNext(long, "v1", limit = 3).map { it.id }).containsExactly("v2", "v3", "v4").inOrder()
    }

    @Test
    fun `advance is the first of up next`() {
        assertThat(nextAfter(items, "a")?.id).isEqualTo("b")
        assertThat(nextAfter(emptyList(), "a")).isNull()
    }

    private fun item(id: String) = FeedItem(
        id = id,
        authorId = "u",
        author = FeedAuthor(id = "u", displayName = "Ada"),
        text = "",
        visibility = "public",
        feedContentType = "long_video",
        postType = "video",
        createdAt = "",
        isPinned = false,
        media = emptyList(),
        counts = FeedCounts(0, 0, 0, 0),
        viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
        isRepostable = false,
    )
}
