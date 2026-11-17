package com.us.android.feature.feed.ui.reels

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedDisclosure
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import org.junit.Test

/**
 * reels-38 (was A-2b): the reel player shows the author's declared labels —
 * "AI-generated" above all — in its caption block, as the web overlay does.
 */
class ReelCaptionLabelsTest {

    private fun reel(disclosure: FeedDisclosure) = FeedItem(
        id = "r1",
        authorId = "a",
        author = FeedAuthor(id = "a", displayName = "Asha", username = "asha"),
        text = "made this",
        visibility = "public",
        feedContentType = "flick",
        postType = "video",
        createdAt = "2026-10-18T10:00:00Z",
        isPinned = false,
        media = emptyList(),
        counts = FeedCounts(likes = 0, comments = 0, reposts = 0, views = 0),
        viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
        isRepostable = true,
        disclosure = disclosure,
    )

    @Test
    fun `an AI-generated reel carries the AI-generated label`() {
        val state = reelCaptionState(reel(FeedDisclosure(aiGenerated = true)), soundLabel = null)

        assertThat(state.labels).containsExactly("AI-generated")
    }

    @Test
    fun `a reel that declares nothing carries no label`() {
        assertThat(reelCaptionState(reel(FeedDisclosure()), soundLabel = null).labels).isEmpty()
    }
}
