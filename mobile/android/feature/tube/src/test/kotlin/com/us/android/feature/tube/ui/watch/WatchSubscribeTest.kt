package com.us.android.feature.tube.ui.watch

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedChannel
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import org.junit.Test

/** Which channel the watch screen's Subscribe points at (2026-09-12). */
class WatchSubscribeTest {

    private fun video(channel: FeedChannel? = null) = FeedItem(
        id = "p",
        authorId = "a",
        author = FeedAuthor(id = "a", displayName = "Ada"),
        text = "",
        visibility = "public",
        feedContentType = "long_video",
        postType = "video",
        createdAt = "",
        isPinned = false,
        media = emptyList(),
        counts = FeedCounts(0, 0, 0, 0),
        viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
        isRepostable = true,
        channel = channel,
    )

    @Test
    fun `the embedded channel is the ref when the row carries one`() {
        assertThat(subscribeRef(video(FeedChannel(userId = "chan", name = "Ada's channel", handle = "ada"))))
            .isEqualTo("chan")
    }

    /** A long video always has a channel server-side, so the author id resolves to the same channel. */
    @Test
    fun `the author is the ref when the row carries no channel, or a channel with no id`() {
        assertThat(subscribeRef(video())).isEqualTo("a")
        assertThat(subscribeRef(video(FeedChannel(userId = "", name = "x", handle = "x")))).isEqualTo("a")
    }
}
