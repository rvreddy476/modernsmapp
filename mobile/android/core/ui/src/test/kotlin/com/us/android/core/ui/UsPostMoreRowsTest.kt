package com.us.android.core.ui

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Which rows the post "more" sheet shows, per case — the founder's
 * Instagram capture (2026-09-04) turned into rules the sheet cannot drift
 * from:
 *
 *  - another person's post: all three groups, Report last;
 *  - the viewer's own post: Save, Copy link, Share and nothing else;
 *  - Unfollow only when the viewer follows, Follow only when they are
 *    known not to, neither while the edge is unknown;
 *  - "Why you're seeing this post" only when the server sent a sentence.
 *
 * And the report vocabulary: every label the reader can pick maps to the
 * token trust-safety stores, in the sheet's order.
 */
class UsPostMoreRowsTest {

    private fun state(
        own: Boolean = false,
        bookmarked: Boolean = false,
        follow: UsPostMoreFollowRow = UsPostMoreFollowRow.FOLLOW,
        reason: String = "",
    ) = UsPostMoreState(
        postId = "p1",
        username = "call_userb",
        isOwnPost = own,
        isBookmarked = bookmarked,
        followRow = follow,
        reasonText = reason,
        link = postShareLink("p1"),
    )

    @Test
    fun `another person's post shows all three groups with report last`() {
        val groups = state(follow = UsPostMoreFollowRow.UNFOLLOW, reason = "From someone you follow").rowGroups()

        assertThat(groups).containsExactly(
            listOf(UsPostMoreRow.SAVE, UsPostMoreRow.COPY_LINK, UsPostMoreRow.SHARE),
            listOf(UsPostMoreRow.WHY, UsPostMoreRow.INTERESTED, UsPostMoreRow.NOT_INTERESTED),
            listOf(UsPostMoreRow.UNFOLLOW, UsPostMoreRow.BLOCK, UsPostMoreRow.REPORT),
        ).inOrder()
    }

    @Test
    fun `the viewer's own post shows save, copy link and share only`() {
        val groups = state(own = true, reason = "Trending now", follow = UsPostMoreFollowRow.UNFOLLOW).rowGroups()

        assertThat(groups).containsExactly(listOf(UsPostMoreRow.SAVE, UsPostMoreRow.COPY_LINK, UsPostMoreRow.SHARE))
        assertThat(groups.flatten()).containsNoneOf(
            UsPostMoreRow.INTERESTED,
            UsPostMoreRow.NOT_INTERESTED,
            UsPostMoreRow.FOLLOW,
            UsPostMoreRow.UNFOLLOW,
            UsPostMoreRow.BLOCK,
            UsPostMoreRow.REPORT,
            UsPostMoreRow.WHY,
        )
    }

    @Test
    fun `a bookmarked post offers unsave in save's place`() {
        assertThat(state(bookmarked = true).rowGroups().first())
            .containsExactly(UsPostMoreRow.UNSAVE, UsPostMoreRow.COPY_LINK, UsPostMoreRow.SHARE)
            .inOrder()
        assertThat(state(bookmarked = false).rowGroups().first()).contains(UsPostMoreRow.SAVE)
    }

    @Test
    fun `unfollow when following, follow when not, neither while unknown`() {
        assertThat(state(follow = UsPostMoreFollowRow.UNFOLLOW).rowGroups().last())
            .containsExactly(UsPostMoreRow.UNFOLLOW, UsPostMoreRow.BLOCK, UsPostMoreRow.REPORT).inOrder()
        assertThat(state(follow = UsPostMoreFollowRow.FOLLOW).rowGroups().last())
            .containsExactly(UsPostMoreRow.FOLLOW, UsPostMoreRow.BLOCK, UsPostMoreRow.REPORT).inOrder()
        assertThat(state(follow = UsPostMoreFollowRow.HIDDEN).rowGroups().last())
            .containsExactly(UsPostMoreRow.BLOCK, UsPostMoreRow.REPORT).inOrder()
    }

    @Test
    fun `the why row appears only with a reason sentence`() {
        assertThat(state(reason = "Popular in Comedy").rowGroups()[1].first()).isEqualTo(UsPostMoreRow.WHY)
        assertThat(state(reason = "").rowGroups()[1])
            .containsExactly(UsPostMoreRow.INTERESTED, UsPostMoreRow.NOT_INTERESTED).inOrder()
        assertThat(state(reason = "   ").rowGroups()[1]).doesNotContain(UsPostMoreRow.WHY)
    }

    @Test
    fun `report reasons map to trust-safety's tokens in the sheet's order`() {
        assertThat(UsReportReason.entries.map { it.label to it.wire }).containsExactly(
            "Spam" to "spam",
            "Harassment" to "harassment",
            "Nudity or sexual content" to "nudity",
            "Violence" to "violence",
            "Hate speech" to "hate",
            "False information" to "false_info",
            "Scam or fraud" to "scam_fraud",
            "Impersonation" to "impersonation",
            "Self-harm" to "self_harm",
            "Intellectual property" to "intellectual_property",
            "Other" to "other",
        ).inOrder()
        assertThat(UsReportReason.entries.filter { it.asksForDetails }).containsExactly(UsReportReason.OTHER)
    }

    @Test
    fun `copy link puts the post's canonical address on the clipboard`() {
        assertThat(postShareLink("abc-123")).isEqualTo("https://momentum.app/p/abc-123")
    }
}
