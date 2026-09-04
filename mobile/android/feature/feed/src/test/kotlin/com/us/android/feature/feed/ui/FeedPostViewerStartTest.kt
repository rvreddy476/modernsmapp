package com.us.android.feature.feed.ui

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Which page the in-place viewer opens on.
 *
 * The viewer pages over the LIVE paging snapshot, and between the tap and
 * its first frame a refresh can move every row. Opening on the wrong post
 * is the one defect that would make "tap a post, see that post" untrue, so
 * the rule is pinned: the tapped id wins, the tapped index is the fallback,
 * and neither can point past the end.
 */
class FeedPostViewerStartTest {

    private val ids = listOf("a", "b", "c", "d")

    @Test
    fun `the viewer starts at the tapped post`() {
        assertThat(viewerStartPage(ids, tappedId = "c", tappedIndex = 2)).isEqualTo(2)
    }

    /** A refresh inserted a row above: the id still finds the post. */
    @Test
    fun `the id wins when the list has shifted under the tap`() {
        assertThat(viewerStartPage(listOf("new") + ids, tappedId = "c", tappedIndex = 2)).isEqualTo(3)
    }

    /** The tapped row is gone: fall back to where it was. */
    @Test
    fun `a removed post falls back to the tapped index`() {
        assertThat(viewerStartPage(listOf("a", "b", "d"), tappedId = "c", tappedIndex = 2)).isEqualTo(2)
    }

    @Test
    fun `the fallback never points past the end`() {
        assertThat(viewerStartPage(listOf("a"), tappedId = "z", tappedIndex = 7)).isEqualTo(0)
        assertThat(viewerStartPage(emptyList(), tappedId = "z", tappedIndex = 7)).isEqualTo(0)
    }
}
