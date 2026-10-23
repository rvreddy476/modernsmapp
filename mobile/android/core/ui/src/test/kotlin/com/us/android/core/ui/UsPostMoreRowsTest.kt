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
 * founder, 2026-10-02 (later the same day): a REEL and a LONG VIDEO show the
 * SAME rows — Block channel, Copy link, Description, Don't recommend this
 * channel, Not interested, Quality, Report, Share — from one function. Only
 * "Use this sound" (a reel with a reusable sound) and the owner's Delete may
 * differ. The tests that pinned the two as different lists, copied from the
 * web's two menus that morning, were changed deliberately; the one list is
 * pinned whole under "A video's rows" below.
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
        shareHidden: Boolean = false,
    ) = UsReelMoreState(
        description = description,
        fullMode = fullMode,
        qualities = qualities,
        selected = selected,
        canUseSound = canUseSound,
        shareHidden = shareHidden,
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
                    put("a long video, $key", post.copy(suggested = false, reel = reel(), longVideo = longVideo()))
                    put("a reel, $key", post.copy(reel = reel(canUseSound = true)))
                    put("a reel with no caption, $key", post.copy(reel = reel(description = "")))
                }
            }
        }
        put("the viewer's own post", state(own = true))
        put("the viewer's own saved post", state(own = true, bookmarked = true))
        put("the viewer's own reel", state(own = true).copy(reel = reel(canUseSound = true)))
        put("the viewer's own long video", state(own = true).copy(reel = reel(), longVideo = longVideo()))
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
     * The whole list, once, so the order is also readable. founder,
     * 2026-10-02: "Block channel, Copy link, Description, Don't recommend this
     * channel, Not interested, Quality, Report, Share" — on a reel AND on a
     * long video. The reel here also has a sound to reuse, the one row that
     * may be extra.
     */
    @Test
    fun `another person's reel reads as the founder's list, in alphabetical order`() {
        val labels = state(reason = "Trending").copy(reel = reel(canUseSound = true)).labels()

        assertThat(labels).containsExactly(
            "Block @call_userb",
            "Copy link",
            "Description",
            "Don't recommend this channel",
            "Not interested",
            "Quality",
            "Report",
            "Use this sound",
        ).inOrder()
    }

    @Test
    fun `another person's long video reads as the same list, in the same order`() {
        val labels = state(follow = UsPostMoreFollowRow.UNFOLLOW).copy(reel = reel(), longVideo = longVideo()).labels()

        assertThat(labels).containsExactly(
            "Block Clee Builds",
            "Copy link",
            "Description",
            "Don't recommend this channel",
            "Not interested",
            "Quality",
            "Report",
        ).inOrder()
    }

    /** The label SHOWN is what sorts: on a feed post the rows about the author carry the handle. */
    @Test
    fun `a row about the author sorts by its own word, with the handle after it`() {
        val labels = state(follow = UsPostMoreFollowRow.UNFOLLOW).labels()

        assertThat(labels).containsAtLeast("Report", "Unfollow @call_userb").inOrder()
        assertThat(labels.first()).isEqualTo("Copy link")
        assertThat(UsPostMoreRow.DONT_RECOMMEND.menuLabel("ada")).isEqualTo("Don't recommend @ada")
        assertThat(UsPostMoreRow.UNFOLLOW.menuLabel("ada")).isEqualTo("Unfollow @ada")
        assertThat(UsPostMoreRow.BLOCK.menuLabel("ada")).isEqualTo("Block @ada")
        assertThat(UsPostMoreRow.FOLLOW.menuLabel("ada")).isEqualTo("Follow")
        assertThat(UsPostMoreRow.REPORT.menuLabel("ada")).isEqualTo("Report")
    }

    /** The same row reads differently on a feed post and on a video, and the sheet draws and sorts by what it reads. */
    @Test
    fun `a video's words are the web's and the same on a reel and a long video, a feed post's carry the handle`() {
        val post = state()
        val onReel = state().copy(reel = reel(), channelName = "Clee Builds")
        val onVideo = state().copy(reel = reel(), longVideo = longVideo())

        assertThat(post.labelOf(UsPostMoreRow.DONT_RECOMMEND)).isEqualTo("Don't recommend @call_userb")
        assertThat(post.labelOf(UsPostMoreRow.BLOCK)).isEqualTo("Block @call_userb")
        assertThat(state(own = true).labelOf(UsPostMoreRow.DELETE)).isEqualTo("Delete post")

        for (video in listOf(onReel, onVideo)) {
            assertThat(video.labelOf(UsPostMoreRow.DONT_RECOMMEND)).isEqualTo("Don't recommend this channel")
            assertThat(video.labelOf(UsPostMoreRow.BLOCK)).isEqualTo("Block Clee Builds")
            assertThat(video.labelOf(UsPostMoreRow.DELETE)).isEqualTo("Delete")
            assertThat(video.labelOf(UsPostMoreRow.COPY_LINK)).isEqualTo("Copy link")
        }
        // Every label of the two is the same word, row for row.
        assertThat(onReel.labels()).isEqualTo(onVideo.labels())
    }

    /** The confirmation names who is blocked, in the row's words. */
    @Test
    fun `the block confirmation names who is blocked`() {
        assertThat(state().blockConfirmTitle()).isEqualTo("Block @call_userb?")
        assertThat(state().copy(reel = reel()).blockConfirmTitle()).isEqualTo("Block @call_userb?")
        assertThat(state().copy(reel = reel(), channelName = "Ada").blockConfirmTitle()).isEqualTo("Block Ada?")
        assertThat(state().copy(longVideo = longVideo()).blockConfirmTitle()).isEqualTo("Block Clee Builds?")
        // A channel with no name still names someone: the handle.
        assertThat(state().copy(longVideo = longVideo(channel = "")).blockConfirmTitle())
            .isEqualTo("Block @call_userb?")
    }

    @Test
    fun `the delete confirmation and its pill say what was deleted`() {
        val own = state(own = true)

        assertThat(own.deleteConfirmTitle()).isEqualTo("Delete post?")
        assertThat(own.deletedText()).isEqualTo("Post deleted")
        assertThat(own.copy(reel = reel()).deleteConfirmTitle()).isEqualTo("Delete reel?")
        assertThat(own.copy(reel = reel()).deletedText()).isEqualTo("Reel deleted")
        assertThat(own.copy(longVideo = longVideo()).deleteConfirmTitle()).isEqualTo("Delete video?")
        assertThat(own.copy(longVideo = longVideo()).deletedText()).isEqualTo("Video deleted")
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
            UsPostMoreRow.WHY,
            UsPostMoreRow.UNFOLLOW,
            UsPostMoreRow.REPORT,
        )
    }

    /**
     * Report is offered on another person's post and never on the viewer's
     * own (2026-09-04). Block is never on the sheet (founder, 2026-10-19): as
     * on TikTok and YouTube it lives on the person's profile.
     */
    @Test
    fun `report is offered on another person's content only, and block on a video only`() {
        for (follow in UsPostMoreFollowRow.entries) {
            val other = state(own = false, follow = follow).rows()
            assertThat(other).contains(UsPostMoreRow.REPORT)
            assertThat(other).doesNotContain(UsPostMoreRow.BLOCK)

            val own = state(own = true, follow = follow).rows()
            assertThat(own).containsNoneOf(UsPostMoreRow.BLOCK, UsPostMoreRow.REPORT)
        }
        // A video (a reel, a long video) carries Block, as the web's menu does
        // (founder, 2026-10-21) — never on the viewer's own.
        for (video in listOf(state().copy(reel = reel()), state().copy(longVideo = longVideo()))) {
            assertThat(video.rows()).containsAtLeast(UsPostMoreRow.BLOCK, UsPostMoreRow.NOT_INTERESTED, UsPostMoreRow.REPORT)
            assertThat(video.copy(isOwnPost = true).rows()).containsNoneOf(UsPostMoreRow.BLOCK, UsPostMoreRow.REPORT)
        }
    }

    /** YouTube's "Don't recommend channel": other people's posts only. */
    @Test
    fun `don't recommend is offered on another person's post and is never on the viewer's own`() {
        assertThat(state(own = false).rows()).containsAtLeast(
            UsPostMoreRow.INTERESTED,
            UsPostMoreRow.DONT_RECOMMEND,
        )

        assertThat(state(own = true).rows()).doesNotContain(UsPostMoreRow.DONT_RECOMMEND)
        assertThat(state(own = true).copy(reel = reel()).rows()).doesNotContain(UsPostMoreRow.DONT_RECOMMEND)
        assertThat(UsPostMoreRow.DONT_RECOMMEND.label).isEqualTo("Don't recommend")
    }

    @Test
    fun `the viewer's own post shows save, copy link and delete, and nothing about the author`() {
        val rows = state(own = true, reason = "Trending now", follow = UsPostMoreFollowRow.UNFOLLOW).rows()

        assertThat(rows).containsExactly(
            UsPostMoreRow.SAVE,
            UsPostMoreRow.COPY_LINK,
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
        assertThat(saved).containsAtLeast(UsPostMoreRow.UNSAVE, UsPostMoreRow.COPY_LINK)
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
        assertThat(followed).doesNotContain(UsPostMoreRow.NOT_INTERESTED)
        assertThat(followed).containsNoneOf(UsPostMoreRow.INTERESTED, UsPostMoreRow.DONT_RECOMMEND)

        val suggestedStranger = state(follow = UsPostMoreFollowRow.FOLLOW).copy(suggested = true).rows()
        assertThat(suggestedStranger).containsAtLeast(
            UsPostMoreRow.INTERESTED,
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
            "Fake job or scam link" to "scam_link",
            "Impersonation" to "impersonation",
            "Self-harm" to "self_harm",
            "Intellectual property" to "intellectual_property",
            "Other" to "other",
        ).inOrder()
        assertThat(UsReportReason.entries.filter { it.asksForDetails }).containsExactly(UsReportReason.OTHER)
    }

    @Test
    fun `the scam link reason is offered on posts and profiles, never on a comment`() {
        assertThat(UsReportReason.forSubject("post")).contains(UsReportReason.SCAM_LINK)
        assertThat(UsReportReason.forSubject("account")).contains(UsReportReason.SCAM_LINK)
        assertThat(UsReportReason.forSubject("comment")).doesNotContain(UsReportReason.SCAM_LINK)
        assertThat(UsReportReason.forSubject("comment")).hasSize(UsReportReason.entries.size - 1)
    }

    @Test
    fun `copy link puts the post's public web permalink on the clipboard`() {
        val id = "b2f7d0c1-5c1e-4a0e-9d1a-0c7f6f1e2a33"
        // The permalink, then the web's share tail (`?ref=<id>&utm_source=share`, 2026-10-22).
        assertThat(postShareLink(id)).matches("https://cleestudio.com/post/$id\\?ref=[a-z0-9]{10}&utm_source=share")
        // The old /p/ route was served by nothing: every copied link 404'd (T-07).
        assertThat(postShareLink(id)).doesNotContain("/p/")
    }

    // ── A video's rows: a reel and a long video, ONE list (2026-10-02) ──

    /** The rows the founder listed for another person's video; Share left More on 2026-10-19. */
    private val viewerRows = listOf(
        UsPostMoreRow.BLOCK,
        UsPostMoreRow.COPY_LINK,
        UsPostMoreRow.DESCRIPTION,
        UsPostMoreRow.DONT_RECOMMEND,
        UsPostMoreRow.NOT_INTERESTED,
        UsPostMoreRow.QUALITY,
        UsPostMoreRow.REPORT,
    )

    /** A reel and the long video beside it, in the same state: what must not differ. */
    private fun bothVideos(
        base: UsPostMoreState,
        video: UsReelMoreState = reel(),
    ): Pair<UsPostMoreState, UsPostMoreState> =
        base.copy(reel = video, channelName = "Clee Builds") to base.copy(reel = video, longVideo = longVideo())

    /** Every state that could change a list: owner or not, each follow edge, saved or not, with or without a reason. */
    private fun everyBase(): List<UsPostMoreState> = listOf(false, true).flatMap { own ->
        UsPostMoreFollowRow.entries.flatMap { follow ->
            listOf(false, true).flatMap { bookmarked ->
                listOf("", "Trending").map { reason -> state(own, bookmarked, follow, reason) }
            }
        }
    }

    /** Every video group that could change a list: with or without a caption, sharing on or off. */
    private fun everyVideo(): List<UsReelMoreState> = listOf("a caption", "").flatMap { description ->
        listOf(false, true).map { shareHidden -> reel(description = description, shareHidden = shareHidden) }
    }

    /**
     * THE RULE (founder, 2026-10-02): "More options must be the SAME in Reels
     * and in long videos." Every state that could change a list, on both
     * surfaces: the two lists are equal, row for row and in order.
     */
    @Test
    fun `a reel and a long video offer the same rows in the same order, in every state`() {
        var compared = 0
        for (base in everyBase()) {
            for (video in everyVideo()) {
                val (onReel, onVideo) = bothVideos(base, video)

                assertThat(onReel.rows()).isEqualTo(onVideo.rows())
                assertThat(onReel.labels()).isEqualTo(onVideo.labels())
                compared++
            }
        }
        assertThat(compared).isEqualTo(96)
    }

    @Test
    fun `another person's video offers exactly the web's rows, on a reel and on a long video`() {
        val (onReel, onVideo) = bothVideos(state(follow = UsPostMoreFollowRow.UNFOLLOW, reason = "Trending"))

        assertThat(onReel.rows()).containsExactlyElementsIn(viewerRows).inOrder()
        assertThat(onVideo.rows()).containsExactlyElementsIn(viewerRows).inOrder()
    }

    /** Context row one: a reel whose sound may be reused adds "Use this sound", and nothing else moves. */
    @Test
    fun `use this sound is the one row a reel may add`() {
        val rows = state().copy(reel = reel(canUseSound = true)).rows()

        assertThat(rows).containsExactlyElementsIn(viewerRows + UsPostMoreRow.USE_SOUND)
        assertThat(rows - UsPostMoreRow.USE_SOUND).isEqualTo(viewerRows)
    }

    /**
     * Context row two: the owner. Delete stands in for the four rows that act
     * on "the channel" or judge the video; the rest of the list is unchanged,
     * on both surfaces.
     */
    @Test
    fun `the owner's video offers delete in place of the rows about someone else, on both surfaces`() {
        val (onReel, onVideo) = bothVideos(state(own = true))
        val ownerRows = listOf(
            UsPostMoreRow.COPY_LINK,
            UsPostMoreRow.DELETE,
            UsPostMoreRow.DESCRIPTION,
            UsPostMoreRow.QUALITY,
        )

        assertThat(onReel.rows()).containsExactlyElementsIn(ownerRows).inOrder()
        assertThat(onVideo.rows()).containsExactlyElementsIn(ownerRows).inOrder()
        assertThat(onReel.labels()).containsExactly("Copy link", "Delete", "Description", "Quality").inOrder()
    }

    /** The follow edge, the bookmark and the "why" sentence shape a feed post's menu. They do not shape a video's. */
    @Test
    fun `a video's rows do not change with the follow edge, the bookmark or the reason`() {
        for (follow in UsPostMoreFollowRow.entries) {
            for (bookmarked in listOf(false, true)) {
                val base = state(bookmarked = bookmarked, follow = follow, reason = "Popular").copy(suggested = true)
                val (onReel, onVideo) = bothVideos(base)

                assertThat(onReel.rows()).isEqualTo(viewerRows)
                assertThat(onVideo.rows()).isEqualTo(viewerRows)
            }
        }
    }

    /** What a video's menu never holds: the feed post's own rows, and the reel's old Clear screen. */
    @Test
    fun `a video never offers the feed post's rows`() {
        val gone = listOf(
            UsPostMoreRow.SAVE,
            UsPostMoreRow.UNSAVE,
            UsPostMoreRow.WHY,
            UsPostMoreRow.INTERESTED,
            UsPostMoreRow.FOLLOW,
            UsPostMoreRow.UNFOLLOW,
            UsPostMoreRow.CLEAR_SCREEN,
            UsPostMoreRow.SHOW_CONTROLS,
        )
        val videos = listOf(
            state(reason = "Trending").copy(reel = reel(canUseSound = true)),
            state(bookmarked = true, follow = UsPostMoreFollowRow.UNFOLLOW).copy(reel = reel()),
            state(own = true).copy(reel = reel(fullMode = true)),
            state(reason = "Trending").copy(reel = reel(), longVideo = longVideo()),
            state(own = true).copy(longVideo = longVideo()),
        )
        for (video in videos) {
            assertThat(video.rows()).containsNoneIn(gone)
        }
    }

    /** The feed card's sheet holds no video rows: no video, none of them. */
    @Test
    fun `a post that is not a video has no video rows`() {
        val rows = state().rows()

        assertThat(rows).containsNoneOf(
            UsPostMoreRow.DESCRIPTION,
            UsPostMoreRow.CLEAR_SCREEN,
            UsPostMoreRow.SHOW_CONTROLS,
            UsPostMoreRow.QUALITY,
            UsPostMoreRow.USE_SOUND,
        )
    }

    /**
     * Founder, 2026-10-19: Share is not a More row on any surface — a reel and
     * a feed post have it on the rail, a long video on its action row.
     */
    @Test
    fun `no more sheet offers share, whatever the surface or the share switch`() {
        for (hidden in listOf(true, false)) {
            val (onReel, onVideo) = bothVideos(state(), reel(shareHidden = hidden))
            assertThat(onReel.rows()).doesNotContain(UsPostMoreRow.SHARE)
            assertThat(onVideo.rows()).doesNotContain(UsPostMoreRow.SHARE)
            assertThat(state(own = true).copy(reel = reel(shareHidden = hidden)).rows()).doesNotContain(UsPostMoreRow.SHARE)
        }
        assertThat(state().rows()).doesNotContain(UsPostMoreRow.SHARE)
        assertThat(state(own = true).rows()).doesNotContain(UsPostMoreRow.SHARE)
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

    /**
     * Description is ALWAYS listed (2026-10-02), so the list is the same from
     * one video to the next; a video with no caption unfolds "No description"
     * rather than nothing. It replaces "a caption-less reel has no row".
     */
    @Test
    fun `description is listed with or without a caption`() {
        for (description in listOf("a caption", "", "  ")) {
            val (onReel, onVideo) = bothVideos(state(), reel(description = description))

            assertThat(onReel.rows()).contains(UsPostMoreRow.DESCRIPTION)
            assertThat(onVideo.rows()).contains(UsPostMoreRow.DESCRIPTION)
        }
        assertThat(NO_DESCRIPTION).isEqualTo("No description")
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
        // The watch screen never offers it: a long video's sound is not reused.
        assertThat(state().copy(reel = reel(), longVideo = longVideo()).rows()).doesNotContain(UsPostMoreRow.USE_SOUND)
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
