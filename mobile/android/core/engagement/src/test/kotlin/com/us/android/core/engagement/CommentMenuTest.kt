package com.us.android.core.engagement

import com.google.common.truth.Truth.assertThat
import com.us.android.core.engagement.data.CommentMenuAction
import com.us.android.core.engagement.data.CommentMenuAction.DELETE
import com.us.android.core.engagement.data.CommentMenuAction.HEART
import com.us.android.core.engagement.data.CommentMenuAction.PIN
import com.us.android.core.engagement.data.CommentMenuAction.REPORT
import com.us.android.core.engagement.data.CommentMenuAction.UNHEART
import com.us.android.core.engagement.data.CommentMenuAction.UNPIN
import com.us.android.core.engagement.data.CommentRow
import com.us.android.core.engagement.data.activeMentionQuery
import com.us.android.core.engagement.data.commentMenuActions
import com.us.android.core.engagement.data.completeMention
import org.junit.Test

/**
 * The ⋮ permission matrix (self / creator / other), checked against what
 * post-service allows: delete is author-only for everyone, the creator
 * included; heart and pin are the post author's; pin is top-level only.
 * And the menu's order: alphabetical by label, the rule for every menu.
 */
class CommentMenuTest {

    private fun row(author: String, reply: Boolean = false, pinned: Boolean = false, hearted: Boolean = false) =
        CommentRow(
            id = "c",
            authorId = author,
            body = "b",
            createdAt = "",
            parentId = if (reply) "parent" else null,
            pinned = pinned,
            heartedByAuthor = hearted,
        )

    private fun menu(row: CommentRow, viewer: String?, creator: String?): List<CommentMenuAction> =
        commentMenuActions(row, viewer, creator)

    @Test
    fun `someone else's comment, viewed by someone who is not the creator - Report only`() {
        assertThat(menu(row("bob"), viewer = "me", creator = "alice")).containsExactly(REPORT)
    }

    @Test
    fun `your own comment on someone else's post - Delete, never Report`() {
        assertThat(menu(row("me"), viewer = "me", creator = "alice")).containsExactly(DELETE)
    }

    @Test
    fun `the creator on someone else's comment - Heart, Pin, Report, but no Delete`() {
        assertThat(menu(row("bob"), viewer = "me", creator = "me"))
            .containsExactly(HEART, PIN, REPORT).inOrder()
    }

    @Test
    fun `the creator on their own comment - Delete, Heart, Pin`() {
        assertThat(menu(row("me"), viewer = "me", creator = "me"))
            .containsExactly(DELETE, HEART, PIN).inOrder()
    }

    @Test
    fun `the creator on a reply - no Pin, the server refuses pinning replies`() {
        assertThat(menu(row("bob", reply = true), viewer = "me", creator = "me"))
            .containsExactly(HEART, REPORT).inOrder()
    }

    @Test
    fun `a pinned and hearted comment offers the undo rows`() {
        assertThat(menu(row("bob", pinned = true, hearted = true), viewer = "me", creator = "me"))
            .containsExactly(UNHEART, REPORT, UNPIN).inOrder()
    }

    @Test
    fun `an unknown viewer is neither author nor creator`() {
        assertThat(menu(row(""), viewer = null, creator = "")).containsExactly(REPORT)
        assertThat(menu(row("me"), viewer = "", creator = "")).containsExactly(REPORT)
    }

    @Test
    fun `a comment still being sent has no menu`() {
        assertThat(menu(row("me").copy(pending = true), viewer = "me", creator = "me")).isEmpty()
    }

    @Test
    fun `labels are the words the viewer reads`() {
        assertThat(CommentMenuAction.entries.map { it.label })
            .containsExactly("Delete", "Heart", "Remove heart", "Pin", "Unpin", "Report")
    }

    // ── @mention parsing ────────────────────────────────────────────────

    @Test
    fun `only a trailing at-handle after a space or the start is a mention being typed`() {
        assertThat(activeMentionQuery("@ra")).isEqualTo("ra")
        assertThat(activeMentionQuery("hi @ra.gh_u")).isEqualTo("ra.gh_u")
        assertThat(activeMentionQuery("hi @")).isNull()
        assertThat(activeMentionQuery("mail me@example")).isNull()
        assertThat(activeMentionQuery("@raghu done")).isNull()
    }

    @Test
    fun `completing a mention replaces the partial handle and adds a space`() {
        assertThat(completeMention("hi @ra", "raghu")).isEqualTo("hi @raghu ")
        assertThat(completeMention("@r", "raghu")).isEqualTo("@raghu ")
        assertThat(completeMention("no mention", "raghu")).isEqualTo("no mention")
    }
}
