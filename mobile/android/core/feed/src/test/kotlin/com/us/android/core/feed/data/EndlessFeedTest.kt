package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import kotlinx.coroutines.test.runTest
import org.junit.Test

/**
 * The full-screen feed never ends (2026-10-18): past the primary timeline's
 * last page it carries on with the fallback's, and only the fallback's end
 * is the feed's end. Server cursors pass through verbatim on both sides.
 */
class EndlessFeedTest {

    private fun item(id: String) = FeedItem(
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

    private val asked = mutableListOf<String>()
    private fun loader(name: String, pages: Map<String?, FeedPage>): suspend (String?) -> FeedPage = { cursor ->
        asked += "$name:$cursor"
        pages.getValue(cursor)
    }

    @Test
    fun `a primary page with a cursor passes through untouched`() = runTest {
        val page = endlessPage(
            cursor = null,
            primary = loader("p", mapOf(null to FeedPage(listOf(item("1")), nextCursor = "2026-10-18T10:00:00Z"))),
            fallback = loader("f", emptyMap()),
        )
        assertThat(page.nextCursor).isEqualTo("2026-10-18T10:00:00Z")
        assertThat(asked).containsExactly("p:null")
    }

    @Test
    fun `the primary's last page hands on to the fallback, which pages with its own cursors`() = runTest {
        val primary = loader("p", mapOf("c1" to FeedPage(listOf(item("9")), nextCursor = null)))
        val fallback = loader(
            "f",
            mapOf(
                null to FeedPage(listOf(item("f1")), nextCursor = "fc2"),
                "fc2" to FeedPage(listOf(item("f2")), nextCursor = null),
            ),
        )

        val last = endlessPage("c1", primary, fallback)
        assertThat(last.nextCursor).isEqualTo(FALLBACK_CURSOR_PREFIX)

        val first = endlessPage(last.nextCursor, primary, fallback)
        assertThat(first.items.map { it.id }).containsExactly("f1")
        assertThat(first.nextCursor).isEqualTo(FALLBACK_CURSOR_PREFIX + "fc2")

        val end = endlessPage(first.nextCursor, primary, fallback)
        assertThat(end.items.map { it.id }).containsExactly("f2")
        assertThat(end.nextCursor).isNull()
        assertThat(asked).containsExactly("p:c1", "f:null", "f:fc2").inOrder()
    }

    @Test
    fun `an empty primary goes straight to the fallback's first page`() = runTest {
        val page = endlessPage(
            cursor = null,
            primary = loader("p", mapOf(null to FeedPage(emptyList(), nextCursor = null))),
            fallback = loader("f", mapOf(null to FeedPage(listOf(item("f1")), nextCursor = "fc2"))),
        )
        assertThat(page.items.map { it.id }).containsExactly("f1")
        assertThat(page.nextCursor).isEqualTo(FALLBACK_CURSOR_PREFIX + "fc2")
    }

    @Test
    fun `a handled error is an error, never an early switch`() = runTest {
        val page = endlessPage(
            cursor = null,
            primary = loader("p", mapOf(null to FeedPage(emptyList(), nextCursor = null, errorCode = "RATE_LIMITED"))),
            fallback = loader("f", emptyMap()),
        )
        assertThat(page.errorCode).isEqualTo("RATE_LIMITED")
        assertThat(asked).containsExactly("p:null")
    }
}
