package com.us.android.core.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** Heart only on posts, reels and videos; no repost on reels (founder, 24 Oct 2026). */
class PostHeartTest {

    @Test
    fun `the only post reaction is like`() {
        assertThat(POST_REACTION_LIKE).isEqualTo("like")
    }

    @Test
    fun `any older viewer reaction reads as liked`() {
        assertThat(isPostLiked(hasReacted = false, viewerReaction = "love")).isTrue()
        assertThat(isPostLiked(hasReacted = false, viewerReaction = "like")).isTrue()
        assertThat(isPostLiked(hasReacted = true, viewerReaction = null)).isTrue()
        assertThat(isPostLiked(hasReacted = false, viewerReaction = null)).isFalse()
        assertThat(isPostLiked(hasReacted = false, viewerReaction = " ")).isFalse()
    }

    @Test
    fun `reels are short, reel, flick and flick_embed, however written`() {
        for (type in listOf("short", "reel", "flick", "flick_embed", " Flick ", "REEL")) {
            assertThat(isReelContentType(type)).isTrue()
            assertThat(canRepostContentType(type)).isFalse()
        }
        for (type in listOf("post", "long_video", "poll", "", null)) {
            assertThat(isReelContentType(type)).isFalse()
            assertThat(canRepostContentType(type)).isTrue()
        }
    }

    @Test
    fun `a feed row of a reel offers no repost or quote, whatever its flag says`() {
        val reel = row(feedContentType = "flick")
        assertThat(reel.isRepostable).isTrue()
        assertThat(reel.allowsRepost).isFalse()
        assertThat(row(feedContentType = "post").allowsRepost).isTrue()
    }

    private fun row(feedContentType: String) = FeedItem(
        id = "p1",
        authorId = "author",
        author = FeedAuthor(id = "author", displayName = "Asha"),
        text = "",
        visibility = "public",
        feedContentType = feedContentType,
        postType = "video",
        createdAt = "",
        isPinned = false,
        media = emptyList(),
        counts = FeedCounts(likes = 0, comments = 0, reposts = 0, views = 0),
        viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
        isRepostable = true,
    )
}
