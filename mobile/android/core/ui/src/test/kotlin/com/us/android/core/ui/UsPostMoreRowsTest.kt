package com.us.android.core.ui

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Which rows the post "more" sheet shows, per case — the founder's
 * Instagram capture (2026-09-04) turned into rules the sheet cannot drift
 * from:
 *
 *  - another person's post: all three groups, Report last, and "Don't
 *    recommend @user" right after "Not interested" (founder, 2026-09-04,
 *    from YouTube's "Don't recommend channel");
 *  - the viewer's own post: Save, Copy link, Share, then Delete post alone
 *    and last — never a row that acts on "the author";
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
        // Followed author, reached through the follow: no Interested (it is
        // not a suggestion) and no Don't recommend (Unfollow is that).
        val groups = state(follow = UsPostMoreFollowRow.UNFOLLOW, reason = "From someone you follow")
            .copy(suggested = false)
            .rowGroups()

        assertThat(groups).containsExactly(
            listOf(UsPostMoreRow.SAVE, UsPostMoreRow.COPY_LINK, UsPostMoreRow.SHARE),
            listOf(UsPostMoreRow.WHY, UsPostMoreRow.NOT_INTERESTED),
            listOf(UsPostMoreRow.UNFOLLOW, UsPostMoreRow.BLOCK, UsPostMoreRow.REPORT),
        ).inOrder()
    }

    /**
     * The founder looked for Block and Report on his OWN reel (2026-09-04)
     * and found them "missing": that is the rule, not a bug. Pinned both
     * ways so neither side of it can drift.
     */
    @Test
    fun `block and report are offered on another person's post and never on the viewer's own`() {
        for (follow in UsPostMoreFollowRow.entries) {
            val other = state(own = false, follow = follow).rowGroups().flatten()
            assertThat(other).containsAtLeast(UsPostMoreRow.BLOCK, UsPostMoreRow.REPORT).inOrder()
            assertThat(other.last()).isEqualTo(UsPostMoreRow.REPORT)

            val own = state(own = true, follow = follow).rowGroups().flatten()
            assertThat(own).containsNoneOf(UsPostMoreRow.BLOCK, UsPostMoreRow.REPORT)
        }
        // A reel changes nothing about it: the reel group is about the frame, not the author.
        assertThat(state(own = false).copy(reel = reel()).rowGroups().flatten())
            .containsAtLeast(UsPostMoreRow.BLOCK, UsPostMoreRow.REPORT).inOrder()
        assertThat(state(own = true).copy(reel = reel()).rowGroups().flatten())
            .containsNoneOf(UsPostMoreRow.BLOCK, UsPostMoreRow.REPORT)
    }

    /** YouTube's "Don't recommend channel": right after "Not interested", other people's posts only. */
    @Test
    fun `don't recommend follows not interested on another person's post and is never on the viewer's own`() {
        val second = state(own = false).rowGroups()[1]
        assertThat(second).containsExactly(
            UsPostMoreRow.INTERESTED,
            UsPostMoreRow.NOT_INTERESTED,
            UsPostMoreRow.DONT_RECOMMEND,
        ).inOrder()
        assertThat(second.indexOf(UsPostMoreRow.DONT_RECOMMEND))
            .isEqualTo(second.indexOf(UsPostMoreRow.NOT_INTERESTED) + 1)

        assertThat(state(own = true).rowGroups().flatten()).doesNotContain(UsPostMoreRow.DONT_RECOMMEND)
        assertThat(state(own = true).copy(reel = reel()).rowGroups().flatten())
            .doesNotContain(UsPostMoreRow.DONT_RECOMMEND)
        assertThat(UsPostMoreRow.DONT_RECOMMEND.label).isEqualTo("Don't recommend")
    }

    @Test
    fun `the viewer's own post shows save, copy link, share and then delete last`() {
        val groups = state(own = true, reason = "Trending now", follow = UsPostMoreFollowRow.UNFOLLOW).rowGroups()

        assertThat(groups).containsExactly(
            listOf(UsPostMoreRow.SAVE, UsPostMoreRow.COPY_LINK, UsPostMoreRow.SHARE),
            listOf(UsPostMoreRow.DELETE),
        ).inOrder()
        assertThat(groups.last().last()).isEqualTo(UsPostMoreRow.DELETE)
        assertThat(groups.flatten()).containsNoneOf(
            UsPostMoreRow.INTERESTED,
            UsPostMoreRow.NOT_INTERESTED,
            UsPostMoreRow.DONT_RECOMMEND,
            UsPostMoreRow.FOLLOW,
            UsPostMoreRow.UNFOLLOW,
            UsPostMoreRow.BLOCK,
            UsPostMoreRow.REPORT,
            UsPostMoreRow.WHY,
        )
    }

    @Test
    fun `delete is offered on the viewer's own post only`() {
        assertThat(state(own = false).rowGroups().flatten()).doesNotContain(UsPostMoreRow.DELETE)
        assertThat(state(own = true).rowGroups().flatten()).contains(UsPostMoreRow.DELETE)
        assertThat(UsPostMoreRow.DELETE.label).isEqualTo("Delete post")
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

    /**
     * Interested is a signal about a suggestion, so a post from an account
     * the viewer already follows does not offer it; and muting an account the
     * viewer follows is what Unfollow is for, so Don't recommend appears only
     * while the author is not followed (founder, 2026-09-04).
     */
    @Test
    fun `interested is for suggestions and don't recommend is for accounts not followed`() {
        val followed = state(follow = UsPostMoreFollowRow.UNFOLLOW).copy(suggested = false).rowGroups()[1]
        assertThat(followed).containsExactly(UsPostMoreRow.NOT_INTERESTED)

        val suggestedStranger = state(follow = UsPostMoreFollowRow.FOLLOW).copy(suggested = true).rowGroups()[1]
        assertThat(suggestedStranger)
            .containsExactly(UsPostMoreRow.INTERESTED, UsPostMoreRow.NOT_INTERESTED, UsPostMoreRow.DONT_RECOMMEND)
            .inOrder()

        val unknownEdge = state(follow = UsPostMoreFollowRow.HIDDEN).copy(suggested = true).rowGroups()[1]
        assertThat(unknownEdge).contains(UsPostMoreRow.DONT_RECOMMEND)
    }

    @Test
    fun `the why row appears only with a reason sentence`() {
        assertThat(state(reason = "Popular in Comedy").rowGroups()[1].first()).isEqualTo(UsPostMoreRow.WHY)
        assertThat(state(reason = "").rowGroups()[1])
            .containsExactly(UsPostMoreRow.INTERESTED, UsPostMoreRow.NOT_INTERESTED, UsPostMoreRow.DONT_RECOMMEND)
            .inOrder()
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

    // ── The reel's group (YouTube Shorts, 2026-09-04) ───────────────────

    private fun reel(
        description: String = "sunday at the lake",
        fullMode: Boolean = false,
        qualities: List<UsReelQuality> = reelQualityOptions(listOf(360, 720)),
        selected: UsReelQuality = UsReelQuality.Auto,
    ) = UsReelMoreState(description = description, fullMode = fullMode, qualities = qualities, selected = selected)

    @Test
    fun `a reel puts description, clear screen and quality above the post's groups`() {
        val groups = state(follow = UsPostMoreFollowRow.UNFOLLOW, reason = "Trending").copy(reel = reel()).rowGroups()

        assertThat(groups).containsExactly(
            listOf(UsPostMoreRow.DESCRIPTION, UsPostMoreRow.CLEAR_SCREEN, UsPostMoreRow.QUALITY),
            listOf(UsPostMoreRow.SAVE, UsPostMoreRow.COPY_LINK, UsPostMoreRow.SHARE),
            listOf(UsPostMoreRow.WHY, UsPostMoreRow.INTERESTED, UsPostMoreRow.NOT_INTERESTED),
            listOf(UsPostMoreRow.UNFOLLOW, UsPostMoreRow.BLOCK, UsPostMoreRow.REPORT),
        ).inOrder()
    }

    /** The feed card's sheet is exactly what it was: no reel, no reel group. */
    @Test
    fun `a post that is not a reel has no reel group`() {
        val rows = state().rowGroups().flatten()

        assertThat(rows).containsNoneOf(
            UsPostMoreRow.DESCRIPTION,
            UsPostMoreRow.CLEAR_SCREEN,
            UsPostMoreRow.SHOW_CONTROLS,
            UsPostMoreRow.QUALITY,
        )
        assertThat(state().rowGroups()).hasSize(3)
    }

    @Test
    fun `the viewer's own reel keeps the reel group and then delete last`() {
        val groups = state(own = true).copy(reel = reel()).rowGroups()

        assertThat(groups).containsExactly(
            listOf(UsPostMoreRow.DESCRIPTION, UsPostMoreRow.CLEAR_SCREEN, UsPostMoreRow.QUALITY),
            listOf(UsPostMoreRow.SAVE, UsPostMoreRow.COPY_LINK, UsPostMoreRow.SHARE),
            listOf(UsPostMoreRow.DELETE),
        ).inOrder()
    }

    @Test
    fun `clear screen reads show controls while full mode is on`() {
        assertThat(state().copy(reel = reel(fullMode = false)).rowGroups().first())
            .contains(UsPostMoreRow.CLEAR_SCREEN)
        val full = state().copy(reel = reel(fullMode = true)).rowGroups().first()
        assertThat(full).contains(UsPostMoreRow.SHOW_CONTROLS)
        assertThat(full).doesNotContain(UsPostMoreRow.CLEAR_SCREEN)
    }

    /** A "Description" that unfolds into nothing is a broken row, not a row. */
    @Test
    fun `description needs a caption to unfold`() {
        assertThat(state().copy(reel = reel(description = "")).rowGroups().first())
            .containsExactly(UsPostMoreRow.CLEAR_SCREEN, UsPostMoreRow.QUALITY).inOrder()
        assertThat(state().copy(reel = reel(description = "  ")).rowGroups().first())
            .doesNotContain(UsPostMoreRow.DESCRIPTION)
    }

    // ── Quality options ─────────────────────────────────────────────────

    @Test
    fun `quality options are auto first then the ladder tallest first, deduped`() {
        val options = reelQualityOptions(listOf(360, 720, 360, 1080, 720))

        assertThat(options).containsExactly(
            UsReelQuality.Auto,
            UsReelQuality.Height(1080),
            UsReelQuality.Height(720),
            UsReelQuality.Height(360),
        ).inOrder()
        assertThat(options.map { it.label }).containsExactly("Auto", "1080p", "720p", "360p").inOrder()
    }

    @Test
    fun `heights the player has not measured are dropped`() {
        assertThat(reelQualityOptions(listOf(0, -1, 720)))
            .containsExactly(UsReelQuality.Auto, UsReelQuality.Height(720)).inOrder()
    }

    /** The original MP4 has no ladder: Auto alone, and the row is inert. */
    @Test
    fun `an original-only reel offers auto alone and nothing to pick`() {
        val options = reelQualityOptions(listOf(720, 360), adaptive = false)

        assertThat(options).containsExactly(UsReelQuality.Auto)
        assertThat(reel(qualities = options).canPickQuality).isFalse()
    }

    @Test
    fun `a ladder not yet read is auto alone and nothing to pick either`() {
        assertThat(reel(qualities = reelQualityOptions(emptyList())).canPickQuality).isFalse()
        assertThat(reel(qualities = reelQualityOptions(listOf(720))).canPickQuality).isTrue()
    }
}
