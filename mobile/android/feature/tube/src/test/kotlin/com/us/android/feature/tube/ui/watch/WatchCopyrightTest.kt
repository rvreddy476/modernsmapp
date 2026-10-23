package com.us.android.feature.tube.ui.watch

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.CopyrightReview
import com.us.android.core.model.CopyrightReviewState
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import org.junit.Test

/**
 * The watch page's copyright notice (copyright title review, 2026-10-24):
 * `copyright_review` is only on the AUTHOR's own post detail (post-service
 * post.go:615), never a list row, so it comes in with the detail laid over
 * the row — and only the author is shown it.
 */
class WatchCopyrightTest {

    private val held = CopyrightReview(CopyrightReviewState.PENDING, "Leo", "2026-10-24T08:00:01Z")

    private fun video(review: CopyrightReview? = null) = FeedItem(
        id = "v1",
        authorId = "a1",
        author = FeedAuthor(id = "a1", displayName = "Kiran", username = "kiran", avatarMediaId = null),
        text = "",
        visibility = "public",
        feedContentType = "long_video",
        postType = "video",
        createdAt = "2026-10-24T08:00:00Z",
        isPinned = false,
        media = emptyList(),
        counts = FeedCounts(likes = 0, comments = 0, reposts = 0, views = 0),
        viewer = FeedViewerState(false, false, false),
        isRepostable = false,
        score = null,
        copyrightReview = review,
    )

    @Test
    fun `the detail brings the review to the row the screen is playing`() {
        val merged = video().withViewerStateOf(video(held))
        assertThat(merged.copyrightReview).isEqualTo(held)
        // A detail with none clears a stale one (a review that lifted itself).
        assertThat(video(held).withViewerStateOf(video()).copyrightReview).isNull()
    }

    @Test
    fun `only the author is shown it`() {
        assertThat(watchCopyrightReview(video(held), ownUserId = "a1")).isEqualTo(held)
        assertThat(watchCopyrightReview(video(held), ownUserId = "someone")).isNull()
        assertThat(watchCopyrightReview(video(held), ownUserId = "")).isNull()
        assertThat(watchCopyrightReview(video(), ownUserId = "a1")).isNull()
    }
}
