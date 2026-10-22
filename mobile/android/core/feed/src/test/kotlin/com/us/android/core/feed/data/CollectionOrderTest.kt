package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import org.junit.Test

/**
 * A collection's reorder, planned on the client (2026-10-22): the list the
 * viewer sees after a move, and the position the server must be sent when
 * rows the viewer may not see sit between the visible ones. The server
 * (`MovePlaylistItem`) removes the row and re-inserts it at the clamped
 * index, over every row; these pin that the client plans the same move.
 */
class CollectionOrderTest {

    private fun video(id: String) = FeedItem(
        id = id,
        authorId = "a1",
        author = FeedAuthor(id = "a1", displayName = "Clee"),
        text = "",
        visibility = "public",
        feedContentType = "long_video",
        postType = "video",
        createdAt = "",
        isPinned = false,
        media = emptyList(),
        counts = FeedCounts(likes = 0, comments = 0, reposts = 0, views = 0),
        viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
        isRepostable = false,
    )

    private val a = video("a")
    private val b = video("b")
    private val c = video("c")

    /** What the server does with a move, for checking the plan against. */
    private fun serverMove(order: List<String>, postId: String, position: Int): List<String> =
        moveIndex(order, order.indexOf(postId), position)

    @Test
    fun `moveIndex moves one element and clamps both ends`() {
        assertThat(moveIndex(listOf(1, 2, 3), 0, 2)).containsExactly(2, 3, 1).inOrder()
        assertThat(moveIndex(listOf(1, 2, 3), 2, 0)).containsExactly(3, 1, 2).inOrder()
        assertThat(moveIndex(listOf(1, 2, 3), 1, 99)).containsExactly(1, 3, 2).inOrder()
        assertThat(moveIndex(listOf(1, 2, 3), 1, -4)).containsExactly(2, 1, 3).inOrder()
        assertThat(moveIndex(emptyList<Int>(), 0, 1)).isEmpty()
    }

    @Test
    fun `with nothing hidden the position is the visible target`() {
        val plan = planCollectionMove(listOf("a", "b", "c"), listOf(a, b, c), "c", 0)!!

        assertThat(plan.position).isEqualTo(0)
        assertThat(plan.videos.map { it.id }).containsExactly("c", "a", "b").inOrder()
        assertThat(plan.order).containsExactly("c", "a", "b").inOrder()
    }

    /** a, hidden, b, c on the server; a, b, c on screen. Move down puts a just after b, where the viewer sees it go. */
    @Test
    fun `move down past a hidden row lands just after the visible neighbour`() {
        val order = listOf("a", "h", "b", "c")

        val plan = planCollectionMove(order, listOf(a, b, c), "a", 1)!!

        assertThat(plan.videos.map { it.id }).containsExactly("b", "a", "c").inOrder()
        assertThat(serverMove(order, "a", plan.position)).isEqualTo(plan.order)
        assertThat(plan.order.filter { it != "h" }).containsExactly("b", "a", "c").inOrder()
    }

    @Test
    fun `move up past a hidden row lands just before the visible neighbour`() {
        val order = listOf("a", "h", "b", "c")

        val plan = planCollectionMove(order, listOf(a, b, c), "b", 0)!!

        assertThat(plan.videos.map { it.id }).containsExactly("b", "a", "c").inOrder()
        assertThat(serverMove(order, "b", plan.position)).isEqualTo(plan.order)
        assertThat(plan.order.filter { it != "h" }).containsExactly("b", "a", "c").inOrder()
    }

    @Test
    fun `to the bottom goes past every row, hidden ones included`() {
        val order = listOf("a", "b", "c", "h")

        val plan = planCollectionMove(order, listOf(a, b, c), "a", 2)!!

        assertThat(plan.videos.map { it.id }).containsExactly("b", "c", "a").inOrder()
        assertThat(serverMove(order, "a", plan.position)).isEqualTo(plan.order)
    }

    @Test
    fun `a move that changes nothing, or of an unknown video, is no plan`() {
        assertThat(planCollectionMove(listOf("a", "b"), listOf(a, b), "a", 0)).isNull()
        assertThat(planCollectionMove(listOf("a", "b"), listOf(a, b), "zz", 1)).isNull()
        assertThat(planCollectionMove(emptyList(), emptyList(), "a", 0)).isNull()
    }

    /** An order read before a video was added (it does not know it) falls back to the visible list. */
    @Test
    fun `a stale order falls back to the visible list`() {
        val plan = planCollectionMove(listOf("a", "b"), listOf(a, b, c), "c", 0)!!

        assertThat(plan.position).isEqualTo(0)
        assertThat(plan.order).containsExactly("c", "a", "b").inOrder()
    }

    @Test
    fun `the server's order is laid over the rows on screen, without re-reading them`() {
        val shown = reconcileCollectionOrder(listOf("c", "h", "a", "gone"), listOf(a, b, c))

        // b is no longer listed by the server; h and gone were never drawn.
        assertThat(shown.map { it.id }).containsExactly("c", "a").inOrder()
        assertThat(shown.first()).isSameInstanceAs(c)
    }
}
