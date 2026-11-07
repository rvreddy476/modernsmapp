package com.us.android.core.feed.data

import androidx.paging.PagingData
import androidx.paging.testing.asSnapshot
import com.google.common.truth.Truth.assertThat
import com.us.android.core.engagement.data.HiddenSet
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.test.runTest
import org.junit.Test

/**
 * The one view of a row after this session's hides, deletes, blocks and
 * edits (audit 2026-10-30, A1). Paged rows and a pinned row both go through
 * [RowOverlays.view], so a post deleted from its own More sheet cannot keep
 * showing at the top while the same post as a paged row is gone.
 */
class RowOverlaysTest {

    private fun item(id: String, author: String = "a", text: String = "old words") = FeedItem(
        id = id,
        authorId = author,
        author = FeedAuthor(id = author, displayName = author),
        text = text,
        visibility = "public",
        feedContentType = "post",
        postType = "text",
        createdAt = "2026-10-30T00:00:00Z",
        isPinned = false,
        media = emptyList(),
        counts = FeedCounts(likes = 0, comments = 0, reposts = 0, views = 0),
        viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
        isRepostable = true,
    )

    @Test
    fun `a deleted or hidden post is not shown`() {
        val laid = RowOverlays(hidden = HiddenSet(postIds = setOf("p1")))

        assertThat(laid.view(item("p1"))).isNull()
        assertThat(laid.view(item("p2"))).isEqualTo(item("p2"))
    }

    @Test
    fun `a blocked author's post is not shown`() {
        val laid = RowOverlays(hidden = HiddenSet(authorIds = setOf("troll")))

        assertThat(laid.view(item("p1", author = "troll"))).isNull()
    }

    @Test
    fun `an edited post shows the saved words`() {
        val laid = RowOverlays(edits = mapOf("p1" to PostEditPatch(text = "new words")))

        assertThat(laid.view(item("p1"))?.text).isEqualTo("new words")
        assertThat(laid.view(item("p2"))?.text).isEqualTo("old words")
    }

    @Test
    fun `a page goes through the same view as a pinned row`() = runTest {
        val laid = RowOverlays(
            hidden = HiddenSet(postIds = setOf("gone"), authorIds = setOf("troll")),
            edits = mapOf("kept" to PostEditPatch(text = "new words")),
        )
        val rows = listOf(item("gone"), item("kept"), item("x", author = "troll"), item("plain"))

        val shown = flowOf(PagingData.from(rows).through(laid)).asSnapshot()

        assertThat(shown).containsExactlyElementsIn(rows.mapNotNull(laid::view)).inOrder()
        assertThat(shown.map { it.id }).containsExactly("kept", "plain").inOrder()
        assertThat(shown.first().text).isEqualTo("new words")
    }

    @Test
    fun `nothing to lay over leaves the page as it was`() = runTest {
        val rows = listOf(item("p1"), item("p2"))

        assertThat(flowOf(PagingData.from(rows).through(RowOverlays())).asSnapshot()).isEqualTo(rows)
    }
}
