package com.us.android.feature.feed.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import org.junit.Test

/**
 * Resolving a feed row to the item it shows.
 *
 * The geometry rule beside this ([mostVisibleVideo]) has always been tested;
 * this half never was, and that is exactly where the bug lived. Autoplay used
 * to look a row up by its LIST index against the PAGING list. Those agree only
 * while nothing is pinned — and a just-published post is pinned as row 0, so
 * every paged row below it was resolved to the item one place earlier. The
 * single feed player was pointed at the wrong video, or refused to play a
 * video row because the item it happened to find had nothing to play.
 */
class FeedAutoplayResolutionTest {

    @Test
    fun `a paged row resolves to its own item`() {
        assertThat(feedItemForKey("p2", paged, head = null)?.id).isEqualTo("p2")
    }

    @Test
    fun `the pinned row resolves to the head, which is in no paged list`() {
        assertThat(feedItemForKey("${HEAD_KEY_PREFIX}h1", paged, head = item("h1"))?.id)
            .isEqualTo("h1")
    }

    /**
     * The regression itself, stated as the thing that was wrong: with a post
     * pinned, the row showing `p2` sits at list index 2 while `p2` is paging
     * index 1. An index lookup returned `p3`; a key lookup returns `p2`.
     */
    @Test
    fun `a pinned head does not shift a paged row onto its neighbour`() {
        val head = item("h1")
        val rowShowingP2 = "p2"
        assertThat(feedItemForKey(rowShowingP2, paged, head)?.id).isEqualTo("p2")
        assertThat(paged[2].id).isEqualTo("p3") // what the old index lookup found
    }

    @Test
    fun `the footer row is not a post`() {
        assertThat(feedItemForKey(APPEND_KEY, paged, head = null)).isNull()
    }

    @Test
    fun `an unloaded paging row keyed by Int is not a post`() {
        assertThat(feedItemForKey(7, paged, head = null)).isNull()
    }

    @Test
    fun `a key naming nothing on screen resolves to nothing`() {
        assertThat(feedItemForKey("gone", paged, head = item("h1"))).isNull()
    }

    private val paged = listOf(item("p1"), item("p2"), item("p3"))

    private fun item(id: String) = FeedItem(
        id = id,
        authorId = "a",
        author = FeedAuthor(id = "a", displayName = "A"),
        text = "t",
        visibility = "public",
        feedContentType = "post",
        postType = "text",
        createdAt = "2026-09-07T00:00:00Z",
        isPinned = false,
        media = emptyList(),
        counts = FeedCounts(likes = 0, comments = 0, reposts = 0, views = 0),
        viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
        isRepostable = true,
    )
}
