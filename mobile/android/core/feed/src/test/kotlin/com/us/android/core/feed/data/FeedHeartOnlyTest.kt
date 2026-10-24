package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.data.dto.FeedAuthorDto
import com.us.android.core.feed.data.dto.FeedItemDto
import com.us.android.core.model.allowsRepost
import org.junit.Test

/** Heart only, and no repost on reels, as the feed maps a row (founder, 24 Oct 2026). */
class FeedHeartOnlyTest {

    private fun item(contentType: String = "post", hasReacted: Boolean = false, viewerReaction: String? = null) = FeedItemDto(
        id = "p1",
        authorId = "a1",
        text = "",
        author = FeedAuthorDto(id = "a1", displayName = "A"),
        contentType = contentType,
        hasReacted = hasReacted,
        viewerReaction = viewerReaction,
    )

    @Test
    fun `an older non-like reaction reads as liked`() {
        assertThat(item(viewerReaction = "haha").toDomain().viewer.hasReacted).isTrue()
        assertThat(item(viewerReaction = null).toDomain().viewer.hasReacted).isFalse()
    }

    @Test
    fun `a reel row allows no repost, a post and a long video do`() {
        assertThat(item(contentType = "flick").toDomain().allowsRepost).isFalse()
        assertThat(item(contentType = "reel").toDomain().allowsRepost).isFalse()
        assertThat(item(contentType = "post").toDomain().allowsRepost).isTrue()
        assertThat(item(contentType = "long_video").toDomain().allowsRepost).isTrue()
    }
}
