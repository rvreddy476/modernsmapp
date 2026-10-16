package com.us.android.feature.tube.ui.hub

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.tube.data.hub.HubDraft
import com.us.android.feature.tube.data.hub.HubInboxRow
import com.us.android.feature.tube.data.hub.HubLibraryRow
import com.us.android.feature.tube.data.hub.HubVisibility
import org.junit.Test

/** T-31: the hub's row lines and chart geometry — pure, so the numbers on screen are pinned here. */
class CreatorHubPresentationTest {

    private val row = HubLibraryRow(
        id = "v1",
        title = "One",
        contentType = "long_video",
        visibility = HubVisibility.UNLISTED,
        scheduledAt = null,
        publishedAt = null,
        createdAt = "not a time",
        viewCount = 1_234,
        likeCount = 4,
        commentCount = 0,
        processingStatus = "ready",
    )

    @Test
    fun `a library row reads its visibility and its counts the web's way`() {
        assertThat(libraryMeta(row)).isEqualTo("Unlisted")
        assertThat(libraryCounts(row)).isEqualTo("1.2K views · 4 likes · 0 comments")
    }

    @Test
    fun `a draft's line is its status, with when it was last saved when known`() {
        val draft = HubDraft(
            id = "d1",
            title = "Draft",
            visibility = "",
            status = "blocked",
            scheduleAt = null,
            publishedPostId = null,
            blockedReason = null,
            createdAt = "",
            updatedAt = "",
        )
        assertThat(draftMeta(draft)).isEqualTo("Held")
    }

    @Test
    fun `a comment's line is the author, the post and the time, skipping what is missing`() {
        val comment = HubInboxRow(
            commentId = "c1",
            postId = "p1",
            body = "Great one",
            authorId = "u1",
            authorName = "Asha",
            createdAt = "",
            postTitle = "One",
            postContentType = "long_video",
            authorReplied = false,
        )
        assertThat(inboxMeta(comment)).isEqualTo("Asha · One")
        assertThat(inboxMeta(comment.copy(authorName = ""))).isEqualTo("One")
    }

    @Test
    fun `the chart spaces days evenly, puts the busiest day at 90 percent and zero on the baseline`() {
        val points = sparklinePoints(listOf(0L, 10L, 5L), width = 200f, height = 100f)
        assertThat(points.map { it.first }).containsExactly(0f, 100f, 200f).inOrder()
        assertThat(points[0].second).isEqualTo(100f)
        assertThat(points[1].second).isWithin(0.001f).of(10f)
        assertThat(points[2].second).isWithin(0.001f).of(55f)
    }

    @Test
    fun `one day sits in the middle, all-zero days lie flat, nothing draws nothing`() {
        assertThat(sparklinePoints(listOf(7L), 200f, 100f)).containsExactly(100f to 10f)
        assertThat(sparklinePoints(listOf(0L, 0L), 200f, 100f).map { it.second }).containsExactly(100f, 100f)
        assertThat(sparklinePoints(emptyList(), 200f, 100f)).isEmpty()
    }

    @Test
    fun `day labels read as a short date, anything else as it came`() {
        val expected = java.time.LocalDate.of(2026, 10, 1).format(java.time.format.DateTimeFormatter.ofPattern("MMM d"))
        assertThat(dayLabel("2026-10-01")).isEqualTo(expected)
        assertThat(dayLabel("week 1")).isEqualTo("week 1")
    }

    @Test
    fun `the hub's pages and library tabs, in order and in the web's words`() {
        assertThat(HubSection.entries.map { it.label })
            .containsExactly("Overview", "Library", "Conversations", "Sounds")
            .inOrder()
        assertThat(HubLibraryTab.entries.map { it.label }).containsExactly("Videos", "Reels", "Drafts").inOrder()
    }
}
