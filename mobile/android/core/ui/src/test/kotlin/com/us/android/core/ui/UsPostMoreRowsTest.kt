package com.us.android.core.ui

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The post "more" sheet: WHICH rows it shows, per case, and the ORDER it
 * shows them in.
 *
 * founder, 2026-09-30: the sheet is ONE list in ascending alphabetical order
 * by the label the viewer reads, case-insensitive, like the web — on a feed
 * post, a reel and a long video alike. The groups of the Instagram capture
 * (2026-09-04) are gone, and so are the dividers between them. The order is
 * pinned once, for every surface, by the first test below; every test under
 * "Which rows, for whom" is about which rows appear and says nothing about
 * order.
 *
 * Which rows, on a FEED POST (unchanged):
 *
 *  - another person's post: Save, Copy link, Share; the feedback rows; the
 *    relationship row when the edge is known; Block and Report;
 *  - the viewer's own post: Save, Copy link, Share and Delete post — never a
 *    row that acts on "the author";
 *  - Unfollow only when the viewer follows, Follow only when they are
 *    known not to, neither while the edge is unknown;
 *  - "Why you're seeing this post" only when the server sent a sentence.
 *
 * founder, 2026-10-02: a REEL and a LONG VIDEO show the web's rows and the
 * web's words, and nothing else. The tests that pinned a reel as "the feed
 * post's rows plus four" were changed deliberately; the two lists are
 * pinned whole under their own headings below.
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
        username: String = "call_userb",
    ) = UsPostMoreState(
        postId = "p1",
        username = username,
        isOwnPost = own,
        isBookmarked = bookmarked,
        followRow = follow,
        reasonText = reason,
        link = postShareLink("p1"),
    )

    private fun reel(
        description: String = "sunday at the lake",
        fullMode: Boolean = false,
        qualities: List<UsReelQuality> = reelQualityOptions(listOf(360, 720)),
        selected: UsReelQuality = UsReelQuality.Auto,
        canUseSound: Boolean = false,
    ) = UsReelMoreState(
        description = description,
        fullMode = fullMode,
        qualities = qualities,
        selected = selected,
        canUseSound = canUseSound,
    )

    private fun UsPostMoreState.labels(): List<String> = rows().map { labelOf(it) }

    private fun longVideo(channel: String = "Clee Builds", shareHidden: Boolean = false) =
        UsLongVideoMoreState(channelName = channel, shareHidden = shareHidden)

    // ── The order ───────────────────────────────────────────────────────

    /** Every surface the sheet is opened from, in every state that changes which rows it holds. */
    private fun surfaces(): Map<String, UsPostMoreState> = buildMap {
        for (follow in UsPostMoreFollowRow.entries) {
            for (suggested in listOf(true, false)) {
                for (reason in listOf("", "Popular in Comedy")) {
                    val post = state(follow = follow, reason = reason).copy(suggested = suggested)
                    val key = "$follow suggested=$suggested reason=${reason.isNotBlank()}"
                    put("a feed post, $key", post)
                    put("a saved feed post, $key", post.copy(isBookmarked = true))
                    put("a long video, $key", post.copy(suggested = false, longVideo = longVideo()))
                    put("a reel, $key", post.copy(reel = reel(canUseSound = true)))
                    put("a reel with no caption, $key", post.copy(reel = reel(description = "")))
                }
            }
        }
        put("the viewer's own post", state(own = true))
        put("the viewer's own saved post", state(own = true, bookmarked = true))
        put("the viewer's own reel", state(own = true).copy(reel = reel(canUseSound = true)))
        put("the viewer's own long video", state(own = true).copy(longVideo = longVideo()))
        put("a long video with sharing off", state().copy(longVideo = longVideo(shareHidden = true)))
        // Names that would move a row if the label were compared by case, or without the name.
        put("an author named with capitals", state(username = "Zed", follow = UsPostMoreFollowRow.UNFOLLOW))
        put("an author named with a digit", state(username = "0x", follow = UsPostMoreFollowRow.FOLLOW))
        put("a channel named with a lower-case letter", state().copy(longVideo = longVideo(channel = "zed tv")))
    }

    @Test
    fun `the rows of every surface are in ascending alphabetical order by the label shown`() {
        val surfaces = surfaces()
        assertThat(surfaces.size).isAtLeast(60)

        for ((surface, state) in surfaces) {
            val labels = state.labels()

            assertThat(labels).isNotEmpty()
            assertThat(labels).containsNoDuplicates()
            // Said without the comparator the code uses: each label sorts after the one before it.
            labels.zipWithNext().forEach { (before, after) ->
                if (before.lowercase() >= after.lowercase()) {
                    throw AssertionError("$surface: \"$before\" is drawn before \"$after\" in $labels")
                }
            }
            // Ordering the rows neither drops one nor adds one.
            assertThat(state.rows()).containsExactlyElementsIn(state.offeredRows())
        }
    }

    /**
     * The whole list, once, so the order is also readable: another person's
     * reel. These are the web's rows and the web's words (founder,
     * 2026-10-02); the web's Audio track, Auto scroll, Captions and Playback
     * speed are absent because the Android reel player has no such setting.
     */
    @Test
    fun `another person's reel reads as the web's list, in alphabetical order`() {
        val labels = state(reason = "Trending").copy(reel = reel(canUseSound = true)).labels()

        assertThat(labels).containsExactly(
            "Description",
            "Don't recommend this channel",
            "Not interested",
            "Quality",
            "Report",
            "Use this sound",
        ).inOrder()
    }

    /** The web's watch page menu for a viewer: Block, Don't recommend, Not interested, Report, Share. */
    @Test
    fun `another person's long video reads as the web's list, in alphabetical order`() {
        val labels = state(follow = UsPostMoreFollowRow.UNFOLLOW).copy(longVideo = longVideo()).labels()

        assertThat(labels).containsExactly(
            "Block Clee Builds",
            "Don't recommend this channel",
            "Not interested",
            "Report",
            "Share",
        ).inOrder()
    }

    /** The label SHOWN is what sorts: on a feed post the rows about the author carry the handle. */
    @Test
    fun `a row about the author sorts by its own word, with the handle after it`() {
        val labels = state(follow = UsPostMoreFollowRow.UNFOLLOW).labels()

        assertThat(labels).containsAtLeast("Share", "Unfollow @call_userb").inOrder()
        assertThat(labels.first()).isEqualTo("Block @call_userb")
        assertThat(UsPostMoreRow.DONT_RECOMMEND.menuLabel("ada")).isEqualTo("Don't recommend @ada")
        assertThat(UsPostMoreRow.UNFOLLOW.menuLabel("ada")).isEqualTo("Unfollow @ada")
        assertThat(UsPostMoreRow.BLOCK.menuLabel("ada")).isEqualTo("Block @ada")
        assertThat(UsPostMoreRow.FOLLOW.menuLabel("ada")).isEqualTo("Follow")
        assertThat(UsPostMoreRow.REPORT.menuLabel("ada")).isEqualTo("Report")
    }

    /** The same row reads differently by surface, and the sheet draws and sorts by what it reads. */
    @Test
    fun `the words are the web's on a reel and a long video, and the handle's on a feed post`() {
        val post = state()
        val onReel = state().copy(reel = reel())
        val onVideo = state().copy(longVideo = longVideo())

        assertThat(post.labelOf(UsPostMoreRow.DONT_RECOMMEND)).isEqualTo("Don't recommend @call_userb")
        assertThat(onReel.labelOf(UsPostMoreRow.DONT_RECOMMEND)).isEqualTo("Don't recommend this channel")
        assertThat(onVideo.labelOf(UsPostMoreRow.DONT_RECOMMEND)).isEqualTo("Don't recommend this channel")

        assertThat(post.labelOf(UsPostMoreRow.BLOCK)).isEqualTo("Block @call_userb")
        assertThat(onVideo.labelOf(UsPostMoreRow.BLOCK)).isEqualTo("Block Clee Builds")
        // A channel with no name still names someone: the handle.
        assertThat(state().copy(longVideo = longVideo(channel = "")).labelOf(UsPostMoreRow.BLOCK))
            .isEqualTo("Block @call_userb")

        assertThat(state(own = true).labelOf(UsPostMoreRow.DELETE)).isEqualTo("Delete post")
        assertThat(state(own = true).copy(longVideo = longVideo()).labelOf(UsPostMoreRow.DELETE)).isEqualTo("Delete")
    }

    @Test
    fun `the sheet knows which of the three menus it is`() {
        assertThat(state().surface).isEqualTo(UsPostMoreSurface.POST)
        assertThat(state().copy(reel = reel()).surface).isEqualTo(UsPostMoreSurface.REEL)
        assertThat(state().copy(longVideo = longVideo()).surface).isEqualTo(UsPostMoreSurface.LONG_VIDEO)
    }

    /** Destructive rows keep their place in the alphabet: red is a colour, not a position. */
    @Test
    fun `report and delete are not held back to the end`() {
        val other = state(reason = "Trending").rows()
        assertThat(other.last()).isEqualTo(UsPostMoreRow.WHY)
        assertThat(other.indexOf(UsPostMoreRow.REPORT)).isLessThan(other.indexOf(UsPostMoreRow.SAVE))

        assertThat(state(own = true).rows()).containsExactly(
            UsPostMoreRow.COPY_LINK,
            UsPostMoreRow.DELETE,
            UsPostMoreRow.SAVE,
            UsPostMoreRow.SHARE,
        ).inOrder()
    }

    // ── Which rows, for whom ────────────────────────────────────────────

    @Test
    fun `another person's post shows the post's rows, the feedback rows and the rows about the author`() {
        // Followed author, reached through the follow: no Interested (it is
        // not a suggestion) and no Don't recommend (Unfollow is that).
        val rows = state(follow = UsPostMoreFollowRow.UNFOLLOW, reason = "From someone you follow")
            .copy(suggested = false)
            .rows()

        assertThat(rows).containsExactly(
            UsPostMoreRow.SAVE,
            UsPostMoreRow.COPY_LINK,
            UsPostMoreRow.SHARE,
            UsPostMoreRow.WHY,
            UsPostMoreRow.NOT_INTERESTED,
            UsPostMoreRow.UNFOLLOW,
            UsPostMoreRow.BLOCK,
            UsPostMoreRow.REPORT,
        )
    }

    /**
     * The founder looked for Block and Report on his OWN reel (2026-09-04)
     * and found them "missing": that is the rule, not a bug. Pinned both
     * ways so neither side of it can drift.
     */
    @Test
    fun `block and report are offered on another person's post and never on the viewer's own`() {
        for (follow in UsPostMoreFollowRow.entries) {
            val other = state(own = false, follow = follow).rows()
            assertThat(other).containsAtLeast(UsPostMoreRow.BLOCK, UsPostMoreRow.REPORT)

            val own = state(own = true, follow = follow).rows()
            assertThat(own).containsNoneOf(UsPostMoreRow.BLOCK, UsPostMoreRow.REPORT)
        }
        // A reel offers Report on another person's reel and never Block (the web's menu, 2026-10-02);
        // a long video offers both. The viewer's own has neither, on any surface.
        assertThat(state(own = false).copy(reel = reel()).rows()).contains(UsPostMoreRow.REPORT)
        assertThat(state(own = false).copy(reel = reel()).rows()).doesNotContain(UsPostMoreRow.BLOCK)
        assertThat(state(own = false).copy(longVideo = longVideo()).rows())
            .containsAtLeast(UsPostMoreRow.BLOCK, UsPostMoreRow.REPORT)
        assertThat(state(own = true).copy(reel = reel()).rows())
            .containsNoneOf(UsPostMoreRow.BLOCK, UsPostMoreRow.REPORT)
        assertThat(state(own = true).copy(longVideo = longVideo()).rows())
            .containsNoneOf(UsPostMoreRow.BLOCK, UsPostMoreRow.REPORT)
    }

    /** YouTube's "Don't recommend channel": other people's posts only. */
    @Test
    fun `don't recommend is offered on another person's post and is never on the viewer's own`() {
        assertThat(state(own = false).rows()).containsAtLeast(
            UsPostMoreRow.INTERESTED,
            UsPostMoreRow.NOT_INTERESTED,
            UsPostMoreRow.DONT_RECOMMEND,
        )

        assertThat(state(own = true).rows()).doesNotContain(UsPostMoreRow.DONT_RECOMMEND)
        assertThat(state(own = true).copy(reel = reel()).rows()).doesNotContain(UsPostMoreRow.DONT_RECOMMEND)
        assertThat(UsPostMoreRow.DONT_RECOMMEND.label).isEqualTo("Don't recommend")
    }

    @Test
    fun `the viewer's own post shows save, copy link, share and delete, and nothing about the author`() {
        val rows = state(own = true, reason = "Trending now", follow = UsPostMoreFollowRow.UNFOLLOW).rows()

        assertThat(rows).containsExactly(
            UsPostMoreRow.SAVE,
            UsPostMoreRow.COPY_LINK,
            UsPostMoreRow.SHARE,
            UsPostMoreRow.DELETE,
        )
    }

    @Test
    fun `delete is offered on the viewer's own post only`() {
        assertThat(state(own = false).rows()).doesNotContain(UsPostMoreRow.DELETE)
        assertThat(state(own = true).rows()).contains(UsPostMoreRow.DELETE)
        assertThat(UsPostMoreRow.DELETE.label).isEqualTo("Delete post")
    }

    @Test
    fun `a bookmarked post offers unsave in save's place`() {
        val saved = state(bookmarked = true).rows()
        assertThat(saved).containsAtLeast(UsPostMoreRow.UNSAVE, UsPostMoreRow.COPY_LINK, UsPostMoreRow.SHARE)
        assertThat(saved).doesNotContain(UsPostMoreRow.SAVE)

        val unsaved = state(bookmarked = false).rows()
        assertThat(unsaved).contains(UsPostMoreRow.SAVE)
        assertThat(unsaved).doesNotContain(UsPostMoreRow.UNSAVE)
    }

    @Test
    fun `unfollow when following, follow when not, neither while unknown`() {
        val following = state(follow = UsPostMoreFollowRow.UNFOLLOW).rows()
        assertThat(following).contains(UsPostMoreRow.UNFOLLOW)
        assertThat(following).doesNotContain(UsPostMoreRow.FOLLOW)

        val notFollowing = state(follow = UsPostMoreFollowRow.FOLLOW).rows()
        assertThat(notFollowing).contains(UsPostMoreRow.FOLLOW)
        assertThat(notFollowing).doesNotContain(UsPostMoreRow.UNFOLLOW)

        assertThat(state(follow = UsPostMoreFollowRow.HIDDEN).rows())
            .containsNoneOf(UsPostMoreRow.FOLLOW, UsPostMoreRow.UNFOLLOW)
    }

    /**
     * Interested is a signal about a suggestion, so a post from an account
     * the viewer already follows does not offer it; and muting an account the
     * viewer follows is what Unfollow is for, so Don't recommend appears only
     * while the author is not followed (founder, 2026-09-04).
     */
    @Test
    fun `interested is for suggestions and don't recommend is for accounts not followed`() {
        val followed = state(follow = UsPostMoreFollowRow.UNFOLLOW).copy(suggested = false).rows()
        assertThat(followed).contains(UsPostMoreRow.NOT_INTERESTED)
        assertThat(followed).containsNoneOf(UsPostMoreRow.INTERESTED, UsPostMoreRow.DONT_RECOMMEND)

        val suggestedStranger = state(follow = UsPostMoreFollowRow.FOLLOW).copy(suggested = true).rows()
        assertThat(suggestedStranger).containsAtLeast(
            UsPostMoreRow.INTERESTED,
            UsPostMoreRow.NOT_INTERESTED,
            UsPostMoreRow.DONT_RECOMMEND,
        )

        val unknownEdge = state(follow = UsPostMoreFollowRow.HIDDEN).copy(suggested = true).rows()
        assertThat(unknownEdge).contains(UsPostMoreRow.DONT_RECOMMEND)
    }

    @Test
    fun `the why row appears only with a reason sentence`() {
        assertThat(state(reason = "Popular in Comedy").rows()).contains(UsPostMoreRow.WHY)
        assertThat(state(reason = "").rows()).doesNotContain(UsPostMoreRow.WHY)
        assertThat(state(reason = "   ").rows()).doesNotContain(UsPostMoreRow.WHY)
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

    // ── The reel's rows (the web's More menu, 2026-10-02) ───────────────

    /**
     * Until 2026-10-02 a reel's sheet was the feed post's rows plus the
     * reel's own. It is now the web's menu and nothing else: the rows that
     * left (Save, Share, Copy link, Clear screen, Why, Interested, Follow,
     * Unfollow, Block, Delete post) are on the rail, the author row or the
     * profile, or are reached by a gesture.
     */
    @Test
    fun `another person's reel offers exactly the web's rows`() {
        val rows = state(follow = UsPostMoreFollowRow.UNFOLLOW, reason = "Trending")
            .copy(reel = reel(canUseSound = true))
            .rows()

        assertThat(rows).containsExactly(
            UsPostMoreRow.DESCRIPTION,
            UsPostMoreRow.DONT_RECOMMEND,
            UsPostMoreRow.NOT_INTERESTED,
            UsPostMoreRow.QUALITY,
            UsPostMoreRow.REPORT,
            UsPostMoreRow.USE_SOUND,
        )
    }

    /** The feedback rows are about someone else's reel; the viewer's own keeps the frame's rows only. */
    @Test
    fun `the viewer's own reel offers description, quality and use this sound, and nothing about an author`() {
        val rows = state(own = true).copy(reel = reel(canUseSound = true)).rows()

        assertThat(rows).containsExactly(
            UsPostMoreRow.DESCRIPTION,
            UsPostMoreRow.QUALITY,
            UsPostMoreRow.USE_SOUND,
        )
    }

    /** A row the web's reel menu does not have is not on this one, whatever the reel's state. */
    @Test
    fun `a reel never offers the feed post's rows`() {
        val gone = listOf(
            UsPostMoreRow.SAVE,
            UsPostMoreRow.UNSAVE,
            UsPostMoreRow.COPY_LINK,
            UsPostMoreRow.SHARE,
            UsPostMoreRow.WHY,
            UsPostMoreRow.INTERESTED,
            UsPostMoreRow.FOLLOW,
            UsPostMoreRow.UNFOLLOW,
            UsPostMoreRow.BLOCK,
            UsPostMoreRow.DELETE,
            UsPostMoreRow.CLEAR_SCREEN,
            UsPostMoreRow.SHOW_CONTROLS,
        )
        val reels = listOf(
            state(reason = "Trending").copy(reel = reel(canUseSound = true)),
            state(bookmarked = true, follow = UsPostMoreFollowRow.UNFOLLOW).copy(reel = reel()),
            state(own = true).copy(reel = reel(fullMode = true)),
        )
        for (reelState in reels) {
            assertThat(reelState.rows()).containsNoneIn(gone)
        }
    }

    /** "Don't recommend this channel" is offered on a reel whether or not the viewer follows, as the web offers it. */
    @Test
    fun `a reel offers don't recommend whatever the follow edge`() {
        for (follow in UsPostMoreFollowRow.entries) {
            assertThat(state(follow = follow).copy(reel = reel()).rows()).contains(UsPostMoreRow.DONT_RECOMMEND)
        }
    }

    /** The feed card's sheet holds no reel rows: no reel, none of them. */
    @Test
    fun `a post that is not a reel has no reel rows`() {
        val rows = state().rows()

        assertThat(rows).containsNoneOf(
            UsPostMoreRow.DESCRIPTION,
            UsPostMoreRow.CLEAR_SCREEN,
            UsPostMoreRow.SHOW_CONTROLS,
            UsPostMoreRow.QUALITY,
            UsPostMoreRow.USE_SOUND,
        )
    }

    // ── The long video's rows (the web watch page's More menu, 2026-10-02) ──

    @Test
    fun `another person's long video offers exactly the web's rows`() {
        val rows = state(reason = "Trending").copy(longVideo = longVideo()).rows()

        assertThat(rows).containsExactly(
            UsPostMoreRow.BLOCK,
            UsPostMoreRow.DONT_RECOMMEND,
            UsPostMoreRow.NOT_INTERESTED,
            UsPostMoreRow.REPORT,
            UsPostMoreRow.SHARE,
        )
    }

    @Test
    fun `the viewer's own long video offers delete and share, and nothing about an author`() {
        val rows = state(own = true).copy(longVideo = longVideo()).rows()

        assertThat(rows).containsExactly(UsPostMoreRow.DELETE, UsPostMoreRow.SHARE)
    }

    /** `hide_share`: the creator turned sharing off, so the row is absent, for the owner too. */
    @Test
    fun `a long video with sharing off has no share row`() {
        assertThat(state().copy(longVideo = longVideo(shareHidden = true)).rows()).doesNotContain(UsPostMoreRow.SHARE)
        assertThat(state(own = true).copy(longVideo = longVideo(shareHidden = true)).rows())
            .containsExactly(UsPostMoreRow.DELETE)
    }

    /** Save and Copy link are not in the web's watch menu: Save is on the action row under the video. */
    @Test
    fun `a long video never offers save, copy link, follow or the reel's rows`() {
        for (follow in UsPostMoreFollowRow.entries) {
            val rows = state(follow = follow, reason = "Trending").copy(longVideo = longVideo()).rows()
            assertThat(rows).containsNoneOf(
                UsPostMoreRow.SAVE,
                UsPostMoreRow.UNSAVE,
                UsPostMoreRow.COPY_LINK,
                UsPostMoreRow.FOLLOW,
                UsPostMoreRow.UNFOLLOW,
                UsPostMoreRow.WHY,
                UsPostMoreRow.INTERESTED,
                UsPostMoreRow.DESCRIPTION,
                UsPostMoreRow.QUALITY,
                UsPostMoreRow.USE_SOUND,
            )
        }
    }

    /** What "Don't recommend" says once the server has it names the channel on a long video. */
    @Test
    fun `the confirmation names the channel on a long video and the handle elsewhere`() {
        assertThat(state().copy(longVideo = longVideo()).dontRecommendDoneText())
            .isEqualTo("We won't recommend Clee Builds any more")
        assertThat(state().dontRecommendDoneText()).isEqualTo("We won't recommend posts from @call_userb")
        assertThat(state().copy(reel = reel()).dontRecommendDoneText())
            .isEqualTo("We won't recommend posts from @call_userb")
    }

    /** A "Description" that unfolds into nothing is a broken row, not a row. */
    @Test
    fun `description needs a caption to unfold`() {
        assertThat(state().copy(reel = reel(description = "a caption")).rows()).contains(UsPostMoreRow.DESCRIPTION)
        assertThat(state().copy(reel = reel(description = "")).rows()).doesNotContain(UsPostMoreRow.DESCRIPTION)
        assertThat(state().copy(reel = reel(description = "  ")).rows()).doesNotContain(UsPostMoreRow.DESCRIPTION)
    }

    /**
     * "Use this sound" is the host's to offer (the creator allows reuse, or
     * the reel is the viewer's own, and it is not still processing); the
     * sheet draws the row exactly when it is, on a reel and nowhere else.
     */
    @Test
    fun `use this sound is a reel's row, shown when the host offers it`() {
        assertThat(state().copy(reel = reel(canUseSound = true)).rows()).contains(UsPostMoreRow.USE_SOUND)
        assertThat(state(own = true).copy(reel = reel(canUseSound = true)).rows()).contains(UsPostMoreRow.USE_SOUND)
        assertThat(state().copy(reel = reel(canUseSound = false)).rows()).doesNotContain(UsPostMoreRow.USE_SOUND)
        assertThat(state().copy(reel = reel()).rows()).doesNotContain(UsPostMoreRow.USE_SOUND)
        assertThat(state().rows()).doesNotContain(UsPostMoreRow.USE_SOUND)
        assertThat(UsPostMoreRow.USE_SOUND.label).isEqualTo("Use this sound")
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
