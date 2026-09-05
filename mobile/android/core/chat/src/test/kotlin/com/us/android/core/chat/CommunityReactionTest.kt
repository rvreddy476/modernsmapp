package com.us.android.core.chat

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.CommunityUpdate
import com.us.android.core.chat.data.ReactionCount
import com.us.android.core.chat.data.reactionAfterTap
import com.us.android.core.chat.data.withViewerReaction
import org.junit.Test

/**
 * One reaction per viewer: the same emoji again removes it, another
 * replaces it, and the counts move exactly as the server will report them.
 */
class CommunityReactionTest {

    private fun update(viewer: String?, vararg counts: Pair<String, Int>) = CommunityUpdate(
        id = "u",
        channelId = "c",
        authorId = "a",
        updateType = "post",
        title = null,
        body = "b",
        mediaIds = emptyList(),
        event = null,
        isPinned = false,
        publishedAt = "",
        viewCount = 0,
        reactionCount = counts.sumOf { it.second },
        reactions = counts.map { ReactionCount(it.first, it.second) },
        viewerReaction = viewer,
        canEdit = false,
    )

    private fun CommunityUpdate.count(emoji: String) = reactions.firstOrNull { it.emoji == emoji }?.count ?: 0

    @Test
    fun `tapping the viewer's own emoji removes it`() {
        assertThat(update("👍", "👍" to 3).reactionAfterTap("👍")).isNull()
    }

    @Test
    fun `tapping another emoji replaces the viewer's own`() {
        assertThat(update("👍", "👍" to 3).reactionAfterTap("🔥")).isEqualTo("🔥")
    }

    @Test
    fun `tapping with no reaction yet sets one`() {
        assertThat(update(null).reactionAfterTap("❤️")).isEqualTo("❤️")
    }

    @Test
    fun `setting a first reaction adds one to its count`() {
        val next = update(null, "👍" to 2).withViewerReaction("👍")
        assertThat(next.viewerReaction).isEqualTo("👍")
        assertThat(next.count("👍")).isEqualTo(3)
        assertThat(next.reactionCount).isEqualTo(3)
    }

    @Test
    fun `replacing moves one count from the old emoji to the new`() {
        val next = update("👍", "👍" to 3, "🔥" to 1).withViewerReaction("🔥")
        assertThat(next.count("👍")).isEqualTo(2)
        assertThat(next.count("🔥")).isEqualTo(2)
        assertThat(next.reactionCount).isEqualTo(4)
        assertThat(next.viewerReaction).isEqualTo("🔥")
    }

    @Test
    fun `removing drops the emoji entirely when the viewer was its only reactor`() {
        val next = update("🔥", "👍" to 3, "🔥" to 1).withViewerReaction(null)
        assertThat(next.reactions.map { it.emoji }).containsExactly("👍")
        assertThat(next.reactionCount).isEqualTo(3)
        assertThat(next.viewerReaction).isNull()
    }

    @Test
    fun `applying the same reaction again changes nothing`() {
        val before = update("👍", "👍" to 3)
        assertThat(before.withViewerReaction("👍")).isEqualTo(before)
    }
}
