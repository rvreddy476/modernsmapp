package com.us.android.feature.tube.ui.watch

import com.google.common.truth.Truth.assertThat
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.feed.data.VideoLibraryState
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import org.junit.Test

/**
 * The action row under a long video: the web watch page's row and words
 * (founder, 2026-10-02), and what each control shows for what the viewer has
 * done, including after the video is closed and opened again.
 *
 * 2026-10-02: the row was Like · Comment · Share · Save · More. The tests
 * that pinned that order were changed deliberately with the row.
 */
class WatchActionsTest {

    private val nothing = WatchViewerState(liked = false, disliked = false, queued = false, saved = false)

    private fun video(viewer: FeedViewerState = FeedViewerState(false, false, false)) = FeedItem(
        id = "v1",
        authorId = "a1",
        author = FeedAuthor(id = "a1", displayName = "Clee", username = "clee", avatarMediaId = null),
        text = "",
        visibility = "public",
        feedContentType = "long_video",
        postType = "video",
        createdAt = "2026-09-27T10:00:00Z",
        isPinned = false,
        media = emptyList(),
        counts = FeedCounts(likes = 3, comments = 0, reposts = 0, views = 10),
        viewer = viewer,
        isRepostable = false,
        score = null,
    )

    @Test
    fun `the row is the web's, in the web's order, with the web's words`() {
        val actions = watchActions(likes = 8_800, viewer = nothing)

        assertThat(actions.map { it.kind }).containsExactly(
            WatchActionKind.LIKE,
            WatchActionKind.DISLIKE,
            WatchActionKind.WATCH_LATER,
            WatchActionKind.ADD_TO_COLLECTION,
            WatchActionKind.SAVE,
            WatchActionKind.MORE,
        ).inOrder()
        assertThat(actions.map { it.label })
            .containsExactly("8.8K", "Dislike", "Watch later", "Add to collection", "Save", "More")
            .inOrder()
        assertThat(actions.none { it.lit }).isTrue()
    }

    @Test
    fun `nothing to count reads Like, and a saved video reads Saved`() {
        val actions = watchActions(likes = 0, viewer = nothing.copy(saved = true))

        assertThat(actions.first().label).isEqualTo("Like")
        assertThat(actions.first { it.kind == WatchActionKind.SAVE }.label).isEqualTo("Saved")
    }

    @Test
    fun `each control is lit by its own state and by nothing else`() {
        fun lit(viewer: WatchViewerState) = watchActions(1, viewer).filter { it.lit }.map { it.kind }

        assertThat(lit(nothing.copy(liked = true))).containsExactly(WatchActionKind.LIKE)
        assertThat(lit(nothing.copy(disliked = true))).containsExactly(WatchActionKind.DISLIKE)
        assertThat(lit(nothing.copy(queued = true))).containsExactly(WatchActionKind.WATCH_LATER)
        assertThat(lit(nothing.copy(saved = true))).containsExactly(WatchActionKind.SAVE)
    }

    /** A lit control undoes on the next tap, and a screen reader is told so. */
    @Test
    fun `a lit control is described by what the next tap does`() {
        val off = watchActions(12, nothing).associate { it.kind to it.description() }
        assertThat(off[WatchActionKind.LIKE]).isEqualTo("Like")
        assertThat(off[WatchActionKind.DISLIKE]).isEqualTo("Dislike")
        assertThat(off[WatchActionKind.WATCH_LATER]).isEqualTo("Watch later")
        assertThat(off[WatchActionKind.SAVE]).isEqualTo("Save")
        assertThat(off[WatchActionKind.ADD_TO_COLLECTION]).isEqualTo("Add to collection")
        assertThat(off[WatchActionKind.MORE]).isEqualTo("More")

        val all = WatchViewerState(liked = true, disliked = true, queued = true, saved = true)
        val on = watchActions(12, all).associate { it.kind to it.description() }
        assertThat(on[WatchActionKind.LIKE]).isEqualTo("Remove like")
        assertThat(on[WatchActionKind.DISLIKE]).isEqualTo("Remove dislike")
        assertThat(on[WatchActionKind.WATCH_LATER]).isEqualTo("Remove from Watch later")
        assertThat(on[WatchActionKind.SAVE]).isEqualTo("Remove from Saved")
    }

    // ── What the row shows when a video is opened ───────────────────────

    /** No tap this session: the row is exactly what the server said about the viewer. */
    @Test
    fun `a video opened fresh shows the server's saved, watch later, liked and disliked`() {
        val saved = video(
            FeedViewerState(isBookmarked = true, hasReacted = false, hasReposted = false, isQueued = true),
        )

        val state = watchViewerState(saved, EngagementOverlay(), VideoLibraryState())

        assertThat(state).isEqualTo(WatchViewerState(liked = false, disliked = false, queued = true, saved = true))

        val disliked = video(FeedViewerState(false, false, false, hasDisliked = true))
        assertThat(watchViewerState(disliked, EngagementOverlay(), VideoLibraryState()).disliked).isTrue()
    }

    /**
     * The case that was wrong on a reopen: the video is opened again from a
     * list row that predates the tap (it still says "not saved, not queued").
     * The session's own values win over the row's.
     */
    @Test
    fun `a video reopened from a stale row still shows what the viewer did this session`() {
        val staleRow = video(FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false))

        val state = watchViewerState(
            item = staleRow,
            overlay = EngagementOverlay(reacted = true, bookmarked = true),
            library = VideoLibraryState(queued = mapOf("v1" to true), disliked = mapOf("v1" to false)),
        )

        assertThat(state).isEqualTo(WatchViewerState(liked = true, disliked = false, queued = true, saved = true))
    }

    /** And the other way: taken out this session, while the row still says it is in. */
    @Test
    fun `a video the viewer un-saved and took out of watch later shows neither`() {
        val staleRow = video(
            FeedViewerState(isBookmarked = true, hasReacted = true, hasReposted = false, isQueued = true),
        )

        val state = watchViewerState(
            item = staleRow,
            overlay = EngagementOverlay(reacted = false, bookmarked = false),
            library = VideoLibraryState(queued = mapOf("v1" to false)),
        )

        assertThat(state).isEqualTo(nothing)
    }

    /** Another video's taps say nothing about this one. */
    @Test
    fun `state kept for another video does not leak onto this one`() {
        val state = watchViewerState(
            item = video(),
            overlay = EngagementOverlay(),
            library = VideoLibraryState(queued = mapOf("other" to true), disliked = mapOf("other" to true)),
        )

        assertThat(state).isEqualTo(nothing)
    }

    /**
     * The post detail read on open replaces what the list row said about the
     * viewer, and leaves the row's media alone so playback is not restarted.
     */
    @Test
    fun `the detail's viewer state is laid over the row without touching what plays`() {
        val row = video(FeedViewerState(false, false, false)).copy(title = "From the list")
        val detail = video(
            FeedViewerState(isBookmarked = true, hasReacted = true, hasReposted = false, isQueued = true),
        ).copy(title = "From the detail", counts = FeedCounts(likes = 9, comments = 2, reposts = 0, views = 99))

        val merged = row.withViewerStateOf(detail)

        assertThat(merged.viewer).isEqualTo(detail.viewer)
        assertThat(merged.counts.likes).isEqualTo(9)
        assertThat(merged.title).isEqualTo("From the list")
        assertThat(merged.media).isEqualTo(row.media)
    }
}
