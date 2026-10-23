package com.us.android.feature.feed.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedMedia
import com.us.android.core.model.FeedPoll
import com.us.android.core.model.FeedViewerState
import com.us.android.core.model.MembersOnly
import com.us.android.feature.feed.ui.reels.reelIsLocked
import org.junit.Test

/**
 * Home and Reels draw the members-only lock (paid memberships, 2026-10-23)
 * for a body the server withheld, and for a gated row with nothing to show —
 * never an empty page, never a player.
 */
class MembersOnlyLocksTest {

    private val video = listOf(FeedMedia(mediaId = "m1", kind = "video"))
    private val gated = MembersOnly(tierRequiredId = "t1")
    private val redacted = MembersOnly(tierRequiredId = "t1", bodyRedacted = true)

    private fun item(
        membersOnly: MembersOnly,
        media: List<FeedMedia> = emptyList(),
        text: String = "",
        poll: FeedPoll? = null,
    ) =
        FeedItem(
            id = "p1",
            authorId = "a1",
            author = FeedAuthor(id = "a1", displayName = "Asha"),
            text = text,
            visibility = "public",
            feedContentType = "flick",
            postType = "video",
            createdAt = "",
            isPinned = false,
            media = media,
            counts = FeedCounts(likes = 0, comments = 0, reposts = 0, views = 0),
            viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
            isRepostable = false,
            poll = poll,
            membersOnly = membersOnly,
        )

    @Test
    fun `a reel is locked when redacted, or gated without its video`() {
        assertThat(reelIsLocked(item(redacted))).isTrue()
        assertThat(reelIsLocked(item(redacted, media = video))).isTrue()
        assertThat(reelIsLocked(item(gated))).isTrue()
        assertThat(reelIsLocked(item(gated, media = video))).isFalse()
        assertThat(reelIsLocked(item(MembersOnly.None))).isFalse()
    }

    @Test
    fun `a Home page is locked when redacted, or gated with nothing to draw`() {
        assertThat(stageIsLocked(item(redacted, text = "should not be here"))).isTrue()
        assertThat(stageIsLocked(item(gated))).isTrue()
        assertThat(stageIsLocked(item(gated, text = "for members"))).isFalse()
        assertThat(stageIsLocked(item(gated, poll = FeedPoll("q", false, emptyList(), 0)))).isFalse()
        assertThat(stageIsLocked(item(MembersOnly.None))).isFalse()
    }
}
