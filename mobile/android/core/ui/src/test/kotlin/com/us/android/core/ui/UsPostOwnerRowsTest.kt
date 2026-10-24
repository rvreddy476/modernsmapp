package com.us.android.core.ui

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * A FEED post's owner and viewer tools (2026-10-24, the web's PostControls):
 * Edit post and Pin / Unpin on the viewer's own post, Mute on anyone else's —
 * each only where the host offers it, and never on a video's menu.
 */
class UsPostOwnerRowsTest {

    private fun post(own: Boolean, canEdit: Boolean = false, pinned: Boolean? = null, canMute: Boolean = false) =
        UsPostMoreState(
            postId = "p1",
            username = "ana",
            isOwnPost = own,
            isBookmarked = false,
            followRow = UsPostMoreFollowRow.FOLLOW,
            link = postShareLink("p1"),
            canEditPost = canEdit,
            pinned = pinned,
            canMute = canMute,
        )

    @Test
    fun `the owner's post offers Edit post and Pin to profile where the host has them`() {
        val state = post(own = true, canEdit = true, pinned = false)

        assertThat(state.rows()).containsAtLeast(UsPostMoreRow.EDIT, UsPostMoreRow.PIN, UsPostMoreRow.DELETE)
        assertThat(state.rows()).doesNotContain(UsPostMoreRow.UNPIN)
        assertThat(state.labelOf(UsPostMoreRow.EDIT)).isEqualTo("Edit post")
        assertThat(state.labelOf(UsPostMoreRow.PIN)).isEqualTo("Pin to profile")
    }

    @Test
    fun `a pinned post offers Unpin instead`() {
        val rows = post(own = true, pinned = true).rows()

        assertThat(rows).contains(UsPostMoreRow.UNPIN)
        assertThat(rows).doesNotContain(UsPostMoreRow.PIN)
    }

    @Test
    fun `no host support, no rows`() {
        val rows = post(own = true).rows()

        assertThat(rows).containsNoneOf(UsPostMoreRow.EDIT, UsPostMoreRow.PIN, UsPostMoreRow.UNPIN)
    }

    @Test
    fun `someone else's post offers Mute with the handle, never Edit or Pin`() {
        val state = post(own = false, canEdit = true, pinned = false, canMute = true)

        assertThat(state.rows()).contains(UsPostMoreRow.MUTE)
        assertThat(state.rows()).containsNoneOf(UsPostMoreRow.EDIT, UsPostMoreRow.PIN, UsPostMoreRow.UNPIN)
        assertThat(state.labelOf(UsPostMoreRow.MUTE)).isEqualTo("Mute @ana")
    }

    @Test
    fun `the viewer's own post never offers Mute`() {
        assertThat(post(own = true, canMute = true).rows()).doesNotContain(UsPostMoreRow.MUTE)
    }

    @Test
    fun `the rows stay in alphabetical order with the new ones in`() {
        val own = post(own = true, canEdit = true, pinned = true)
        val other = post(own = false, canMute = true)

        assertThat(own.rows().map { own.labelOf(it) }).isInOrder(String.CASE_INSENSITIVE_ORDER)
        assertThat(other.rows().map { other.labelOf(it) }).isInOrder(String.CASE_INSENSITIVE_ORDER)
    }
}
